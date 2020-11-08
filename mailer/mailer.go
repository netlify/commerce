package mailer

import (
	"fmt"
	"log"
	"time"

	"github.com/netlify/gocommerce/conf"
	"github.com/netlify/gocommerce/models"
	"github.com/netlify/mailme"
	"github.com/sirupsen/logrus"
)

// Mailer will send mail and use templates from the site for easy mail styling
type Mailer interface {
	OrderConfirmationMail(transaction *models.Transaction) error
	OrderReceivedMail(transaction *models.Transaction) error
	OrderConfirmationMailBody(transaction *models.Transaction, templateURL string) (string, error)
}

type mailer struct {
	Config         *conf.Configuration
	TemplateMailer *mailme.Mailer
}

// MailSubjects holds the subject lines for the emails
type MailSubjects struct {
	OrderConfirmationMail string
}

// NewMailer returns a new authlify mailer
func NewMailer(smtp conf.SMTPConfiguration, instanceConfig *conf.Configuration, log logrus.FieldLogger) Mailer {
	if smtp.Host == "" && instanceConfig.SMTP.Host == "" {
		return newNoopMailer()
	}

	smtpHost := instanceConfig.SMTP.Host
	if smtpHost == "" {
		smtpHost = smtp.Host
	}
	smtpPort := instanceConfig.SMTP.Port
	if smtpPort == 0 {
		smtpPort = smtp.Port
	}
	smtpUser := instanceConfig.SMTP.User
	if smtpUser == "" {
		smtpUser = smtp.User
	}
	smtpPass := instanceConfig.SMTP.Pass
	if smtpPass == "" {
		smtpPass = smtp.Pass
	}
	smtpAdminEmail := instanceConfig.SMTP.AdminEmail
	if smtpAdminEmail == "" {
		smtpAdminEmail = smtp.AdminEmail
	}

	return &mailer{
		Config: instanceConfig,
		TemplateMailer: &mailme.Mailer{
			Host:    smtpHost,
			Port:    smtpPort,
			User:    smtpUser,
			Pass:    smtpPass,
			From:    smtpAdminEmail,
			BaseURL: instanceConfig.SiteURL,
			FuncMap: map[string]interface{}{
				"dateFormat":     dateFormat,
				"price":          price,
				"hasProductType": hasProductType,
			},
			Logger: log,
		},
	}
}

func dateFormat(layout string, date time.Time) string {
	return date.Format(layout)
}

func price(amount uint64, currency string) string {
	switch currency {
	case "USD":
		return fmt.Sprintf("$%.2f", float64(amount)/100)
	case "EUR":
		return fmt.Sprintf("%.2f€", float64(amount)/100)
	default:
		return fmt.Sprintf("%.2f %v", float64(amount)/100, currency)
	}
}

func hasProductType(order *models.Order, productType string) bool {
	for _, item := range order.LineItems {
		if item.Type == productType {
			return true
		}
	}
	return false
}

const defaultConfirmationTemplate = `<h2>Thank you for your order!</h2>

<ul>
{{ range .Order.LineItems }}
<li>{{ .Title }} <strong>{{ .Quantity }} x {{ .Price }}</strong></li>
{{ end }}
</ul>

<p>Total amount: <strong>{{ .Order.Total }}</strong></p>
`

// OrderConfirmationMail sends an order confirmation to the user
func (m *mailer) OrderConfirmationMail(transaction *models.Transaction) error {
	log.Printf("Sending order confirmation to %v with template %v", transaction.Order.Email, m.Config.Mailer.Templates.OrderConfirmation)
	return m.TemplateMailer.Mail(
		transaction.Order.Email,
		withDefault(m.Config.Mailer.Subjects.OrderConfirmation, "Order Confirmation"),
		m.Config.Mailer.Templates.OrderConfirmation,
		defaultConfirmationTemplate,
		map[string]interface{}{
			"SiteURL":     m.Config.SiteURL,
			"Order":       transaction.Order,
			"Transaction": transaction,
		},
	)
}

const defaultReceivedTemplate = `<h2>Order Received From {{ .Order.Email }}</h2>

<ul>
{{ range .Order.LineItems }}
<li>{{ .Title }} <strong>{{ .Quantity }} x {{ .Price }}</strong></li>
{{ end }}
</ul>

<p>Total amount: <strong>{{ .Order.Total }}</strong></p>
`

// OrderReceivedMail sends a notification to the shop admin
func (m *mailer) OrderReceivedMail(transaction *models.Transaction) error {
	return m.TemplateMailer.Mail(
		m.TemplateMailer.From,
		withDefault(m.Config.Mailer.Subjects.OrderReceived, "Order Received From {{ .Order.Email }}"),
		m.Config.Mailer.Templates.OrderReceived,
		defaultReceivedTemplate,
		map[string]interface{}{
			"SiteURL":     m.Config.SiteURL,
			"Order":       transaction.Order,
			"Transaction": transaction,
		},
	)
}

func (m *mailer) OrderConfirmationMailBody(transaction *models.Transaction, templateURL string) (string, error) {
	if templateURL == "" {
		templateURL = m.Config.Mailer.Templates.OrderConfirmation
	}

	return m.TemplateMailer.MailBody(templateURL, defaultReceivedTemplate, map[string]interface{}{
		"SiteURL":     m.Config.SiteURL,
		"Order":       transaction.Order,
		"Transaction": transaction,
	})
}

func withDefault(value string, defaultValue string) string {
	if value == "" {
		return defaultValue
	}
	return value
}
