package cli

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"time"

	"github.com/voska/vtexkit/cli/errfmt"
	"github.com/voska/vtexkit/money"
	"github.com/voska/vtexkit/store"
	"github.com/voska/vtexkit/vtex"
	"github.com/zalando/go-keyring"
)

// These tests exist only because Globals carries the store descriptor.
// Pre-extraction, zonasul's command layer constructed clients from package
// constants and could not be pointed at a test server at all.

// cartServer answers the endpoints a checkout run touches.
func cartServer(t *testing.T, items string, paymentSystems string, hits map[string]int) *httptest.Server {
	t.Helper()
	orderForm := `{"orderFormId":"OF","items":` + items + `,
		"totalizers":[{"id":"Items","name":"Total dos Itens","value":15372}],
		"shippingData":{"selectedAddresses":[{"addressId":"a1"}],
			"logisticsInfo":[{"slas":[{"id":"Entrega Agendada"}]}]},
		"paymentData":{"paymentSystems":` + paymentSystems + `,"availableAccounts":[]}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		hits[p]++
		switch {
		case strings.HasSuffix(p, "/authenticated/user"):
			_, _ = w.Write([]byte(`{"user":"a@b.c"}`))
		case strings.Contains(p, "/api/sessions"):
			_, _ = w.Write([]byte(`{"namespaces":{"cookie":{},"checkout":{"orderFormId":{"value":"OF"}}}}`))
		case strings.HasSuffix(p, "/attachments/shippingData"),
			strings.HasSuffix(p, "/attachments/paymentData"):
			_, _ = w.Write([]byte(`{}`))
		case strings.HasSuffix(p, "/transaction"):
			t.Error("the transaction endpoint must never be reached without --confirm")
			_, _ = w.Write([]byte(`{"orderGroup":"SHOULD-NOT-HAPPEN"}`))
		default:
			_, _ = w.Write([]byte(orderForm))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testGlobals(t *testing.T, s store.Store, cli *CLI) *Globals {
	t.Helper()
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())
	if err := keyring.Set(s.KeyringService(), keyringTokenKey, "TOKEN"); err != nil {
		t.Fatal(err)
	}
	if cli == nil {
		cli = &CLI{}
	}
	return &Globals{CLI: cli, Store: s, Version: "test"}
}

const pixOnly = `[{"id":125,"name":"Pix","groupName":"instantPaymentPaymentGroup"}]`
const oneItem = `[{"id":"134","name":"Kit Peixe","quantity":1,"sellingPrice":15372,"seller":"1"}]`

func TestCheckoutWithoutConfirmNeverPlacesOrder(t *testing.T) {
	hits := map[string]int{}
	srv := cartServer(t, oneItem, pixOnly, hits)
	g := testGlobals(t, store.Store{Name: "fr", Account: "fr", BaseURL: srv.URL}, &CLI{JSON: true})

	// The cartServer handler fails the test if /transaction is hit.
	if err := (&CheckoutRunCmd{Payment: "pix", Window: -1}).Run(g); err != nil {
		t.Fatal(err)
	}
}

func TestCheckoutPreviewReportsNotPlaced(t *testing.T) {
	hits := map[string]int{}
	srv := cartServer(t, oneItem, pixOnly, hits)
	g := testGlobals(t, store.Store{Name: "fr", Account: "fr", BaseURL: srv.URL}, &CLI{JSON: true})

	out := captureStdout(t, func() {
		if err := (&CheckoutRunCmd{Payment: "pix", Window: -1}).Run(g); err != nil {
			t.Fatal(err)
		}
	})

	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("preview must be valid JSON: %v (%s)", err, out)
	}
	if got["placed"] != false {
		t.Errorf("placed = %v, want false", got["placed"])
	}
	if got["total"] != float64(15372) {
		t.Errorf("total = %v, want 15372", got["total"])
	}
	if !strings.Contains(out, "--confirm") {
		t.Error("preview must say how to actually place the order")
	}
}

func TestCheckoutBelowMinimumReturnsExit9(t *testing.T) {
	hits := map[string]int{}
	srv := cartServer(t, oneItem, pixOnly, hits)
	// Zona Sul's R$100 floor against a R$153.72 cart passes, so use a
	// higher minimum to exercise the refusal.
	g := testGlobals(t, store.Store{
		Name: "zs", Account: "zs", BaseURL: srv.URL, MinOrder: money.Reais(500),
	}, &CLI{JSON: true})

	err := (&CheckoutRunCmd{Payment: "pix", Window: -1}).Run(g)
	var typed *errfmt.Error
	if !errors.As(err, &typed) {
		t.Fatalf("err = %v, want a typed error", err)
	}
	if typed.Code != errfmt.ExitDomain {
		t.Errorf("code = %d, want %d (domain_error)", typed.Code, errfmt.ExitDomain)
	}
	// The message must carry both numbers so a caller knows the shortfall.
	if !strings.Contains(err.Error(), "R$500,00") || !strings.Contains(err.Error(), "R$153,72") {
		t.Errorf("message must name both amounts, got: %v", err)
	}
}

func TestCheckoutWithNoMinimumAcceptsAnyTotal(t *testing.T) {
	hits := map[string]int{}
	srv := cartServer(t, oneItem, pixOnly, hits)
	// Frescatto declares no minimum; a small cart must not be refused.
	g := testGlobals(t, store.Store{Name: "fr", Account: "fr", BaseURL: srv.URL}, &CLI{JSON: true})

	if err := (&CheckoutRunCmd{Payment: "pix", Window: -1}).Run(g); err != nil {
		t.Fatalf("a store with no minimum must accept any total: %v", err)
	}
}

func TestCheckoutEmptyCartReturnsExit3(t *testing.T) {
	hits := map[string]int{}
	srv := cartServer(t, `[]`, pixOnly, hits)
	g := testGlobals(t, store.Store{Name: "fr", Account: "fr", BaseURL: srv.URL}, &CLI{JSON: true})

	err := (&CheckoutRunCmd{Payment: "pix", Window: -1}).Run(g)
	var typed *errfmt.Error
	if !errors.As(err, &typed) || typed.Code != errfmt.ExitEmpty {
		t.Errorf("err = %v, want exit 3", err)
	}
}

func TestCheckoutUnsupportedPaymentListsAlternatives(t *testing.T) {
	hits := map[string]int{}
	srv := cartServer(t, oneItem, pixOnly, hits)
	g := testGlobals(t, store.Store{Name: "fr", Account: "fr", BaseURL: srv.URL}, &CLI{JSON: true})

	err := (&CheckoutRunCmd{Payment: "boleto", Window: -1}).Run(g)
	if err == nil {
		t.Fatal("an unsupported method must fail")
	}
	// Payment IDs are per-store, so the error has to say what IS accepted.
	if !strings.Contains(err.Error(), "Pix") {
		t.Errorf("error must list available methods, got: %v", err)
	}
}

func TestCheckoutWindowOutOfRangeIsActionable(t *testing.T) {
	hits := map[string]int{}
	srv := cartServer(t, oneItem, pixOnly, hits)
	g := testGlobals(t, store.Store{Name: "fr", Account: "fr", BaseURL: srv.URL}, &CLI{JSON: true})

	err := (&CheckoutRunCmd{Payment: "pix", Window: 99}).Run(g)
	if err == nil {
		t.Fatal("an out-of-range window must fail")
	}
	if !strings.Contains(err.Error(), "fr delivery windows") {
		t.Errorf("error must name the recovery command, got: %v", err)
	}
}

func TestExitCodesCommandMatchesTable(t *testing.T) {
	g := testGlobals(t, store.Store{Name: "fr"}, &CLI{JSON: true})
	out := captureStdout(t, func() {
		if err := (&ExitCodesCmd{}).Run(g); err != nil {
			t.Fatal(err)
		}
	})
	var got []errfmt.ExitCodeEntry
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("exit-codes --json must be parseable: %v (%s)", err, out)
	}
	want := errfmt.ExitCodeTable()
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// confirmServer answers a full checkout including the transaction endpoint,
// then serves the order that transaction produced.
func confirmServer(t *testing.T, order string) *httptest.Server {
	t.Helper()
	orderForm := `{"orderFormId":"OF","items":` + oneItem + `,
		"totalizers":[{"id":"Items","name":"Total dos Itens","value":15372}],
		"shippingData":{"selectedAddresses":[{"addressId":"a1"}],
			"logisticsInfo":[{"slas":[{"id":"Entrega Agendada"}]}]},
		"paymentData":{"paymentSystems":` + pixOnly + `,"availableAccounts":[]}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/authenticated/user"):
			_, _ = w.Write([]byte(`{"user":"a@b.c"}`))
		case strings.Contains(p, "/api/sessions"):
			_, _ = w.Write([]byte(`{"namespaces":{"cookie":{},"checkout":{"orderFormId":{"value":"OF"}}}}`))
		case strings.HasSuffix(p, "/attachments/shippingData"),
			strings.HasSuffix(p, "/attachments/paymentData"):
			_, _ = w.Write([]byte(`{}`))
		case strings.HasSuffix(p, "/transaction"):
			_, _ = w.Write([]byte(`{"orderGroup":"1234","merchantTransactions":[
				{"id":"FR-1","transactionId":"tx-9"}]}`))
		case strings.Contains(p, "/api/oms/user/orders/"):
			_, _ = w.Write([]byte(order))
		default:
			_, _ = w.Write([]byte(orderForm))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// pendingPixOrder is a placed order whose payment the store has not
// collected yet — the honest state of a pix checkout the moment it returns.
const pendingPixOrder = `{"orderId":"1234-01","status":"payment-pending",
	"statusDescription":"Pagamento pendente","value":15372,"authorizedDate":null,
	"items":[],"paymentData":{"transactions":[{"payments":[
		{"id":"pay-1","group":"instantPaymentPaymentGroup","tid":null,
		 "connectorResponses":{}}]}]}}`

// cancelledCardOrder is the 2026-08-18 Frescatto shape: a card payment the
// gateway never settled, on an order the CLI printed as placed.
const cancelledCardOrder = `{"orderId":"1234-01","status":"payment-pending",
	"statusDescription":"Pagamento pendente","value":15372,"authorizedDate":null,
	"items":[],"paymentData":{"transactions":[{"payments":[
		{"id":"pay-1","group":"creditCardPaymentGroup","tid":null,
		 "connectorResponses":{}}]}]}}`

const settledCardOrder = `{"orderId":"1234-01","status":"payment-approved",
	"statusDescription":"Pagamento aprovado","value":15372,
	"authorizedDate":"2026-08-18T21:04:11.000Z","items":[],
	"paymentData":{"transactions":[{"payments":[
		{"id":"pay-1","group":"creditCardPaymentGroup","tid":"tid-fake-9"}]}]}}`

// reportClient points a client at a test server and shortens the settlement
// poll so a test never waits on a real gateway's clock.
func reportClient(t *testing.T, order string, status int) *vtex.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		_, _ = w.Write([]byte(order))
	}))
	t.Cleanup(srv.Close)
	c := vtex.New(store.Store{Name: "fr", Account: "fr", BaseURL: srv.URL}, "TOKEN")
	c.SettlementInterval = time.Millisecond
	return c
}

