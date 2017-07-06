package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Sirupsen/logrus"
	"github.com/guregu/kami"
	"github.com/jinzhu/gorm"
	"github.com/logpacker/PayPal-Go-SDK"
	"github.com/pborman/uuid"
	"github.com/stripe/stripe-go"
	"github.com/stripe/stripe-go/charge"
	"github.com/stripe/stripe-go/refund"

	"github.com/netlify/gocommerce/models"
)

// MaxConcurrentLookups controls the number of simultaneous HTTP Order lookups
const MaxConcurrentLookups = 10

// PaymentParams holds the parameters for creating a payment
type PaymentParams struct {
	Amount       uint64 `json:"amount"`
	Currency     string `json:"currency"`
	StripeToken  string `json:"stripe_token"`
	PaypalID     string `json:"paypal_payment_id"`
	PaypalUserID string `json:"paypal_user_id"`
}

type paymentProvider interface {
	charge(amount uint64, currency, token, userToken string) (string, error)
	refund(amount uint64, id string) (string, error)
}

// PaymentListForUser is the endpoint for listing transactions for a user.
// The ID in the claim and the ID in the path must match (or have admin override)
func (a *API) PaymentListForUser(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	log, claims, userID, httpErr := initEndpoint(ctx, w, "user_id", true)
	if httpErr != nil {
		sendJSON(w, httpErr.Code, httpErr)
		return
	}

	if userID != claims.ID && !isAdmin(ctx) {
		log.Warn("Illegal access attempt")
		unauthorizedError(w, "Can't access payments for this user")
		return
	}

	trans, httpErr := queryForTransactions(a.db, log, "user_id = ?", userID)
	if httpErr != nil {
		sendJSON(w, httpErr.Code, httpErr)
		return
	}
	sendJSON(w, 200, trans)
}

// PaymentListForOrder is the endpoint for listing transactions for an order. You must be the owner
// of the order (user_id) or an admin. Listing the payments for an anon order.
func (a *API) PaymentListForOrder(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	log, claims, orderID, httpErr := initEndpoint(ctx, w, "order_id", true)
	if httpErr != nil {
		sendJSON(w, httpErr.Code, httpErr)
		return
	}

	order, httpErr := queryForOrder(a.db, orderID, log)
	if httpErr != nil {
		sendJSON(w, httpErr.Code, httpErr)
		return
	}

	isAdmin := isAdmin(ctx)

	// now we need to check if you're allowed to look at this order
	if order.UserID == "" && !isAdmin {
		// anon order ~ only accessible by an admin
		log.Warn("Queried for an anonymous order but not as admin")
		sendJSON(w, 401, unauthorizedError(w, "Anonymous orders must be accessed by admins"))
		return
	}

	if order.UserID != claims.ID && !isAdmin {
		log.Warnf("Attempt to access order as %s, but order.UserID is %s", claims.ID, order.UserID)
		sendJSON(w, 401, unauthorizedError(w, "Anonymous orders must be accessed by admins"))
		return
	}

	log.Debugf("Returning %d transactions", len(order.Transactions))
	sendJSON(w, 200, order.Transactions)
}

