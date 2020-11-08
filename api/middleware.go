package api

import (
	"bytes"
	"context"
	"io"
	"io/ioutil"
	"net/http"

	jwt "github.com/dgrijalva/jwt-go"
	"github.com/netlify/gocommerce/assetstores"
	"github.com/netlify/gocommerce/conf"
	gcontext "github.com/netlify/gocommerce/context"
	"github.com/netlify/gocommerce/mailer"
	"github.com/netlify/gocommerce/models"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

const (
	jwsSignatureHeaderName = "x-nf-sign"
)

type NetlifyMicroserviceClaims struct {
	SiteURL    string `json:"site_url"`
	InstanceID string `json:"id"`
	NetlifyID  string `json:"netlify_id"`
	jwt.StandardClaims
}

func addGetBody(w http.ResponseWriter, req *http.Request) (context.Context, error) {
	if req.Body == nil || req.Body == http.NoBody {
		return nil, badRequestError("request must provide a body")
	}

	buf, err := ioutil.ReadAll(req.Body)
	if err != nil {
		return nil, internalServerError("Error reading body").WithInternalError(err)
	}
	req.GetBody = func() (io.ReadCloser, error) {
		return ioutil.NopCloser(bytes.NewReader(buf)), nil
	}
	req.Body, _ = req.GetBody()
	return req.Context(), nil
}

func (api *API) verifyOperatorRequest(w http.ResponseWriter, req *http.Request) (context.Context, error) {
	c, _, err := api.extractOperatorRequest(w, req)
	return c, err
}

func (api *API) extractOperatorRequest(w http.ResponseWriter, req *http.Request) (context.Context, string, error) {
	token, err := extractBearerToken(req)
	if err != nil {
		return nil, token, err
	}
	if token == "" || token != api.config.OperatorToken {
		return nil, token, unauthorizedError("Request does not include an Operator token")
	}
	return req.Context(), token, nil
}

func (api *API) loadInstanceConfig(w http.ResponseWriter, r *http.Request) (context.Context, error) {
	ctx := r.Context()

	signature := r.Header.Get(jwsSignatureHeaderName)
	if signature == "" {
		return nil, badRequestError("Netlify microservice headers missing")
	}

	claims := NetlifyMicroserviceClaims{}
	p := jwt.Parser{ValidMethods: []string{jwt.SigningMethodHS256.Name}}
	_, err := p.ParseWithClaims(signature, &claims, func(token *jwt.Token) (interface{}, error) {
		return []byte(api.config.OperatorToken), nil
	})
	if err != nil {
		return nil, badRequestError("Operator microservice headers are invalid: %v", err)
	}

	instanceID := claims.InstanceID
	if instanceID == "" {
		return nil, badRequestError("Instance ID is missing")
	}

	logEntrySetField(r, "instance_id", instanceID)
	logEntrySetField(r, "netlify_id", claims.NetlifyID)
	instance, err := models.GetInstance(api.db, instanceID)
	if err != nil {
		if models.IsNotFoundError(err) {
			return nil, notFoundError("Unable to locate site configuration")
		}
		return nil, internalServerError("Database error loading instance").WithInternalError(err)
	}

	config, err := instance.Config()
	if err != nil {
		return nil, internalServerError("Error loading environment config").WithInternalError(err)
	}
	if claims.SiteURL != "" {
		config.SiteURL = claims.SiteURL
	}
	logEntrySetField(r, "site_url", config.SiteURL)

	ctx, err = WithInstanceConfig(ctx, api.config.SMTP, config, instanceID, getLogEntry(r))
	if err != nil {
		return nil, internalServerError("Error loading instance config").WithInternalError(err)
	}

	return ctx, nil
}

func WithInstanceConfig(ctx context.Context, smtp conf.SMTPConfiguration, config *conf.Configuration, instanceID string, log logrus.FieldLogger) (context.Context, error) {
	ctx = gcontext.WithInstanceID(ctx, instanceID)
	ctx = gcontext.WithConfig(ctx, config)
	ctx, err := gcontext.WithCoupons(ctx, config)
	if err != nil {
		return nil, err
	}

	mailer := mailer.NewMailer(smtp, config, log)
	ctx = gcontext.WithMailer(ctx, mailer)

	store, err := assetstores.NewStore(config)
	if err != nil {
		return nil, errors.Wrap(err, "Error initializing asset store")
	}
	ctx = gcontext.WithAssetStore(ctx, store)

	provs, err := createPaymentProviders(config)
	if err != nil {
		return nil, errors.Wrap(err, "error creating payment providers")
	}
	if len(provs) == 0 {
		return nil, errors.New("No payment providers enabled")
	}
	ctx = gcontext.WithPaymentProviders(ctx, provs)

	return ctx, nil
}
