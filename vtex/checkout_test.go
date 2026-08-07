package vtex_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/voska/vtexkit/money"
	"github.com/voska/vtexkit/store"
	"github.com/voska/vtexkit/vtex"
)

// cartWithAddress is an order form carrying a saved address and one SLA.
const cartWithAddress = `{"orderFormId":"OF","items":[{},{}],"shippingData":{
	"selectedAddresses":[{"addressId":"addr-1","postalCode":"22440-030"}],
	"logisticsInfo":[{"slas":[{"id":"Entrega Agendada"}]}]}}`

func TestSetAddressUsesTheStoresOwnSLA(t *testing.T) {
	var payload struct {
		LogisticsInfo []struct {
			ItemIndex      int    `json:"itemIndex"`
			AddressID      string `json:"addressId"`
			SelectedSLA    string `json:"selectedSla"`
			DeliveryWindow any    `json:"deliveryWindow"`
		} `json:"logisticsInfo"`
		SelectedAddresses                []map[string]any `json:"selectedAddresses"`
		ClearAddressIfPostalCodeNotFound bool             `json:"clearAddressIfPostalCodeNotFound"`
	}
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/attachments/shippingData") {
			_ = json.NewDecoder(r.Body).Decode(&payload)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		_, _ = w.Write([]byte(cartWithAddress))
	})

	if err := c.SetAddress("OF", 2); err != nil {
		t.Fatal(err)
	}
	if len(payload.LogisticsInfo) != 2 {
		t.Fatalf("logisticsInfo has %d entries, want one per item", len(payload.LogisticsInfo))
	}
	// The SLA must come from the store, not a hardcoded default. The old
	// code fell back to "Entrega Zona Sul", which is invalid anywhere else.
	if payload.LogisticsInfo[0].SelectedSLA != "Entrega Agendada" {
		t.Errorf("selectedSla = %q", payload.LogisticsInfo[0].SelectedSLA)
	}
	if payload.LogisticsInfo[0].AddressID != "addr-1" {
		t.Errorf("addressId = %q", payload.LogisticsInfo[0].AddressID)
	}
	if payload.LogisticsInfo[0].DeliveryWindow != nil {
		t.Error("SetAddress must not send a delivery window")
	}
	if payload.ClearAddressIfPostalCodeNotFound {
		t.Error("clearAddressIfPostalCodeNotFound must be false")
	}
}

func TestSetAddressRefusesWhenAccountHasNoAddress(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"orderFormId":"OF","items":[{}],"shippingData":{}}`))
	})
	err := c.SetAddress("OF", 1)
	if err == nil {
		t.Fatal("must refuse rather than post an addressless shipping payload")
	}
	if !strings.Contains(err.Error(), "address") {
		t.Errorf("error should name the problem: %v", err)
	}
}

func TestSetAddressRefusesWhenStoreOffersNoSLA(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"orderFormId":"OF","items":[{}],"shippingData":{
			"selectedAddresses":[{"addressId":"a"}],"logisticsInfo":[]}}`))
	})
	// Guessing an SLA name here is what made the old code store-specific.
	if err := c.SetAddress("OF", 1); err == nil {
		t.Fatal("must refuse rather than invent an SLA name")
	}
}

func TestSetShippingWindowEchoesRawTimestamps(t *testing.T) {
	var payload struct {
		LogisticsInfo []struct {
			DeliveryWindow struct {
				StartDateUtc string         `json:"startDateUtc"`
				EndDateUtc   string         `json:"endDateUtc"`
				Price        money.Centavos `json:"price"`
			} `json:"deliveryWindow"`
		} `json:"logisticsInfo"`
	}
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/attachments/shippingData") {
			_ = json.NewDecoder(r.Body).Decode(&payload)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		_, _ = w.Write([]byte(cartWithAddress))
	})

	win := vtex.DeliveryWindow{
		RawStart: "2026-08-08T14:00:00+00:00",
		RawEnd:   "2026-08-08T16:00:59+00:00",
		Price:    700,
	}
	if err := c.SetShippingWindow("OF", win, 2); err != nil {
		t.Fatal(err)
	}
	// VTEX matches on the exact strings it issued; a reformatted RFC3339
	// timestamp is rejected.
	dw := payload.LogisticsInfo[0].DeliveryWindow
	if dw.StartDateUtc != win.RawStart || dw.EndDateUtc != win.RawEnd {
		t.Errorf("window = %+v, want the raw strings echoed", dw)
	}
	if dw.Price != 700 {
		t.Errorf("price = %d", int64(dw.Price))
	}
}

