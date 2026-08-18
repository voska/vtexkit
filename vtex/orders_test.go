package vtex_test

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/voska/vtexkit/cli/errfmt"
	"github.com/voska/vtexkit/money"
)

// Neither zonasul nor frescatto had any test for order history; these are
// its first.
func TestListOrdersParsesEnvelope(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/oms/user/orders" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"list":[
			{"orderId":"1234-01","status":"ready-for-handling",
			 "statusDescription":"Pronto para manuseio",
			 "totalValue":15372,"totalItems":3}]}`))
	})
	got, err := c.ListOrders()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].OrderID != "1234-01" {
		t.Fatalf("orders = %+v", got)
	}
	if got[0].TotalValue != money.Centavos(15372) {
		t.Errorf("TotalValue = %d, want centavos", int64(got[0].TotalValue))
	}
	if got[0].TotalItems != 3 {
		t.Errorf("TotalItems = %d", got[0].TotalItems)
	}
}

func TestListOrdersEmptyHistory(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"list":[]}`))
	})
	got, err := c.ListOrders()
	if err != nil {
		t.Fatal(err)
	}
	// A new account has no orders; that is exit code 3, not an error.
	if len(got) != 0 {
		t.Errorf("got %d orders", len(got))
	}
}

func TestGetOrderParsesItems(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/oms/user/orders/1234-01" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"orderId":"1234-01","status":"invoiced","items":[
			{"id":"134","sellerSku":"134","name":"Kit Peixe","quantity":2,"price":15372}]}`))
	})
	got, err := c.GetOrder("1234-01")
	if err != nil {
		t.Fatal(err)
	}
	if got.OrderID != "1234-01" || got.Status != "invoiced" {
		t.Errorf("order = %+v", got)
	}
	if len(got.Items) != 1 || got.Items[0].Quantity != 2 || got.Items[0].SKU != "134" {
		t.Errorf("items = %+v", got.Items)
	}
}

// unsettledOrder is the shape of the Frescatto order that printed as placed
// on 2026-08-18 and was cancelled five minutes later: a payment with no tid,
// an empty connector response, and no authorizedDate anywhere.
const unsettledOrder = `{"orderId":"1234-01","status":"payment-pending",
	"statusDescription":"Pagamento pendente","value":15372,"authorizedDate":null,
	"items":[{"id":"134","sellerSku":"134","name":"Kit Peixe","quantity":1,"price":15372}],
	"paymentData":{"transactions":[{"payments":[
		{"id":"pay-1","paymentSystemName":"Visa","group":"creditCardPaymentGroup",
		 "value":15372,"tid":null,"connectorResponses":{}}]}]}}`

// settledOrder is the same order once the gateway answered.
const settledOrder = `{"orderId":"1234-01","status":"payment-approved",
	"statusDescription":"Pagamento aprovado","value":15372,
	"authorizedDate":"2026-08-18T21:04:11.000Z",
	"items":[{"id":"134","sellerSku":"134","name":"Kit Peixe","quantity":1,"price":15372}],
	"paymentData":{"transactions":[{"payments":[
		{"id":"pay-1","paymentSystemName":"Visa","group":"creditCardPaymentGroup",
		 "value":15372,"tid":"tid-fake-9","connectorResponses":{"Tid":"tid-fake-9"}}]}]}}`

func TestGetOrderReportsPaymentAuthorization(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(settledOrder))
	})
	got, err := c.GetOrder("1234-01")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Authorized {
		t.Error("a payment carrying a tid and an authorizedDate is authorized")
	}
	if got.TID != "tid-fake-9" {
		t.Errorf("TID = %q", got.TID)
	}
	if got.StatusDescription != "Pagamento aprovado" || got.Value != money.Centavos(15372) {
		t.Errorf("order = %+v", got)
	}
	if len(got.Payments) != 1 || got.Payments[0].Group != "creditCardPaymentGroup" {
		t.Errorf("payments = %+v", got.Payments)
	}
}

func TestGetOrderSeesAnUnsettledPayment(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(unsettledOrder))
	})
	got, err := c.GetOrder("1234-01")
	if err != nil {
		t.Fatal(err)
	}
	// This is the whole point: no tid, no authorizedDate, no money moved.
	if got.Authorized {
		t.Error("a payment with no tid and no authorizedDate is not authorized")
	}
	if got.TID != "" {
		t.Errorf("TID = %q, want empty", got.TID)
	}
	if got.Canceled() {
		t.Error("pending is not cancelled")
	}
}

func TestGetOrderResolvesAnOrderGroupID(t *testing.T) {
	var paths []string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		// OMS keys orders as <group>-<seq>; the bare group 404s.
		if r.URL.Path == "/api/oms/user/orders/1234-01" {
			_, _ = w.Write([]byte(settledOrder))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	// Checkout reports the order *group*, so that is the id a caller holds.
	got, err := c.GetOrder("1234")
	if err != nil {
		t.Fatalf("the id checkout printed must resolve: %v", err)
	}
	if got.OrderID != "1234-01" {
		t.Errorf("orderId = %q", got.OrderID)
	}
	if len(paths) != 2 || paths[0] != "/api/oms/user/orders/1234" {
		t.Errorf("paths = %v, want the id as given first", paths)
	}
}

func TestGetOrderTriesTheIDAsGivenOnly(t *testing.T) {
	var calls int
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(settledOrder))
	})
	if _, err := c.GetOrder("1234-02"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 — a full order id needs no fallback", calls)
	}
}

func TestGetOrderMissingEverywhereReportsNotFound(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	_, err := c.GetOrder("9999")
	var typed *errfmt.Error
	if !errors.As(err, &typed) || typed.Code != errfmt.ExitNotFound {
		t.Errorf("err = %v, want exit %d", err, errfmt.ExitNotFound)
	}
}

func TestAwaitOrderSettlementPollsUntilTheGatewayAnswers(t *testing.T) {
	var calls int
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			_, _ = w.Write([]byte(unsettledOrder))
			return
		}
		_, _ = w.Write([]byte(settledOrder))
	})
	c.SettlementInterval = time.Millisecond

	got, err := c.AwaitOrderSettlement("1234")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Authorized || got.Status != "payment-approved" {
		t.Errorf("order = %+v, want the settled read", got)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

func TestAwaitOrderSettlementStopsOnCancellation(t *testing.T) {
	var calls int
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"orderId":"1234-01","status":"canceled",
			"statusDescription":"Cancelado","paymentData":{"transactions":[]}}`))
	})
	c.SettlementInterval = time.Millisecond

	got, err := c.AwaitOrderSettlement("1234")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Canceled() || got.Authorized {
		t.Errorf("order = %+v", got)
	}
	// A cancelled order will never settle; polling it out is wasted time.
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

