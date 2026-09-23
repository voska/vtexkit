package cli

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/voska/vtexkit/cli/errfmt"
	"github.com/voska/vtexkit/store"
	"github.com/voska/vtexkit/vtex"
)

// These fixtures are real Zona Sul catalog entries recorded 2026-09-23, and
// they are why product ids may never stand in for SKUs: on that store the
// two sequences overlap almost completely, so a single number names two
// unrelated groceries.
//
//	36941  SKU -> Vinho Tinto Chileno      product -> Filé de Salmão Tablete
//	37014  SKU -> Filé de Salmão Tablete   product -> Desodorante Dove
//	33345  SKU -> Sopa do Jão Holy Soup    product -> Peixe Salmão Temperado
const (
	wineBySKU = `{"productId":"36868","productName":"Vinho Tinto Chileno Cabernet Sauvignon Gran Reserva",
		"items":[{"itemId":"36941","name":"Vinho Tinto Chileno Cabernet Sauvignon Gran Reserva",
		"measurementUnit":"un","unitMultiplier":1,"sellers":[{"sellerId":"zonasulzsa",
		"sellerDefault":true,"commertialOffer":{"Price":89.9,"ListPrice":89.9,
		"AvailableQuantity":6,"IsAvailable":true}}]}]}`

	salmonByProduct = `{"productId":"36941","productName":"Filé de Salmão Tablete Cia do Peixe 400g",
		"items":[{"itemId":"37014","name":"Filé de Salmão Tablete Cia do Peixe 400g",
		"measurementUnit":"un","unitMultiplier":1,"sellers":[{"sellerId":"zonasulzsa",
		"sellerDefault":true,"commertialOffer":{"Price":74.9,"ListPrice":74.9,
		"AvailableQuantity":8,"IsAvailable":true}}]}]}`
)