func TestResolvePaymentSystemLooksUpByName(t *testing.T) {
	c := vtex.New(store.Store{Name: "t"}, "")
	of := &vtex.OrderForm{PaymentSystems: []vtex.PaymentSystem{
		{ID: 125, Name: "Pix", GroupName: "instantPaymentPaymentGroup"},
		{ID: 2, Name: "Visa", GroupName: "creditCardPaymentGroup"},
	}}

	// Case-insensitive: the CLI flag is lowercase, VTEX capitalizes.
	id, err := c.ResolvePaymentSystem(of, "pix")
	if err != nil {
		t.Fatal(err)
	}
	if id != 125 {
		t.Errorf("pix = %d, want 125", id)
	}

	_, err = c.ResolvePaymentSystem(of, "boleto")
	if err == nil {
		t.Fatal("an unsupported method must error")
	}
	// Listing what IS available is what lets an agent recover.
	if !strings.Contains(err.Error(), "Pix") || !strings.Contains(err.Error(), "Visa") {
		t.Errorf("error must list available methods, got: %v", err)
	}
}

func TestResolvePaymentSystemOnEmptyCartIsActionable(t *testing.T) {
	c := vtex.New(store.Store{Name: "t"}, "")
	_, err := c.ResolvePaymentSystem(&vtex.OrderForm{}, "pix")
	if err == nil || !strings.Contains(err.Error(), "cart") {
		t.Errorf("err = %v; VTEX only lists payment systems once the cart has items", err)
	}
}

// gatewayClient wires a client whose payment gateway and ClearSale host both
// point at test servers.
func gatewayClient(t *testing.T, quirks store.Quirks, onPayment func(*http.Request, []map[string]any)) *vtex.Client {
	t.Helper()
	clearsale := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(clearsale.Close)

	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		onPayment(r, body)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(gw.Close)

	c := vtex.New(store.Store{
		Name: "t", Account: "testacct", BaseURL: gw.URL, Quirks: quirks,
	}, "TOKEN")
	c.GatewayURL = gw.URL
	c.ClearSaleURL = clearsale.URL
	return c
}

func fingerprintFrom(body []map[string]any) (string, bool) {
	if len(body) == 0 {
		return "", false
	}
	fields, ok := body[0]["fields"].(map[string]any)
	if !ok {
		return "", false
	}
	fp, ok := fields["deviceFingerprint"].(string)
	return fp, ok
}

func TestPayWithSavedCardSkipsClearSaleWithoutTheQuirk(t *testing.T) {
	var body []map[string]any
	c := gatewayClient(t, 0, func(_ *http.Request, b []map[string]any) { body = b })

	tx := &vtex.TransactionResult{TransactionID: "tx-1", OrderGroup: "og-1", MerchantName: "FRESCATTO-1"}
	err := c.PayWithSavedCard(tx, vtex.SavedCard{AccountID: "acct", PaymentSystem: "2"}, "123", 15372)
	if err != nil {
		t.Fatal(err)
	}
	if fp, ok := fingerprintFrom(body); ok {
		t.Errorf("deviceFingerprint = %q; a store without the quirk must not send one", fp)
	}
}

func TestPayWithSavedCardSendsClearSaleWithTheQuirk(t *testing.T) {
	var body []map[string]any
	c := gatewayClient(t, store.ClearSaleFingerprint, func(_ *http.Request, b []map[string]any) { body = b })

	tx := &vtex.TransactionResult{TransactionID: "tx-1", OrderGroup: "og-1", MerchantName: "ZONASULZSA-zonasulzsa"}
	err := c.PayWithSavedCard(tx, vtex.SavedCard{AccountID: "acct", PaymentSystem: "2"}, "123", 15372)
	if err != nil {
		t.Fatal(err)
	}
	fp, ok := fingerprintFrom(body)
	if !ok || fp == "" {
		// Zona Sul's gateway returns Cielo code 59 without this.
		t.Fatalf("deviceFingerprint missing; body = %+v", body)
	}
	if len(fp) != 36 {
		t.Errorf("fingerprint = %q, want a 36-char session id", fp)
	}
}

