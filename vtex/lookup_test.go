package vtex_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/voska/vtexkit/store"
	"github.com/voska/vtexkit/vtex"
)

// The two ids below are real Zona Sul catalog entries, recorded 2026-09-23.
// They are the whole reason this file exists: 37014 is a salmon fillet as a
// SKU and a deodorant as a product id. Resolving one namespace and accepting
// the other returns an unrelated grocery.
const (
	salmonProduct = `{"productId":"36941","productName":"Filé de Salmão Tablete Cia do Peixe 400g",
		"items":[{"itemId":"37014","name":"Filé de Salmão Tablete Cia do Peixe 400g",
		"measurementUnit":"un","unitMultiplier":1,"sellers":[{"sellerId":"zonasulzsa",
		"sellerDefault":true,"commertialOffer":{"Price":74.9,"ListPrice":74.9,
		"AvailableQuantity":8,"IsAvailable":true}}]}]}`

	deodorantProduct = `{"productId":"37014","productName":"Desodorante Dove Creme Sérum 50g",
		"items":[{"itemId":"37087","name":"Antitranspirante Creme Sérum 50g",
		"measurementUnit":"un","unitMultiplier":1,"sellers":[{"sellerId":"zonasulzsa",
		"sellerDefault":true,"commertialOffer":{"Price":19.9,"ListPrice":19.9,
		"AvailableQuantity":4,"IsAvailable":true}}]}]}`
)

// catalogFilter reports which fq filter a request carried, e.g. "skuId:37014".
func catalogFilter(r *http.Request) string {
	fq, _ := url.QueryUnescape(r.URL.Query().Get("fq"))
	return fq
}

// exactLookupServer answers the catalog filter endpoint from a fq -> body
// map and fails the test if Intelligent Search is queried. Intelligent
// Search accepts fq=skuId and ignores it — it answers 200 with the whole
// unfiltered catalog — so an id lookup that reaches it is silently wrong.
func exactLookupServer(t *testing.T, mode store.SearchMode, bodies map[string]string) *vtex.Client {
	t.Helper()
	return searchClient(t, mode, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "intelligent-search") {
			t.Errorf("id lookup queried Intelligent Search (%s); it ignores fq=skuId", r.URL)
			w.WriteHeader(http.StatusTeapot)
			return
		}
		if !strings.Contains(r.URL.Path, "catalog_system") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		body, ok := bodies[catalogFilter(r)]
		if !ok {
			body = "[]"
		}
		_, _ = w.Write([]byte(body))
	})
}

func TestSKUByIDUsesTheExactFilterNotFreeTextSearch(t *testing.T) {
	var gotFilter string
	c := searchClient(t, store.SearchIntelligentREST, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "intelligent-search") {
			t.Fatalf("id lookup must not use Intelligent Search: %s", r.URL)
		}
		if got := r.URL.Query().Get("ft"); got != "" {
			t.Errorf("id lookup sent free text ft=%q; an id is not a search term", got)
		}
		gotFilter = catalogFilter(r)
		_, _ = w.Write([]byte("[" + salmonProduct + "]"))
	})

	got, ok, err := c.SKUByID("37014")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("SKU 37014 not resolved")
	}
	if gotFilter != "skuId:37014" {
		t.Errorf("fq = %q, want skuId:37014", gotFilter)
	}
	if got.SKU != "37014" || got.ProductID != "36941" {
		t.Errorf("SKU=%q ProductID=%q, want 37014 / 36941", got.SKU, got.ProductID)
	}
	if got.Seller != "zonasulzsa" {
		t.Errorf("Seller = %q, want it read from the response", got.Seller)
	}
}

// The store answering a filter with a product that does not carry the
// requested SKU is the failure this whole change exists to prevent: the
// number is a valid id in the other namespace. Nothing may be returned.
func TestSKUByIDNeverReturnsAProductIDMatch(t *testing.T) {
	c := exactLookupServer(t, store.SearchAuto, map[string]string{
		// 36941 is a product id here (the salmon) but the caller asked for
		// a SKU. The real SKU 36941 is a wine, which this store does not
		// have, so the honest answer is "no such SKU".
		"skuId:36941": "[" + salmonProduct + "]",
	})

	got, ok, err := c.SKUByID("36941")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatalf("resolved SKU 36941 to %q (product %s) — that is a product-id match, not a SKU",
			got.Name, got.ProductID)
	}
}

