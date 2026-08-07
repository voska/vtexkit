package vtex_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/voska/vtexkit/money"
	"github.com/voska/vtexkit/store"
	"github.com/voska/vtexkit/vtex"
)

// searchClient points a client with the given mode at a test server.
func searchClient(t *testing.T, mode store.SearchMode, h http.HandlerFunc) *vtex.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return vtex.New(store.Store{
		Name: "t", Account: "testacct", BaseURL: srv.URL, Search: mode,
	}, "")
}

func TestSearchCatalogParsesLiveFixture(t *testing.T) {
	c := searchClient(t, store.SearchCatalogREST, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "catalog_system") {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("ft"); got != "salmao" {
			t.Errorf("ft = %q", got)
		}
		_, _ = w.Write(mustReadFile(t, "testdata/search_catalog.json"))
	})

	got, err := c.Search("salmao", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("no results parsed from the live fixture")
	}
	r := got[0]
	if r.SKU != "134" || r.ProductID != "134" {
		t.Errorf("SKU=%q ProductID=%q", r.SKU, r.ProductID)
	}
	// Recorded live: R$153,72 as a decimal-reais float.
	if r.Price != money.Centavos(15372) {
		t.Errorf("Price = %d, want 15372 centavos", int64(r.Price))
	}
	if r.Available != 10 {
		t.Errorf("Available = %d, want 10", r.Available)
	}
	if r.Unit != "un" {
		t.Errorf("Unit = %q", r.Unit)
	}
}

func TestSearchReadsSellerFromResponse(t *testing.T) {
	c := searchClient(t, store.SearchCatalogREST, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(mustReadFile(t, "testdata/search_catalog.json"))
	})
	got, err := c.Search("salmao", 1)
	if err != nil {
		t.Fatal(err)
	}
	// Frescatto's seller is "1"; zonasul's was "zonasulzsa". Neither may
	// be hardcoded — the cart API needs whatever the catalog reported.
	if got[0].Seller != "1" {
		t.Errorf("Seller = %q, want it read from the response", got[0].Seller)
	}
}

func TestSearchIntelligentParsesLiveFixture(t *testing.T) {
	c := searchClient(t, store.SearchIntelligentREST, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "intelligent-search") {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("count"); got != "5" {
			t.Errorf("count = %q, want 5", got)
		}
		_, _ = w.Write(mustReadFile(t, "testdata/search_is.json"))
	})

	got, err := c.Search("salmao", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2 from the fixture", len(got))
	}
	for _, r := range got {
		if r.SKU == "" || r.Seller == "" || r.Price <= 0 {
			t.Errorf("incomplete result: %+v", r)
		}
	}
}

func TestSearchAutoFallsBackToCatalog(t *testing.T) {
	var isHits, catalogHits int
	c := searchClient(t, store.SearchAuto, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "intelligent-search"):
			isHits++
			// Carrefour answers 403 here but serves catalog REST.
			w.WriteHeader(http.StatusForbidden)
		case strings.Contains(r.URL.Path, "catalog_system"):
			catalogHits++
			_, _ = w.Write(mustReadFile(t, "testdata/search_catalog.json"))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})

	got, err := c.Search("salmao", 10)
	if err != nil {
		t.Fatal(err)
	}
	if isHits != 1 || catalogHits != 1 {
		t.Errorf("intelligent hits=%d catalog hits=%d, want 1 and 1", isHits, catalogHits)
	}
	if len(got) == 0 {
		t.Fatal("fallback produced no results")
	}
}

func TestSearchAutoPrefersIntelligentWhenItWorks(t *testing.T) {
	var catalogHits int
	c := searchClient(t, store.SearchAuto, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "catalog_system") {
			catalogHits++
		}
		_, _ = w.Write(mustReadFile(t, "testdata/search_is.json"))
	})
	if _, err := c.Search("salmao", 5); err != nil {
		t.Fatal(err)
	}
	if catalogHits != 0 {
		t.Error("catalog must not be queried when intelligent search succeeds")
	}
}

