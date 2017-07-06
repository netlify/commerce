package cmd

import (
	"fmt"

	"github.com/Sirupsen/logrus"
	"github.com/netlify/gocommerce/api"
	"github.com/netlify/gocommerce/assetstores"
	"github.com/netlify/gocommerce/conf"
	"github.com/netlify/gocommerce/mailer"
	"github.com/netlify/gocommerce/models"
	"github.com/spf13/cobra"
	stripe "github.com/stripe/stripe-go"

	paypalsdk "github.com/logpacker/PayPal-Go-SDK"
)

var serveCmd = cobra.Command{
	Use:  "serve",
	Long: "Start API server",
	Run: func(cmd *cobra.Command, args []string) {
		execWithConfig(cmd, serve)
	},
}

func serve(config *conf.Configuration) {
	db, err := models.Connect(config)
	if err != nil {
		logrus.Fatalf("Error opening database: %+v", err)
	}

	bgDB, err := models.Connect(config)
	if err != nil {
		logrus.Fatalf("Error opening database: %+v", err)
	}

	var paypal *paypalsdk.Client
	if config.Payment.Paypal.ClientID != "" && config.Payment.Paypal.Secret != "" {
		var ppEnv string
		if config.Payment.Paypal.Env == "production" {
			ppEnv = paypalsdk.APIBaseLive
		} else {
			ppEnv = paypalsdk.APIBaseSandBox
		}

		paypal, err = paypalsdk.NewClient(
			config.Payment.Paypal.ClientID,
			config.Payment.Paypal.Secret,
			ppEnv,
		)
		if err != nil {
			logrus.Fatalf("Error configuring paypal: %+v", err)
		}
		_, err = paypal.GetAccessToken()
		if err != nil {
			logrus.Fatalf("Error authorizing with paypal: %+v", err)
		}
	}

	mailer := mailer.NewMailer(config)

	store, err := assetstores.NewStore(config)
	if err != nil {
		logrus.Fatalf("Error initializing asset store: %+v", err)
	}

	api := api.NewAPIWithVersion(config, db.Debug(), paypal, mailer, store, Version)

	stripe.Key = config.Payment.Stripe.SecretKey

	l := fmt.Sprintf("%v:%v", config.API.Host, config.API.Port)
	logrus.Infof("GoCommerce API started on: %s", l)

	models.RunHooks(bgDB, logrus.WithField("component", "hooks"), config.Webhooks.Secret)

	api.ListenAndServe(l)
}