func TestSKUByIDPicksTheRequestedItemFromAMultiSKUProduct(t *testing.T) {
	multi := `[{"productId":"900","productName":"Vinho Gran Reserva","items":[
		{"itemId":"901","name":"Garrafa 750ml","measurementUnit":"un","unitMultiplier":1,
		 "sellers":[{"sellerId":"s","sellerDefault":true,
		  "commertialOffer":{"Price":50,"AvailableQuantity":3,"IsAvailable":true}}]},
		{"itemId":"902","name":"Caixa com 6","measurementUnit":"un","unitMultiplier":1,
		 "sellers":[{"sellerId":"s","sellerDefault":true,
		  "commertialOffer":{"Price":280,"AvailableQuantity":2,"IsAvailable":true}}]}]}]`
	c := exactLookupServer(t, store.SearchAuto, map[string]string{"skuId:902": multi})

	got, ok, err := c.SKUByID("902")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("SKU 902 not resolved")
	}
	// The filter matches the SKU but the endpoint answers with the whole
	// parent product, so taking items[0] would hand back the bottle when
	// the caller asked for the case.
	if got.SKU != "902" || got.Name != "Caixa com 6" {
		t.Errorf("got SKU=%q name=%q, want 902 / Caixa com 6", got.SKU, got.Name)
	}
}

func TestSKUByIDReportsMissingWithoutError(t *testing.T) {
	c := exactLookupServer(t, store.SearchAuto, nil)

	got, ok, err := c.SKUByID("999999999")
	if err != nil {
		t.Fatalf("an absent SKU is an ordinary answer, not a failure: %v", err)
	}
	if ok {
		t.Errorf("resolved a SKU that does not exist: %+v", got)
	}
}

func TestProductSKUsResolvesTheOtherNamespace(t *testing.T) {
	c := exactLookupServer(t, store.SearchAuto, map[string]string{
		"productId:37014": "[" + deodorantProduct + "]",
	})

	name, skus, err := c.ProductSKUs("37014")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(name, "Dove") {
		t.Errorf("name = %q, want the deodorant that product 37014 names", name)
	}
	if len(skus) != 1 || skus[0] != "37087" {
		t.Errorf("skus = %v, want [37087]", skus)
	}
}

// A filter answered with some other product must not leak through: the
// caller is about to print "this is what that id really is".
func TestProductSKUsDropsForeignProducts(t *testing.T) {
	c := exactLookupServer(t, store.SearchAuto, map[string]string{
		"productId:37014": "[" + salmonProduct + "," + deodorantProduct + "]",
	})

	name, skus, err := c.ProductSKUs("37014")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(name, "Salmão") || len(skus) != 1 || skus[0] != "37087" {
		t.Errorf("got name=%q skus=%v, want only product 37014's own SKUs", name, skus)
	}
}

// The explanation must survive a sold-out product: someone who typed a
// product id needs its SKU whether or not the store currently stocks it.
func TestProductSKUsListsSoldOutSKUs(t *testing.T) {
	soldOut := `[{"productId":"1007","productName":"Escova Dental Dentil Whitening",
		"items":[{"itemId":"1000","name":"Escova Dental Dentil Whitening","sellers":[
		{"sellerId":"zonasulzsa","sellerDefault":true,
		 "commertialOffer":{"Price":9.9,"AvailableQuantity":0,"IsAvailable":false}}]}]}]`
	c := exactLookupServer(t, store.SearchAuto, map[string]string{"productId:1007": soldOut})

	name, skus, err := c.ProductSKUs("1007")
	if err != nil {
		t.Fatal(err)
	}
	if name == "" || len(skus) != 1 || skus[0] != "1000" {
		t.Errorf("got name=%q skus=%v, want the sold-out SKU listed", name, skus)
	}
}

func TestSKUTitleFindsASoldOutSKU(t *testing.T) {
	soldOut := `[{"productId":"36868","productName":"Vinho Tinto Chileno",
		"items":[{"itemId":"36941","name":"Vinho Tinto Chileno Garrafa 750ml","sellers":[
		{"sellerId":"zonasulzsa","sellerDefault":true,
		 "commertialOffer":{"Price":89.9,"AvailableQuantity":0,"IsAvailable":false}}]}]}]`
	c := exactLookupServer(t, store.SearchAuto, map[string]string{"skuId:36941": soldOut})

	// SKUByID answers only about purchasable SKUs ...
	if _, ok, err := c.SKUByID("36941"); err != nil || ok {
		t.Errorf("SKUByID = ok:%v err:%v, want an unbuyable SKU withheld", ok, err)
	}
	// ... but the id is a real SKU, and saying otherwise sends the caller
	// hunting for a typo.
	title, ok, err := c.SKUTitle("36941")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !strings.Contains(title, "Vinho") {
		t.Errorf("SKUTitle = %q, %v; want the wine named", title, ok)
	}
}

func TestIDLookupsRejectEmptyInput(t *testing.T) {
	c := exactLookupServer(t, store.SearchAuto, nil)
	if _, ok, err := c.SKUByID(""); ok || err == nil {
		t.Errorf("SKUByID(\"\") = ok:%v err:%v, want a refusal", ok, err)
	}
	if _, _, err := c.ProductSKUs(""); err == nil {
		t.Error("ProductSKUs(\"\") must refuse rather than query the catalog")
	}
	if _, _, err := c.SKUTitle(""); err == nil {
		t.Error("SKUTitle(\"\") must refuse rather than query the catalog")
	}
}
