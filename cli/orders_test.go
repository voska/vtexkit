package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/voska/vtexkit/store"
)

// orderServer answers the endpoints an authenticated read touches, plus one
// order. The order is the settled shape: status, an authorizedDate, and a
// payment carrying the acquirer's transaction id.
func orderServer(t *testing.T, order string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/authenticated/user"):
			_, _ = w.Write([]byte(`{"user":"a@b.c"}`))
		case strings.Contains(r.URL.Path, "/api/sessions"):
			_, _ = w.Write([]byte(`{"namespaces":{"cookie":{},"checkout":{}}}`))
		default:
			_, _ = w.Write([]byte(order))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

const approvedOrder = `{"orderId":"1234-01","status":"payment-approved",
	"statusDescription":"Pagamento aprovado","value":15372,
	"authorizedDate":"2026-08-18T21:04:11.000Z",
	"items":[{"id":"134","sellerSku":"134","name":"Kit Peixe","quantity":2,"price":15372}],
	"paymentData":{"transactions":[{"payments":[
		{"id":"pay-1","paymentSystemName":"Visa","group":"creditCardPaymentGroup",
		 "value":15372,"tid":"tid-fake-9"}]}]}}`

func TestOrderDetailJSONCarriesStatusAndAuthorization(t *testing.T) {
	srv := orderServer(t, approvedOrder)
	g := testGlobals(t, store.Store{Name: "fr", Account: "fr", BaseURL: srv.URL}, &CLI{JSON: true})

	out := captureStdout(t, func() {
		if err := (&OrdersCmd{OrderID: "1234-01"}).Run(g); err != nil {
			t.Fatal(err)
		}
	})

	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("orders <id> --json must be parseable: %v (%s)", err, out)
	}
	// The command parsed status all along and printed only line items, so
	// nobody could answer "did this order survive" from a stock binary.
	if got["status"] != "payment-approved" {
		t.Errorf("status = %v", got["status"])
	}
	if got["authorized"] != true {
		t.Errorf("authorized = %v", got["authorized"])
	}
	if got["tid"] != "tid-fake-9" {
		t.Errorf("tid = %v", got["tid"])
	}
	items, ok := got["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("items = %v; the line items must survive the addition", got["items"])
	}
}

func TestOrderDetailResultsOnlyStillYieldsTheItems(t *testing.T) {
	srv := orderServer(t, approvedOrder)
	g := testGlobals(t, store.Store{Name: "fr", Account: "fr", BaseURL: srv.URL},
		&CLI{JSON: true, ResultsOnly: true})

	out := captureStdout(t, func() {
		if err := (&OrdersCmd{OrderID: "1234-01"}).Run(g); err != nil {
			t.Fatal(err)
		}
	})

	// --results-only strips the envelope, which is how a caller that wants
	// the old items-only array keeps getting exactly that.
	var items []map[string]any
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("--results-only must yield the items array: %v (%s)", err, out)
	}
	if len(items) != 1 || items[0]["sellerSku"] != "134" {
		t.Errorf("items = %+v", items)
	}
}

func TestOrderDetailHumanOutputLeadsWithStatus(t *testing.T) {
	srv := orderServer(t, approvedOrder)
	g := testGlobals(t, store.Store{Name: "fr", Account: "fr", BaseURL: srv.URL}, &CLI{})

	out := captureStdout(t, func() {
		if err := (&OrdersCmd{OrderID: "1234-01"}).Run(g); err != nil {
			t.Fatal(err)
		}
	})

	for _, want := range []string{"payment-approved", "tid-fake-9", "Kit Peixe", "R$153,72"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestOrderDetailNamesAnUnsettledPayment(t *testing.T) {
	srv := orderServer(t, `{"orderId":"1234-01","status":"payment-pending",
		"statusDescription":"Pagamento pendente","value":15372,"authorizedDate":null,
		"items":[],"paymentData":{"transactions":[{"payments":[
			{"id":"pay-1","tid":null,"connectorResponses":{}}]}]}}`)
	g := testGlobals(t, store.Store{Name: "fr", Account: "fr", BaseURL: srv.URL}, &CLI{})

	out := captureStdout(t, func() {
		if err := (&OrdersCmd{OrderID: "1234-01"}).Run(g); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "not authorized") {
		t.Errorf("an unsettled payment must be named as such:\n%s", out)
	}
}
