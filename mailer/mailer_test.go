package mailer

import (
	"testing"

	"github.com/netlify/gocommerce/conf"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func TestNoopMailer(t *testing.T) {
	smtp := conf.SMTPConfiguration{}
	conf := &conf.Configuration{}
	m := NewMailer(smtp, conf, logrus.New())
	assert.IsType(t, &noopMailer{}, m)
}

func TestTemplateMailer(t *testing.T) {
	smtp := conf.SMTPConfiguration{
		Host: "localhost",
		Port: 25,
	}
	conf := &conf.Configuration{}
	conf.SMTP.AdminEmail = "test@example.com"
	m := NewMailer(smtp, conf, logrus.New())
	assert.IsType(t, &mailer{}, m)
}