func TestCheckoutConfirmedOrderReportsTheStoresOwnStatus(t *testing.T) {
	srv := confirmServer(t, pendingPixOrder)
	g := testGlobals(t, store.Store{Name: "fr", Account: "fr", BaseURL: srv.URL}, &CLI{JSON: true})

	out := captureStdout(t, func() {
		if err := (&CheckoutRunCmd{Payment: "pix", Window: -1, Confirm: true}).Run(g); err != nil {
			t.Fatal(err)
		}
	})

	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("confirmed checkout must be valid JSON: %v (%s)", err, out)
	}
	// "placed" was printed unconditionally, straight after the request:
	// the status now comes from re-reading the order the store created.
	if got["status"] != "payment-pending" {
		t.Errorf("status = %v, want the store's own status", got["status"])
	}
	if got["orderId"] != "1234" || got["placed"] != true {
		t.Errorf("record = %v", got)
	}
	if got["authorized"] != false {
		t.Errorf("authorized = %v; nothing has settled on a pending pix order", got["authorized"])
	}
}

func TestCheckoutCardPaymentThatNeverSettledFailsLoudly(t *testing.T) {
	g := testGlobals(t, store.Store{Name: "fr", Account: "fr"}, &CLI{JSON: true})
	client := reportClient(t, cancelledCardOrder, http.StatusOK)
	tx := &vtex.TransactionResult{OrderGroup: "1234"}

	var err error
	out := captureStdout(t, func() {
		err = (&CheckoutRunCmd{}).report(g, client, tx, true, money.Centavos(15372))
	})

	var typed *errfmt.Error
	if !errors.As(err, &typed) {
		t.Fatalf("err = %v, want a typed error — this is the order that lost the salmon", err)
	}
	if typed.Code != errfmt.ExitDomain {
		t.Errorf("code = %d, want %d", typed.Code, errfmt.ExitDomain)
	}
	// A caller has to be able to find the order without re-running
	// checkout, which would place a second one.
	if !strings.Contains(err.Error(), "1234") || !strings.Contains(err.Error(), "fr orders 1234") {
		t.Errorf("error must name the order and how to inspect it: %v", err)
	}
	var got map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &got); jsonErr != nil {
		t.Fatalf("the record must still reach stdout: %v (%s)", jsonErr, out)
	}
	if got["authorized"] != false || got["status"] != "payment-pending" {
		t.Errorf("record = %v", got)
	}
}