func TestPayWithSavedCardSplitsMerchantName(t *testing.T) {
	var body []map[string]any
	c := gatewayClient(t, 0, func(_ *http.Request, b []map[string]any) { body = b })

	tx := &vtex.TransactionResult{TransactionID: "tx-1", OrderGroup: "og-1", MerchantName: "ZONASULZSA-zonasulzsa"}
	if err := c.PayWithSavedCard(tx, vtex.SavedCard{AccountID: "a"}, "123", 100); err != nil {
		t.Fatal(err)
	}
	// The payment id keeps the full value; transaction.merchantName takes
	// only the prefix before the hyphen.
	if body[0]["id"] != "ZONASULZSA-zonasulzsa" {
		t.Errorf("id = %v", body[0]["id"])
	}
	txField, _ := body[0]["transaction"].(map[string]any)
	if txField["merchantName"] != "ZONASULZSA" {
		t.Errorf("transaction.merchantName = %v, want the prefix", txField["merchantName"])
	}
}

func TestPayWithSavedCardRequiresMerchantName(t *testing.T) {
	c := vtex.New(store.Store{Name: "t", Account: "t", BaseURL: "http://unused"}, "")
	err := c.PayWithSavedCard(&vtex.TransactionResult{TransactionID: "tx-1"},
		vtex.SavedCard{}, "123", 3990)
	if err == nil || !strings.Contains(err.Error(), "merchant name") {
		t.Errorf("err = %v, want a clear refusal rather than a guessed merchant", err)
	}
}

func TestPlaceOrderSurfacesErrorMessages(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"orderGroup":"og-1","messages":[
			{"status":"error","text":"Item unavailable"}]}`))
	})
	_, err := c.PlaceOrder("OF", 15372)
	if err == nil || !strings.Contains(err.Error(), "Item unavailable") {
		t.Errorf("err = %v, want the store's message surfaced", err)
	}
}

func TestPlaceOrderParsesTransaction(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/transaction") {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"orderGroup":"v12345","receiverUri":"https://x/y",
			"merchantTransactions":[{"id":"FRESCATTO-1","transactionId":"tx-9"}]}`))
	})
	tx, err := c.PlaceOrder("OF", 15372)
	if err != nil {
		t.Fatal(err)
	}
	if tx.OrderGroup != "v12345" || tx.TransactionID != "tx-9" || tx.MerchantName != "FRESCATTO-1" {
		t.Errorf("tx = %+v", tx)
	}
}

func TestPlaceOrderFailsWithoutAnOrderID(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})
	if _, err := c.PlaceOrder("OF", 100); err == nil {
		t.Fatal("a response with no order id must not be reported as success")
	}
}

func TestGatewayCallbackRetriesOn428(t *testing.T) {
	var attempts int
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			// The gateway answers 428 while the transaction settles.
			w.WriteHeader(http.StatusPreconditionRequired)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	if err := c.GatewayCallback("og-1"); err != nil {
		t.Fatal(err)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
}

func TestGatewayCallbackGivesUpOnPermanentFailure(t *testing.T) {
	var attempts int
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadRequest)
	})
	if err := c.GatewayCallback("og-1"); err == nil {
		t.Fatal("a 400 must not be retried into a success")
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 — 400 is permanent", attempts)
	}
}

func TestGetSavedCards(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"paymentData":{"availableAccounts":[
			{"accountId":"acct-1","cardNumber":"****9999","bin":"411111",
			 "paymentSystem":"2","paymentSystemName":"Visa"}]}}`))
	})
	cards, err := c.GetSavedCards("OF")
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 || cards[0].AccountID != "acct-1" || cards[0].PaymentSystemName != "Visa" {
		t.Errorf("cards = %+v", cards)
	}
}

func TestSetPaymentPostsSystemID(t *testing.T) {
	var payload struct {
		Payments []struct {
			PaymentSystem int            `json:"paymentSystem"`
			Value         money.Centavos `json:"value"`
		} `json:"payments"`
	}
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/attachments/paymentData") {
			t.Errorf("path = %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		_, _ = w.Write([]byte(`{}`))
	})
	if err := c.SetPayment("OF", 125, 15372); err != nil {
		t.Fatal(err)
	}
	if payload.Payments[0].PaymentSystem != 125 || payload.Payments[0].Value != 15372 {
		t.Errorf("payment = %+v", payload.Payments[0])
	}
}
