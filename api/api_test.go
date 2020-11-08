package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/netlify/gocommerce/conf"
)

func TestTraceWrapper(t *testing.T) {
	hook := test.NewGlobal()
	globalConfig := new(conf.GlobalConfiguration)
	globalConfig.MultiInstanceMode = true
	globalConfig.OperatorToken = "token"

	config := new(conf.Configuration)
	config.Payment.Stripe.Enabled = true
	config.Payment.Stripe.SecretKey = "secret"

	ctx, err := WithInstanceConfig(context.Background(), globalConfig.SMTP, config, "", logrus.New())
	require.NoError(t, err)
	api := NewAPIWithVersion(ctx, globalConfig, logrus.StandardLogger(), nil, "")

	server := httptest.NewServer(api.handler)
	defer server.Close()

	client := http.Client{}
	req, err := http.NewRequest(http.MethodGet, server.URL+"/", nil)
	require.NoError(t, err)
	req.Header.Add("Authorization", "Bearer token")
	rsp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rsp.StatusCode)
	assert.True(t, len(hook.Entries) > 0)

	for _, entry := range hook.Entries {
		if _, ok := entry.Data["request_id"]; !ok {
			assert.Fail(t, "expected entry: request_id")
		}
		expected := map[string]string{
			"method": "GET",
			"path":   "/",
		}
		for k, v := range expected {
			if value, ok := entry.Data[k]; ok {
				assert.Equal(t, v, value)
			} else {
				assert.Fail(t, "expected entry: "+k)
			}
		}
	}
}
