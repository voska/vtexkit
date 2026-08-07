package cli

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/voska/vtexkit/cli/errfmt"
	"github.com/voska/vtexkit/money"
	"github.com/voska/vtexkit/store"
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