func TestAwaitOrderSettlementReturnsTheLastUnsettledRead(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(unsettledOrder))
	})
	c.SettlementInterval = time.Millisecond

	got, err := c.AwaitOrderSettlement("1234")
	if err != nil {
		t.Fatal(err)
	}
	// Never authorized within the budget is an answer, not an error: the
	// caller has to report the truth rather than a placed-order success.
	if got.Authorized {
		t.Error("nothing settled")
	}
	if got.Status != "payment-pending" {
		t.Errorf("status = %q", got.Status)
	}
}

func TestAwaitOrderSettlementFailsWhenTheOrderCannotBeRead(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	c.SettlementInterval = time.Millisecond

	if _, err := c.AwaitOrderSettlement("1234"); err == nil {
		t.Fatal("an unreadable order must not be reported as anything but unverified")
	}
}

func TestGetOrderResolvesAGroupThatAnswersAnEmptyBody(t *testing.T) {
	var paths []string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/api/oms/user/orders/1234-01" {
			_, _ = w.Write([]byte(settledOrder))
			return
		}
		// A group id can answer 200 with no order in it, which is not an
		// order and must not be printed as one.
		_, _ = w.Write([]byte(`{}`))
	})
	got, err := c.GetOrder("1234")
	if err != nil {
		t.Fatal(err)
	}
	if got.OrderID != "1234-01" || len(paths) != 2 {
		t.Errorf("order = %+v, paths = %v", got, paths)
	}
}