func TestCheckoutCardPaymentApprovedReportsTheAuthorization(t *testing.T) {
	g := testGlobals(t, store.Store{Name: "fr", Account: "fr"}, &CLI{JSON: true})
	client := reportClient(t, settledCardOrder, http.StatusOK)
	tx := &vtex.TransactionResult{OrderGroup: "1234"}

	var err error
	out := captureStdout(t, func() {
		err = (&CheckoutRunCmd{}).report(g, client, tx, true, money.Centavos(15372))
	})
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &got); jsonErr != nil {
		t.Fatalf("bad JSON: %v (%s)", jsonErr, out)
	}
	if got["authorized"] != true || got["status"] != "payment-approved" || got["tid"] != "tid-fake-9" {
		t.Errorf("record = %v", got)
	}
}

func TestCheckoutUnreadableOrderIsNeverReportedAsPaid(t *testing.T) {
	g := testGlobals(t, store.Store{Name: "fr", Account: "fr"}, &CLI{JSON: true})
	client := reportClient(t, "", http.StatusInternalServerError)
	tx := &vtex.TransactionResult{OrderGroup: "1234"}

	var err error
	out := captureStdout(t, func() {
		err = (&CheckoutRunCmd{}).report(g, client, tx, true, money.Centavos(15372))
	})

	var typed *errfmt.Error
	if !errors.As(err, &typed) || typed.Code != errfmt.ExitDomain {
		t.Fatalf("err = %v, want exit %d — an unverifiable card payment is not a success", err, errfmt.ExitDomain)
	}
	if !strings.Contains(out, "unverified") {
		t.Errorf("the record must say the payment is unverified:\n%s", out)
	}
}

func TestCheckoutUnreadablePixOrderStillReportsThePlacedOrder(t *testing.T) {
	g := testGlobals(t, store.Store{Name: "fr", Account: "fr"}, &CLI{JSON: true})
	client := reportClient(t, "", http.StatusInternalServerError)
	tx := &vtex.TransactionResult{OrderGroup: "1234"}

	var err error
	out := captureStdout(t, func() {
		err = (&CheckoutRunCmd{}).report(g, client, tx, false, money.Centavos(15372))
	})
	// Nothing was charged, and the store issued the order id itself, so
	// the order is real — only its status is unknown.
	if err != nil {
		t.Fatalf("err = %v; a pix order the store acknowledged is placed", err)
	}
	if !strings.Contains(out, "unverified") || !strings.Contains(out, "1234") {
		t.Errorf("record = %s", out)
	}
}