func TestSearchSkipsItemsWithNoSellerInStock(t *testing.T) {
	body := `[{"productId":"1","productName":"P","items":[
		{"itemId":"in","name":"In stock","sellers":[
			{"sellerId":"1","sellerDefault":true,
			 "commertialOffer":{"Price":10,"AvailableQuantity":5,"IsAvailable":true}}]},
		{"itemId":"out","name":"Out of stock","sellers":[
			{"sellerId":"1","sellerDefault":true,
			 "commertialOffer":{"Price":10,"AvailableQuantity":0,"IsAvailable":false}}]}]}]`
	c := searchClient(t, store.SearchCatalogREST, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	})
	got, err := c.Search("x", 10)
	if err != nil {
		t.Fatal(err)
	}
	// An unbuyable result is worse than none: an agent would cart it and
	// fail at checkout.
	if len(got) != 1 || got[0].SKU != "in" {
		t.Errorf("got %+v, want only the in-stock SKU", got)
	}
}

func TestPickSellerPrefersDefaultThenAnyWithStock(t *testing.T) {
	body := `[{"productId":"1","productName":"P","items":[
		{"itemId":"a","sellers":[
			{"sellerId":"marketplace","sellerDefault":false,
			 "commertialOffer":{"Price":10,"AvailableQuantity":5,"IsAvailable":true}},
			{"sellerId":"house","sellerDefault":true,
			 "commertialOffer":{"Price":9,"AvailableQuantity":5,"IsAvailable":true}}]},
		{"itemId":"b","sellers":[
			{"sellerId":"dead","sellerDefault":true,
			 "commertialOffer":{"Price":10,"AvailableQuantity":0,"IsAvailable":false}},
			{"sellerId":"alive","sellerDefault":false,
			 "commertialOffer":{"Price":11,"AvailableQuantity":3,"IsAvailable":true}}]}]}]`
	c := searchClient(t, store.SearchCatalogREST, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	})
	got, err := c.Search("x", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2", len(got))
	}
	if got[0].Seller != "house" {
		t.Errorf("item a seller = %q, want the in-stock default seller", got[0].Seller)
	}
	// A default seller with no stock must not win over one that has it.
	if got[1].Seller != "alive" {
		t.Errorf("item b seller = %q, want the seller that actually has stock", got[1].Seller)
	}
}

func TestSearchLimitIsClamped(t *testing.T) {
	tests := []struct {
		limit  int
		wantTo string
	}{
		{0, "19"}, // default 20
		{5, "4"},
		{100, "49"}, // clamped to 50
	}
	for _, tt := range tests {
		var gotTo string
		c := searchClient(t, store.SearchCatalogREST, func(w http.ResponseWriter, r *http.Request) {
			gotTo = r.URL.Query().Get("_to")
			_, _ = w.Write([]byte(`[]`))
		})
		if _, err := c.Search("x", tt.limit); err != nil {
			t.Fatal(err)
		}
		if gotTo != tt.wantTo {
			t.Errorf("limit %d produced _to=%s, want %s", tt.limit, gotTo, tt.wantTo)
		}
	}
}

func TestGraphQLSurfacesStaleHashLoudly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"errors":[{"message":"query not found",
			"extensions":{"code":"PERSISTED_QUERY_NOT_FOUND"}}]}`))
	}))
	defer srv.Close()
	c := vtex.New(store.Store{
		Name: "t", Account: "testacct", BaseURL: srv.URL,
		Search: store.SearchGraphQL, SearchHash: "deadbeef", BindingID: "b",
	}, "")

	_, err := c.Search("x", 5)
	if err == nil {
		t.Fatal("a stale hash must error, not silently return zero results")
	}
	if !strings.Contains(err.Error(), "PERSISTED_QUERY_NOT_FOUND") {
		t.Errorf("error must name the VTEX code, got: %v", err)
	}
	if !strings.Contains(err.Error(), "stale") {
		t.Errorf("error must say what to do about it, got: %v", err)
	}
}

func TestGraphQLRequiresHash(t *testing.T) {
	c := vtex.New(store.Store{
		Name: "t", Account: "testacct", BaseURL: "http://unused",
		Search: store.SearchGraphQL,
	}, "")
	_, err := c.Search("x", 5)
	if err == nil || !strings.Contains(err.Error(), "SearchHash") {
		t.Errorf("err = %v, want a config error naming the missing field", err)
	}
}
