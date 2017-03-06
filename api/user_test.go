package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/guregu/kami"
	"github.com/stretchr/testify/assert"

	"github.com/netlify/gocommerce/models"
)

func TestUsersQueryForAllUsersAsStranger(t *testing.T) {
	db, config := db(t)
	ctx := testContext(testToken("magical-unicorn", ""), config, false)

	recorder := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", urlWithUserID, nil)

	NewAPI(config, db, nil).UserList(ctx, recorder, req)
	validateError(t, 401, recorder)
}

func TestUsersQueryForAllUsersWithParams(t *testing.T) {
	db, config := db(t)
	toDie := models.User{
		ID:    "villian",
		Email: "twoface@dc.com",
	}
	rsp := db.Create(&toDie)
	if rsp.Error != nil {
		assert.FailNow(t, "failed b/c of db error: "+rsp.Error.Error())
	}
	defer db.Unscoped().Delete(&toDie)

	ctx := testContext(testToken("magical-unicorn", ""), config, true)

	req, _ := http.NewRequest("GET", "http://junk?email=twoface@dc.com", nil)
	recorder := httptest.NewRecorder()

	NewAPI(config, db, nil).UserList(ctx, recorder, req)

	users := []models.User{}
	extractPayload(t, 200, recorder, &users)
	assert.Equal(t, 1, len(users))
	assert.Equal(t, "villian", users[0].ID)
}

func TestUsersQueryForAllUsers(t *testing.T) {
	db, config := db(t)
	toDie := models.User{
		ID:    "villian",
		Email: "twoface@dc.com",
	}
	db.Create(&toDie)
	defer db.Unscoped().Delete(&toDie)

	recorder := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", urlWithUserID, nil)
	ctx := testContext(testToken("magical-unicorn", ""), config, true)

	NewAPI(config, db, nil).UserList(ctx, recorder, req)

	users := []models.User{}
	extractPayload(t, 200, recorder, &users)
	for _, u := range users {
		switch u.ID {
		case toDie.ID:
			assert.Equal(t, "twoface@dc.com", u.Email)
		case testUser.ID:
			assert.Equal(t, testUser.Email, u.Email)
		default:
			assert.Fail(t, "unexpected user %v\n", u)
		}
	}
}

//func TestUsersQueryForDeletedUser(t *testing.T) {
//	toDie := models.User{
//		ID:    "def-should-not-exist",
//		Email: "twoface@dc.com",
//	}
//	db.Create(&toDie)
//	db.Delete(&toDie) // soft delete
//	defer db.Unscoped().Delete(&toDie)
//
//	recorder := httptest.NewRecorder()
//	req, _ := http.NewRequest("GET", urlWithUserID, nil)
//
//	config := testConfig()
//	ctx := testContext(testToken(toDie.ID, toDie.Email, nil), config)
//	ctx = kami.SetParam(ctx, "user_id", toDie.ID)
//
//	api := NewAPI(config, db, nil)
//	api.UserView(ctx, recorder, req)
//	validateError(t, 404, recorder)
//}

func TestUsersQueryForUserAsUser(t *testing.T) {
	db, config := db(t)
	recorder := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", urlWithUserID, nil)

	ctx := testContext(testToken(testUser.ID, testUser.Email), config, false)
	ctx = kami.SetParam(ctx, "user_id", testUser.ID)

	api := NewAPI(config, db, nil)
	api.UserView(ctx, recorder, req)
	user := new(models.User)
	extractPayload(t, 200, recorder, user)

	validateUser(t, &testUser, user)
}

func TestUsersQueryForUserAsStranger(t *testing.T) {
	db, config := db(t)
	recorder := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", urlWithUserID, nil)

	ctx := testContext(testToken("magical-unicorn", ""), config, false)
	ctx = kami.SetParam(ctx, "user_id", testUser.ID)

	api := NewAPI(config, db, nil)
	api.UserView(ctx, recorder, req)
	validateError(t, 401, recorder)
}

func TestUsersQueryForUserAsAdmin(t *testing.T) {
	db, config := db(t)
	recorder := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", urlWithUserID, nil)

	ctx := testContext(testToken("magical-unicorn", ""), config, true)
	ctx = kami.SetParam(ctx, "user_id", testUser.ID)

	NewAPI(config, db, nil).UserView(ctx, recorder, req)

	user := new(models.User)
	extractPayload(t, 200, recorder, user)
	validateUser(t, &testUser, user)
}