// PaymentCreate is the endpoint for creating a payment for an order
func (a *API) PaymentCreate(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	params := &PaymentParams{Currency: "USD"}
	jsonDecoder := json.NewDecoder(r.Body)
	err := jsonDecoder.Decode(params)
	if err != nil {
		badRequestError(w, fmt.Sprintf("Could not read params: %v", err))
		return
	}

	if params.StripeToken == "" {
		if a.paypal == nil {
			badRequestError(w, "Payments requires a stripe_token")
			return
		} else if params.PaypalID == "" || params.PaypalUserID == "" {
			badRequestError(w, "Payments requires a stripe_token or a paypal_payment_id and paypal_user_id pair")
			return
		}
	}

	orderID := kami.Param(ctx, "order_id")
	tx := a.db.Begin()
	order := &models.Order{}

	if result := tx.Preload("LineItems").Preload("BillingAddress").First(order, "id = ?", orderID); result.Error != nil {
		tx.Rollback()
		if result.RecordNotFound() {
			notFoundError(w, "No order with this ID found")
		} else {
			internalServerError(w, fmt.Sprintf("Error during database query: %v", result.Error))
		}
		return
	}

	if order.PaymentState == models.PaidState {
		tx.Rollback()
		badRequestError(w, "This order has already been paid")
		return
	}

	if order.Currency != params.Currency {
		tx.Rollback()
		badRequestError(w, fmt.Sprintf("Currencies doesn't match - %v vs %v", order.Currency, params.Currency))
		return
	}

	token := getToken(ctx)
	if order.UserID == "" {
		if token != nil {
			claims := token.Claims.(*JWTClaims)
			order.UserID = claims.ID
			tx.Save(order)
		}
	} else {
		if token == nil {
			tx.Rollback()
			unauthorizedError(w, "You must be logged in to pay for this order")
			return
		}
		claims := token.Claims.(*JWTClaims)
		if order.UserID != claims.ID {
			tx.Rollback()
			unauthorizedError(w, "You must be logged in to pay for this order")
			return
		}
	}

	err = a.verifyAmount(ctx, order, params.Amount)
	if err != nil {
		tx.Rollback()
		internalServerError(w, fmt.Sprintf("We failed to authorize the amount for this order: %v", err))
		return
	}
	tr := models.NewTransaction(order)

	var chType ChargerType
	var paymentToken string
	var paymentUser string
	if params.StripeToken != "" {
		chType = StripeChargerType
		paymentToken = params.StripeToken
		order.PaymentProcessor = "stripe"
	} else if a.paypal != nil && params.PaypalID != "" {
		chType = PaypalChargerType
		paymentToken = params.PaypalID
		paymentUser = params.PaypalUserID
		order.PaymentProcessor = "paypal"
	}

	processorID, err := getCharger(ctx, chType).charge(params.Amount, params.Currency, paymentToken, paymentUser)
	tr.ProcessorID = processorID

	if err != nil {
		tr.FailureCode = "500"
		tr.FailureDescription = err.Error()
		tr.Status = "failed"
	} else {
		tr.Status = "pending"
	}
	tx.Create(tr)

	if err != nil {
		tx.Commit()
		internalServerError(w, fmt.Sprintf("There was an error charging your card: %v", err))
		return
	}

	order.PaymentState = models.PaidState
	tx.Save(order)

	if a.config.Webhooks.Payment != "" {
		hook := models.NewHook("payment", a.config.Webhooks.Payment, order.UserID, order)
		tx.Save(hook)
	}

	tx.Commit()

	go func() {
		err1 := a.mailer.OrderConfirmationMail(tr)
		err2 := a.mailer.OrderReceivedMail(tr)

		if err1 != nil || err2 != nil {
			a.log.Errorf("Error sending order confirmation mails: %v %v", err1, err2)
		}
	}()

	sendJSON(w, 200, tr)
}

// PaymentList will list all the payments that meet the criteria. It is only available to admins
func (a *API) PaymentList(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	log, _, httpErr := requireAdmin(ctx, "")
	if httpErr != nil {
		sendJSON(w, httpErr.Code, httpErr)
		return
	}

	query, err := parsePaymentQueryParams(a.db, r.URL.Query())
	if err != nil {
		log.WithError(err).Info("Malformed request")
		badRequestError(w, err.Error())
		return
	}

	trans, httpErr := queryForTransactions(query, log, "", "")
	if httpErr != nil {
		sendJSON(w, httpErr.Code, httpErr)
		return
	}
	sendJSON(w, 200, trans)
}

func (a *API) PaymentView(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	if trans, httpErr := a.getTransaction(ctx); httpErr != nil {
		sendJSON(w, httpErr.Code, httpErr)
	} else {
		sendJSON(w, 200, trans)
	}
}