// collisionStore answers the exact catalog filters for the 36941 collision
// and ranks the product-id match first in free-text search, which is what
// the live store does. Any resolver that still reads free text will hand
// back salmon when asked for wine.
func collisionStore(t *testing.T, withWine bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "intelligent-search") {
			// Live ranking for query=36941: the salmon product first. The
			// wine appears only when this store stocks it, so free text
			// and the exact filter never disagree about what exists.
			products := salmonByProduct
			if withWine {
				products += "," + wineBySKU
			}
			_, _ = w.Write([]byte(`{"products":[` + products + `]}`))
			return
		}
		fq, _ := url.QueryUnescape(r.URL.Query().Get("fq"))
		switch fq {
		case "skuId:36941":
			if withWine {
				_, _ = w.Write([]byte(`[` + wineBySKU + `]`))
				return
			}
		case "productId:36941":
			_, _ = w.Write([]byte(`[` + salmonByProduct + `]`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func collisionGlobals(t *testing.T, srv *httptest.Server, cli *CLI) *Globals {
	t.Helper()
	return testGlobals(t, store.Store{
		Name: "zonasul", DisplayName: "Zona Sul", Account: "zonasul", BaseURL: srv.URL,
	}, cli)
}

// The reported bug: `product 36941` printed the salmon fillet, because the
// product-id match ranked ahead of the SKU match and `r.SKU == id ||
// r.ProductID == id` accepted whichever came first.
func TestProductResolvesTheSKUNamespaceNotWhicheverRankedFirst(t *testing.T) {
	g := collisionGlobals(t, collisionStore(t, true), &CLI{JSON: true})

	out := captureStdout(t, func() {
		if err := (&ProductCmd{SKU: "36941"}).Run(g); err != nil {
			t.Fatal(err)
		}
	})

	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("product --json must be parseable: %v (%s)", err, out)
	}
	if got["sku"] != "36941" {
		t.Errorf("sku = %v, want the SKU that was asked for", got["sku"])
	}
	if name, _ := got["name"].(string); !strings.Contains(name, "Vinho") {
		t.Errorf("name = %q, want the wine — SKU 36941. Salmão is product 36941, "+
			"a different item in a different sequence", name)
	}
}

// When the SKU genuinely does not exist, answering with the product-id
// match is answering a different question. The chef asked for a SKU.
func TestProductRefusesToAnswerWithAProductIDMatch(t *testing.T) {
	g := collisionGlobals(t, collisionStore(t, false), &CLI{JSON: true})

	out := captureStdout(t, func() {
		err := (&ProductCmd{SKU: "36941"}).Run(g)
		if err == nil {
			t.Fatal("a missing SKU must fail, not fall back to the product-id namespace")
		}
		var e *errfmt.Error
		if !errors.As(err, &e) {
			t.Fatalf("err = %T, want *errfmt.Error", err)
		}
		if e.Code != errfmt.ExitNotFound {
			t.Errorf("exit code = %d, want %d (not found)", e.Code, errfmt.ExitNotFound)
		}
		msg := e.Error()
		// The failure has to be actionable: say that the number is a
		// product id, and name the SKU the caller actually wants.
		for _, want := range []string{"product id", "37014", "Salmão"} {
			if !strings.Contains(msg, want) {
				t.Errorf("error %q must mention %q", msg, want)
			}
		}
	})
	if strings.TrimSpace(out) != "" {
		t.Errorf("a refused lookup must print no product data, got: %s", out)
	}
}

// A number in neither namespace gets a plain not-found, with no invented
// explanation about product ids.
func TestProductUnknownIDReportsPlainNotFound(t *testing.T) {
	g := collisionGlobals(t, collisionStore(t, true), &CLI{JSON: true})

	err := (&ProductCmd{SKU: "999999999"}).Run(g)
	if err == nil {
		t.Fatal("want not found")
	}
	var e *errfmt.Error
	if !errors.As(err, &e) {
		t.Fatalf("err = %T, want *errfmt.Error", err)
	}
	if e.Code != errfmt.ExitNotFound {
		t.Errorf("exit code = %d, want %d", e.Code, errfmt.ExitNotFound)
	}
	if strings.Contains(e.Error(), "product id") {
		t.Errorf("error %q claims a product id that does not exist", e.Error())
	}
}

// wishlistServer answers the vtex.wish-list persisted queries with a fixed
// list and records which item id a removal targeted.
func wishlistServer(t *testing.T, items string, removed *int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/authenticated/user") {
			_, _ = w.Write([]byte(`{"user":"a@b.c"}`))
			return
		}
		// Session and other GET plumbing shares this server; only the
		// GraphQL POSTs carry a wishlist operation.
		if r.Method != http.MethodPost {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		var payload struct {
			OperationName string `json:"operationName"`
			Extensions    struct {
				Variables string `json:"variables"`
			} `json:"extensions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode wishlist request: %v", err)
		}
		switch payload.OperationName {
		case "ViewLists":
			_, _ = w.Write([]byte(`{"data":{"viewLists":[{"name":"Wishlist","public":false,"data":[` + items + `]}]}}`))
		case "RemoveFromList":
			raw, err := base64.StdEncoding.DecodeString(payload.Extensions.Variables)
			if err != nil {
				t.Errorf("decode variables: %v", err)
			}
			var vars struct {
				ID int `json:"id"`
			}
			if err := json.Unmarshal(raw, &vars); err != nil {
				t.Errorf("parse variables: %v", err)
			}
			*removed = vars.ID
			_, _ = w.Write([]byte(`{"data":{"removeFromList":true}}`))
		default:
			t.Errorf("unexpected wishlist operation %q", payload.OperationName)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// `fav remove 37014` must delete the salmon that is saved under SKU 37014,
// never the deodorant that merely carries 37014 as its product id. Matching
// either namespace made this a silent wrong-item deletion.
func TestFavRemoveMatchesTheSKUNotTheProductID(t *testing.T) {
	const saved = `{"id":1,"productId":"37014","sku":"37087","title":"Desodorante Dove Creme Sérum 50g"},
		{"id":2,"productId":"36941","sku":"37014","title":"Filé de Salmão Tablete Cia do Peixe 400g"}`

	gotID := -1
	srv := wishlistServer(t, saved, &gotID)
	g := testGlobals(t, store.Store{
		Name: "zonasul", DisplayName: "Zona Sul", Account: "zonasul", BaseURL: srv.URL,
		Wishlist: store.WishlistHashes{View: "v", Add: "a", Remove: "r"},
	}, &CLI{JSON: true})

	if err := (&FavRemoveCmd{SKU: "37014"}).Run(g); err != nil {
		t.Fatal(err)
	}
	if gotID != 2 {
		t.Errorf("removed wishlist item %d, want 2 — item 1 only carries 37014 as a product id", gotID)
	}
}

// The same collision must not make `fav add` report a save it never made.
func TestFavAddIsNotBlockedByAProductIDCollision(t *testing.T) {
	const saved = `{"id":1,"productId":"37014","sku":"37087","title":"Desodorante Dove Creme Sérum 50g"}`

	unused := -1
	srv := wishlistServer(t, saved, &unused)
	g := testGlobals(t, store.Store{
		Name: "zonasul", DisplayName: "Zona Sul", Account: "zonasul", BaseURL: srv.URL,
		Wishlist: store.WishlistHashes{View: "v", Add: "a", Remove: "r"},
	}, &CLI{JSON: true})

	// SKU 37014 is not saved, so this must proceed to the catalog lookup
	// rather than short-circuit on the deodorant's product id. The test
	// server has no catalog, so reaching it is the assertion.
	err := (&FavAddCmd{SKU: "37014", DryRun: true}).Run(g)
	if err == nil {
		t.Fatal("expected the catalog lookup to be attempted")
	}
	if strings.Contains(err.Error(), "already saved") {
		t.Errorf("err = %v, want a catalog lookup rather than a collision short-circuit", err)
	}
}

// An out-of-stock SKU is a real SKU. Calling it "not found in the catalog"
// sends the caller looking for a typo, and explaining it as a product-id
// mix-up would name a different item entirely.
func TestProductDistinguishesOutOfStockFromMissing(t *testing.T) {
	const outOfStock = `[{"productId":"36868","productName":"Vinho Tinto Chileno Gran Reserva",
		"items":[{"itemId":"36941","name":"Vinho Tinto Chileno Gran Reserva",
		"sellers":[{"sellerId":"zonasulzsa","sellerDefault":true,
		"commertialOffer":{"Price":89.9,"AvailableQuantity":0,"IsAvailable":false}}]}]}]`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fq, _ := url.QueryUnescape(r.URL.Query().Get("fq"))
		switch fq {
		case "skuId:36941":
			_, _ = w.Write([]byte(outOfStock))
		case "productId:36941":
			_, _ = w.Write([]byte(`[` + salmonByProduct + `]`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	t.Cleanup(srv.Close)

	err := (&ProductCmd{SKU: "36941"}).Run(collisionGlobals(t, srv, &CLI{JSON: true}))
	if err == nil {
		t.Fatal("an unbuyable SKU must not be returned as a result")
	}
	msg := err.Error()
	if !strings.Contains(msg, "stock") || !strings.Contains(msg, "Vinho") {
		t.Errorf("error %q must say the wine is out of stock", msg)
	}
	if strings.Contains(msg, "Salmão") {
		t.Errorf("error %q explains a real SKU as a product-id mix-up", msg)
	}
	// Exit code stays 5: scripts already branch on it for this case.
	var e *errfmt.Error
	if !errors.As(err, &e) || e.Code != errfmt.ExitNotFound {
		t.Errorf("err = %v, want exit %d", err, errfmt.ExitNotFound)
	}
}

// discoverSeller decides which seller a cart line is bought from, so it is
// the id lookup with money behind it. It matched on SKU only and so could
// not return the wrong item, but it read free-text search: a valid SKU that
// did not rank inside the window came back as "not found in the catalog".
func TestDiscoverSellerUsesTheExactFilterNotFreeTextSearch(t *testing.T) {
	// Free text for this id ranks the salmon product and never surfaces
	// the wine SKU, which is exactly what the live store does.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "intelligent-search") || r.URL.Query().Get("ft") != "" {
			t.Errorf("a cart lookup must not read free-text search: %s", r.URL)
			_, _ = w.Write([]byte(`{"products":[` + salmonByProduct + `]}`))
			return
		}
		if fq, _ := url.QueryUnescape(r.URL.Query().Get("fq")); fq == "skuId:36941" {
			_, _ = w.Write([]byte(`[` + wineBySKU + `]`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)

	seller, err := discoverSeller(testClient(t, srv), "36941")
	if err != nil {
		t.Fatalf("SKU 36941 is in the catalog and purchasable: %v", err)
	}
	if seller != "zonasulzsa" {
		t.Errorf("seller = %q, want the one the catalog reported", seller)
	}
}

// Adding a product id to a cart is the mistake with the worst ending, so
// the refusal has to name the real SKU — and must not offer --seller,
// which would put an id the store does not sell into the cart.
func TestDiscoverSellerRefusesAProductIDWithoutOfferingSellerOverride(t *testing.T) {
	_, err := discoverSeller(testClient(t, collisionStore(t, false)), "36941")
	if err == nil {
		t.Fatal("a product id must not resolve to a seller")
	}
	msg := err.Error()
	for _, want := range []string{"product id", "37014"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q must mention %q", msg, want)
		}
	}
	if strings.Contains(msg, "--seller") {
		t.Errorf("error %q offers --seller for a product id; that would cart a SKU "+
			"the store does not sell", msg)
	}
}

// The escape hatch stays for an id the catalog genuinely does not know:
// marketplace SKUs have always been addable this way.
func TestDiscoverSellerKeepsTheSellerOverrideHintForUnknownIDs(t *testing.T) {
	_, err := discoverSeller(testClient(t, collisionStore(t, true)), "999999999")
	if err == nil {
		t.Fatal("want not found")
	}
	if !strings.Contains(err.Error(), "--seller") {
		t.Errorf("error %q dropped the --seller escape hatch", err.Error())
	}
	var e *errfmt.Error
	if !errors.As(err, &e) || e.Code != errfmt.ExitNotFound {
		t.Errorf("err = %v, want exit %d", err, errfmt.ExitNotFound)
	}
}

// testClient builds an unauthenticated client pointed at a test server.
func testClient(t *testing.T, srv *httptest.Server) *vtex.Client {
	t.Helper()
	return vtex.New(store.Store{
		Name: "zonasul", DisplayName: "Zona Sul", Account: "zonasul", BaseURL: srv.URL,
	}, "")
}