func TestUsersQueryForAllAddressesAsAdmin(t *testing.T) {
	db, config := db(t)
	second := getTestAddress()
	second.UserID = testUser.ID
	assert.Nil(t, second.Validate())
	db.Create(&second)
	defer db.Unscoped().Delete(&second)

	ctx := testContext(testToken("magical-unicorn", ""), config, true)

	addrs := queryForAddresses(t, ctx, NewAPI(config, db, nil), testUser.ID)
	assert.Equal(t, 2, len(addrs))
	for _, a := range addrs {
		assert.Nil(t, a.Validate())
		switch a.ID {
		case second.ID:
			validateAddress(t, *second, a)
		case testAddress.ID:
			validateAddress(t, testAddress, a)
		default:
			assert.Fail(t, fmt.Sprintf("Unexpected address: %+v", a))
		}
	}
}

func TestUsersQueryForAllAddressesAsUser(t *testing.T) {
	db, config := db(t)
	ctx := testContext(testToken(testUser.ID, ""), config, false)
	addrs := queryForAddresses(t, ctx, NewAPI(config, db, nil), testUser.ID)
	assert.Equal(t, 1, len(addrs))
	validateAddress(t, testAddress, addrs[0])
}

func TestUsersQueryForAllAddressesAsStranger(t *testing.T) {
	db, config := db(t)
	ctx := testContext(testToken("stranger-danger", ""), config, false)
	ctx = kami.SetParam(ctx, "user_id", testUser.ID)
	recorder := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", urlWithUserID, nil)

	NewAPI(config, db, nil).AddressList(ctx, recorder, req)
	validateError(t, 401, recorder)
}

func TestUsersQueryForAllAddressesNoAddresses(t *testing.T) {
	db, config := db(t)
	u := models.User{
		ID:    "temporary",
		Email: "junk@junk.com",
	}
	db.Create(u)
	defer db.Unscoped().Delete(u)

	ctx := testContext(testToken(u.ID, ""), config, false)
	addrs := queryForAddresses(t, ctx, NewAPI(config, db, nil), u.ID)
	assert.Equal(t, 0, len(addrs))
}

func TestUsersQueryForAllAddressesMissingUser(t *testing.T) {
	db, config := db(t)
	ctx := testContext(testToken("dne", ""), config, false)
	ctx = kami.SetParam(ctx, "user_id", "dne")
	recorder := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", urlWithUserID, nil)

	NewAPI(config, db, nil).AddressList(ctx, recorder, req)
	validateError(t, 404, recorder)
}

func TestUsersQueryForSingleAddressAsUser(t *testing.T) {
	db, config := db(t)
	ctx := testContext(testToken(testUser.ID, ""), config, false)

	ctx = kami.SetParam(ctx, "user_id", testUser.ID)
	recorder := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", urlWithUserID, nil)

	NewAPI(config, db, nil).AddressView(ctx, recorder, req)

	addr := new(models.Address)
	extractPayload(t, 200, recorder, addr)
	validateAddress(t, testAddress, *addr)
}

func TestUsersDeleteNonExistentUser(t *testing.T) {
	db, config := db(t)
	ctx := testContext(testToken("magical-unicorn", ""), config, true)
	ctx = kami.SetParam(ctx, "user_id", "dne")

	recorder := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", urlWithUserID, nil)

	NewAPI(config, db, nil).UserDelete(ctx, recorder, req)
	assert.Equal(t, 200, recorder.Code)
	assert.Equal(t, "", recorder.Body.String())
}