func (a *API) PaymentRefund(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	params := &PaymentParams{Currency: "USD"}
	jsonDecoder := json.NewDecoder(r.Body)
	err := jsonDecoder.Decode(params)
	if err != nil {
		badRequestError(w, "Could not read params: %v", err)
		return
	}

	trans, httpErr := a.getTransaction(ctx)
	if httpErr != nil {
		sendJSON(w, httpErr.Code, httpErr)
		return
	}

	if trans.Currency != params.Currency {
		badRequestError(w, "Currencies doesn't match - %v vs %v", trans.Currency, params.Currency)
		return
	}

	if params.Amount <= 0 || params.Amount > trans.Amount {
		badRequestError(w, "The balance of the refund must be between 0 and the total amount")
		return
	}

	if trans.FailureCode != "" {
		badRequestError(w, "Can't refund a failed transaction")
		return
	}

	if trans.Status != models.PaidState {
		badRequestError(w, "Can't refund a transaction that hasn't been paid")
		return
	}

	log := getLogger(ctx)
	order, httpErr := queryForOrder(a.db, trans.OrderID, log)
	if httpErr != nil {
		sendJSON(w, httpErr.Code, httpErr)
		return
	}

	if order.PaymentProcessor == "paypal" {
		badRequestError(w, "Can't refund a paypal transaction")
		return
	}

	// ok make the refund
	m := &models.Transaction{
		ID:       uuid.NewRandom().String(),
		Amount:   params.Amount,
		Currency: params.Currency,
		UserID:   trans.UserID,
		OrderID:  trans.OrderID,
		Type:     models.RefundTransactionType,
		Status:   models.PendingState,
	}

	tx := a.db.Begin()
	tx.Create(m)
	log.Debug("Starting refund to stripe")
	// TODO ~ refund via paypal #58
	stripeID, err := getCharger(ctx, StripeChargerType).refund(params.Amount, trans.ProcessorID)
	if err != nil {
		log.WithError(err).Info("Failed to refund value")
		m.FailureCode = "500"
		m.FailureDescription = err.Error()
		m.Status = models.FailedState
	} else {
		m.ProcessorID = stripeID
		m.Status = models.PaidState
	}

	log.Infof("Finished transaction with stripe: %s", m.ProcessorID)
	tx.Save(m)
	if a.config.Webhooks.Refund != "" {
		hook := models.NewHook("refund", a.config.Webhooks.Refund, m.UserID, m)
		tx.Save(hook)
	}
	tx.Commit()
	sendJSON(w, http.StatusOK, m)
}

// ------------------------------------------------------------------------------------------------
// Helpers
// ------------------------------------------------------------------------------------------------
func (a *API) getTransaction(ctx context.Context) (*models.Transaction, *HTTPError) {
	log, payID, httpErr := requireAdmin(ctx, "pay_id")
	if httpErr != nil {
		return nil, httpErr
	}

	trans := &models.Transaction{ID: payID}
	if rsp := a.db.First(trans); rsp.Error != nil {
		if rsp.RecordNotFound() {
			log.Infof("Failed to find transaction %s", payID)
			return nil, httpError(404, "Transaction not found")
		}

		log.WithError(rsp.Error).Warnf("Error while querying for transaction '%s'", payID)
		return nil, httpError(500, "Error while querying for transactions")
	}

	return trans, nil
}

func requireAdmin(ctx context.Context, paramKey string) (*logrus.Entry, string, *HTTPError) {
	log := getLogger(ctx)
	paramValue := ""
	if paramKey != "" {
		paramValue = kami.Param(ctx, paramKey)
		log = log.WithField(paramKey, paramValue)
	}

	if !isAdmin(ctx) {
		log.Warn("Illegal access attempt")
		return nil, paramValue, httpError(401, "Can only access payments as admin")
	}

	return log, paramValue, nil
}

