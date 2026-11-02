package cli

import (
	"encoding/json"
	"errors"
	"fmt"
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
	cleared := false
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
		case strings.HasSuffix(p, "/items/removeAll"):
			cleared = true
			_, _ = w.Write([]byte(`{}`))
		default:
			body := orderForm
			if cleared {
				body = strings.Replace(body, oneItem, `[]`, 1)
			}
			_, _ = w.Write([]byte(body))
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

func TestCheckoutResetsOnlyAnAcceptedOrdersCart(t *testing.T) {
	for _, tc := range []struct {
		name       string
		confirm    bool
		dryRun     bool
		card       bool
		placeFails bool
		resetFails bool
		stillFull  bool
		readFails  bool
		wantReset  bool
		wantErr    bool
	}{
		{name: "placed", confirm: true, wantReset: true},
		{name: "preview"},
		{name: "dry run", confirm: true, dryRun: true},
		{name: "placement rejected", confirm: true, placeFails: true, wantErr: true},
		{name: "payment submission fails", confirm: true, card: true, wantReset: true, wantErr: true},
		{name: "payment and reset fail", confirm: true, card: true, resetFails: true, wantReset: true, wantErr: true},
		{name: "reset rejected", confirm: true, resetFails: true, wantReset: true, wantErr: true},
		{name: "reset did not empty cart", confirm: true, stillFull: true, wantReset: true, wantErr: true},
		{name: "reset readback fails", confirm: true, readFails: true, wantReset: true, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items, resets := oneItem, 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/vtexid/pub/authenticated/user":
					_, _ = w.Write([]byte(`{"user":"test@example.invalid"}`))
				case "/api/sessions":
					_, _ = w.Write([]byte(`{"namespaces":{}}`))
				case "/api/checkout/pub/orderForm/OF/attachments/shippingData",
					"/api/checkout/pub/orderForm/OF/attachments/paymentData":
					_, _ = w.Write([]byte(`{}`))
				case "/api/checkout/pub/orderForm/OF/transaction":
					if tc.placeFails {
						http.Error(w, "placement rejected", http.StatusBadRequest)
						return
					}
					// No merchant: saved-card submission fails before any gateway call.
					_, _ = w.Write([]byte(`{"orderGroup":"1234"}`))
				case "/api/oms/user/orders/1234":
					_, _ = w.Write([]byte(pendingPixOrder))
				case "/api/checkout/pub/orderForm/OF/items/removeAll":
					resets++
					if tc.resetFails {
						http.Error(w, "reset rejected", http.StatusServiceUnavailable)
						return
					}
					if !tc.stillFull {
						items = `[]`
					}
					_, _ = w.Write([]byte(`{}`))
				case "/api/checkout/pub/orderForm/OF/items":
					var cartItems []vtex.OrderFormItem
					if err := json.Unmarshal([]byte(items), &cartItems); err != nil {
						t.Error(err)
					}
					cartItems = append(cartItems, vtex.OrderFormItem{ID: "NEXT", Quantity: 1, Seller: "1"})
					body, err := json.Marshal(cartItems)
					if err != nil {
						t.Error(err)
					}
					items = string(body)
					fallthrough
				case "/api/checkout/pub/orderForm/OF":
					if tc.readFails && resets > 0 {
						http.Error(w, "read failed", http.StatusServiceUnavailable)
						return
					}
					_, _ = fmt.Fprintf(w, `{"orderFormId":"OF","items":%s,
						"totalizers":[{"id":"Items","value":15372}],
						"shippingData":{"selectedAddresses":[{"addressId":"a1"}],
						"logisticsInfo":[{"slas":[{"id":"Delivery"}]}]},
						"paymentData":{"paymentSystems":%s,"availableAccounts":[{"paymentSystem":"2"}]}}`, items, pixOnly)
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			g := testGlobals(t, store.Store{Name: "fr", Account: "fr", BaseURL: srv.URL}, &CLI{JSON: true})
			g.PersistOrderFormID("OF")
			cmd := &CheckoutRunCmd{Payment: "pix", Window: -1, Confirm: tc.confirm, DryRun: tc.dryRun}
			if tc.card {
				cmd.CVV = "000"
			}
			var runErr error
			captureStdout(t, func() { runErr = cmd.Run(g) })
			if (runErr != nil) != tc.wantErr {
				t.Fatalf("checkout error = %v, wantErr %v", runErr, tc.wantErr)
			}
			if (resets == 1) != tc.wantReset || resets > 1 {
				t.Errorf("cart resets = %d, wantReset %v", resets, tc.wantReset)
			}
			if tc.resetFails || tc.stillFull || tc.readFails {
				var typed *errfmt.Error
				if !errors.As(runErr, &typed) || typed.Code != errfmt.ExitDomain ||
					!strings.Contains(runErr.Error(), "1234") || !strings.Contains(runErr.Error(), "Do not re-run checkout") {
					t.Errorf("cleanup failure must identify the placed order and forbid checkout retry: %v", runErr)
				}
				if tc.card && typed != nil && (!strings.Contains(typed.Error(), "merchant name missing") || !strings.Contains(typed.Error(), "remove all items: HTTP 503")) {
					t.Errorf("rendered CLI error must retain both failures: %v", typed)
				}
			}
			if tc.name == "placed" {
				out := captureStdout(t, func() {
					if err := (&CartShowCmd{}).Run(g); err != nil {
						t.Fatal(err)
					}
				})
				if strings.TrimSpace(out) != "[]" {
					t.Fatalf("cart after checkout = %s, want []", out)
				}
				captureStdout(t, func() {
					if err := (&CartAddCmd{SKU: "NEXT", Seller: "1", Qty: 1}).Run(g); err != nil {
						t.Fatal(err)
					}
				})
				if strings.Contains(items, `"134"`) || !strings.Contains(items, `"NEXT"`) {
					t.Fatalf("next cart retained the old order: %s", items)
				}
			}
		})
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