func TestUsersDeleteSingleUser(t *testing.T) {
	db, config := db(t)
	dyingUser := models.User{ID: "going-to-die", Email: "nobody@nowhere.com"}
	dyingAddr := getTestAddress()
	dyingAddr.UserID = dyingUser.ID
	dyingOrder := models.NewOrder("session2", dyingUser.Email, "usd")
	dyingOrder.UserID = dyingUser.ID
	dyingTransaction := models.NewTransaction(dyingOrder)
	dyingTransaction.UserID = dyingUser.ID
	dyingLineItem := models.LineItem{
		ID:          123,
		OrderID:     dyingOrder.ID,
		Title:       "coffin",
		SKU:         "123-cough-cough-123",
		Type:        "home",
		Description: "nappytimeplace",
		Price:       100,
		Quantity:    1,
		Path:        "/right/to/the/grave",
	}
	items := []interface{}{&dyingUser, &dyingAddr, dyingOrder, &dyingLineItem, &dyingTransaction}
	for _, i := range items {
		db.Create(i)
	}
	defer func() {
		for _, i := range items {
			db.Unscoped().Delete(i)
		}
	}()

	ctx := testContext(testToken("magical-unicorn", ""), config, true)
	ctx = kami.SetParam(ctx, "user_id", dyingUser.ID)

	recorder := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", urlWithUserID, nil)

	NewAPI(config, db, nil).UserDelete(ctx, recorder, req)
	assert.Equal(t, 200, recorder.Code)
	assert.Equal(t, "", recorder.Body.String())

	// now load it back and it should be soft deleted
	//found := &models.User{ID: dyingUser.ID}
	assert.False(t, db.Unscoped().First(&dyingUser).RecordNotFound())
	assert.NotNil(t, dyingUser.DeletedAt, "user wasn't deleted")
	assert.False(t, db.Unscoped().First(&dyingAddr).RecordNotFound())
	assert.NotNil(t, dyingAddr.DeletedAt, "addr wasn't deleted")
	assert.False(t, db.Unscoped().First(dyingOrder).RecordNotFound())
	assert.NotNil(t, dyingOrder.DeletedAt, "order wasn't deleted")
	assert.False(t, db.Unscoped().First(&dyingTransaction).RecordNotFound())
	assert.NotNil(t, dyingTransaction.DeletedAt, "transaction wasn't deleted")
	assert.False(t, db.Unscoped().First(&dyingLineItem).RecordNotFound())
	assert.NotNil(t, dyingLineItem.DeletedAt, "line item wasn't deleted")
}

func TestDeleteUserAddress(t *testing.T) {
	db, config := db(t)
	addr := getTestAddress()
	addr.UserID = testUser.ID
	db.Create(addr)

	ctx := testContext(testToken("magical-unicorn", ""), config, true)
	ctx = kami.SetParam(ctx, "user_id", testUser.ID)
	ctx = kami.SetParam(ctx, "addr_id", addr.ID)

	recorder := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", urlWithUserID, nil)

	NewAPI(config, db, nil).AddressDelete(ctx, recorder, req)
	assert.Equal(t, 200, recorder.Code)
	assert.Equal(t, "", recorder.Body.String())

	assert.False(t, db.Unscoped().First(&addr).RecordNotFound())
	assert.NotNil(t, addr.DeletedAt)
}

func TestCreateAnAddress(t *testing.T) {
	db, config := db(t)
	addr := getTestAddress()
	b, err := json.Marshal(&addr.AddressRequest)
	assert.Nil(t, err)

	ctx := testContext(testToken("magical-unicorn", ""), config, true)
	ctx = kami.SetParam(ctx, "user_id", testUser.ID)

	recorder := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", urlWithUserID, bytes.NewBuffer(b))

	NewAPI(config, db, nil).CreateNewAddress(ctx, recorder, req)

	assert.Equal(t, 200, recorder.Code)

	results := struct {
		ID string
	}{}
	err = json.Unmarshal(recorder.Body.Bytes(), &results)
	assert.Nil(t, err)

	// now pull off the address from the DB
	dbAddr := &models.Address{ID: results.ID, UserID: testUser.ID}
	rsp := db.First(dbAddr)
	assert.False(t, rsp.RecordNotFound())
}

func TestCreateInvalidAddress(t *testing.T) {
	db, config := db(t)
	addr := getTestAddress()
	addr.LastName = "" // required field

	b, err := json.Marshal(&addr.AddressRequest)
	assert.Nil(t, err)

	ctx := testContext(testToken("magical-unicorn", ""), config, true)
	ctx = kami.SetParam(ctx, "user_id", testUser.ID)

	recorder := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", urlWithUserID, bytes.NewBuffer(b))

	NewAPI(config, db, nil).CreateNewAddress(ctx, recorder, req)

	validateError(t, 400, recorder)
}

// ------------------------------------------------------------------------------------------------

func queryForAddresses(t *testing.T, ctx context.Context, api *API, id string) []models.Address {
	ctx = kami.SetParam(ctx, "user_id", id)
	recorder := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", urlWithUserID, nil)

	api.AddressList(ctx, recorder, req)
	addrs := []models.Address{}
	extractPayload(t, 200, recorder, &addrs)
	return addrs
}