func (a *API) verifyAmount(ctx context.Context, order *models.Order, amount uint64) error {
	if order.Total != amount {
		return fmt.Errorf("Amount calculated for order didn't match amount to charge. %v vs %v", order.Total, amount)
	}

	return nil
}

func initEndpoint(ctx context.Context, w http.ResponseWriter, paramKey string, authRequired bool) (*logrus.Entry, *JWTClaims, string, *HTTPError) {
	log := getLogger(ctx)
	paramValue := ""
	if paramKey != "" {
		paramValue = kami.Param(ctx, paramKey)
		log = log.WithField(paramKey, paramValue)
	}

	claims := getClaims(ctx)
	if claims == nil && authRequired {
		log.Warn("Claims is missing and auth required")
		return log, nil, paramValue, unauthorizedError(w, "Listing payments requires authentication")
	}

	return log, claims, paramValue, nil
}

func queryForOrder(db *gorm.DB, orderID string, log *logrus.Entry) (*models.Order, *HTTPError) {
	order := &models.Order{}
	if rsp := db.Preload("Transactions").Find(order, "id = ?", orderID); rsp.Error != nil {
		if rsp.RecordNotFound() {
			log.Infof("Failed to find order %s", orderID)
			return nil, httpError(404, "Order not found")
		}

		log.WithError(rsp.Error).Warnf("Error while querying for order %s", orderID)
		return nil, httpError(500, "Error while querying for order")
	}
	return order, nil
}

func queryForTransactions(db *gorm.DB, log *logrus.Entry, clause, id string) ([]models.Transaction, *HTTPError) {
	trans := []models.Transaction{}
	if rsp := db.Find(&trans, clause, id); rsp.Error != nil {
		if rsp.RecordNotFound() {
			log.Infof("Failed to find transactions that meet criteria '%s' '%s'", clause, id)
			return nil, httpError(404, "Transactions not found")
		}

		log.WithError(rsp.Error).Warnf("Error while querying for transactions '%s' '%s'", clause, id)
		return nil, httpError(500, "Error while querying for transactions")
	}

	return trans, nil
}

type stripeProvider struct {
}

func (stripeProvider) charge(amount uint64, currency, token, userToken string) (string, error) {
	ch, err := charge.New(&stripe.ChargeParams{
		Amount:   amount,
		Source:   &stripe.SourceParams{Token: token},
		Currency: stripe.Currency(currency),
	})

	if err != nil {
		return "", err
	}

	return ch.ID, nil
}

func (stripeProvider) refund(amount uint64, id string) (string, error) {
	r, err := refund.New(&stripe.RefundParams{
		Charge: id,
		Amount: amount,
	})
	if err != nil {
		return "", err
	}

	return r.ID, err
}

type paypalProvider struct {
	paypal *paypalsdk.Client
}

func (p *paypalProvider) charge(amount uint64, currency, paymentID, payerID string) (string, error) {
	payment, err := p.paypal.GetPayment(paymentID)
	if err != nil {
		return "", err
	}
	if len(payment.Transactions) != 1 {
		return "", fmt.Errorf("The paypal payment must have exactly 1 transaction, had %v", len(payment.Transactions))
	}

	if payment.Transactions[0].Amount == nil {
		return "", fmt.Errorf("No amount in this transaction %v", payment.Transactions[0])
	}

	transactionValue := fmt.Sprintf("%.2f", float64(amount)/100)

	if transactionValue != payment.Transactions[0].Amount.Total || payment.Transactions[0].Amount.Currency != currency {
		return "", fmt.Errorf("The Amount in the transaction doesn't match the amount for the order: %v", payment.Transactions[0].Amount)
	}

	executeResult, err := p.paypal.ExecuteApprovedPayment(paymentID, payerID)
	if err != nil {
		return "", err
	}

	return executeResult.ID, nil
}

func (paypalProvider) refund(amount uint64, id string) (string, error) {
	return "", nil
}
