package vtex

import (
	"encoding/json"
	"fmt"
	"net/url"
)

// Identifier lookups are deliberately separate from Search.
//
// A VTEX store numbers products and SKUs from two independent sequences,
// and on a grocery catalog they overlap almost completely. Recorded against
// Zona Sul on 2026-09-23:
//
//	id      as a SKU                     as a product id
//	36941   Vinho Tinto Chileno          Filé de Salmão Tablete
//	37014   Filé de Salmão Tablete       Desodorante Dove
//	33345   Sopa do Jão Holy Soup        Peixe Salmão Temperado
//	46047   Jogo de Frigideiras          Lombo de Salmão
//
// So the namespace is not a detail of a lookup — it *is* the lookup. Asking
// a relevance ranker for "36941" and then accepting whichever of the two
// namespaces matched first returns an unrelated grocery, which is what the
// CLI did through v0.4.4.
//
// Only the legacy catalog API filters on an id exactly. Intelligent Search
// accepts fq=skuId and silently ignores it: it answers 200 with the entire
// unfiltered catalog (recordsFiltered unchanged at 23769 for every id
// tried), which is worse than an error because it looks like a result. That
// is why these functions pin the catalog endpoint instead of honouring the
// descriptor's SearchMode — the mode selects a *search* backend, and this
// is not a search.

// SKUByID resolves exactly one SKU by its id.
//
// It never falls back to the product-id sequence: an id that is not a SKU
// reports ok=false, even when the same number names a product. A missing
// SKU is an ordinary answer rather than a failure, so err stays nil.
func (c *Client) SKUByID(sku string) (SearchResult, bool, error) {
	products, err := c.catalogByID("skuId", sku)
	if err != nil {
		return SearchResult{}, false, err
	}
	// The filter matches an item but the endpoint answers with its whole
	// parent product, so the requested item still has to be picked out —
	// items[0] is the wrong SKU for any product with variants.
	for _, r := range toResults(products) {
		if r.SKU == sku {
			return r, true, nil
		}
	}
	return SearchResult{}, false, nil
}

// SKUTitle returns the catalog name of a SKU id whether or not any seller
// currently stocks it.
//
// SKUByID answers only about purchasable SKUs, so a caller reporting a miss
// needs this to tell "there is no such SKU" apart from "the store is out of
// it". They are different problems for whoever is shopping, and reporting
// the second as the first sends them hunting for a typo that isn't there.
func (c *Client) SKUTitle(sku string) (string, bool, error) {
	products, err := c.catalogByID("skuId", sku)
	if err != nil {
		return "", false, err
	}
	for _, p := range products {
		for _, item := range p.Items {
			if item.ItemID != sku {
				continue
			}
			if item.Name != "" {
				return item.Name, true, nil
			}
			return p.ProductName, true, nil
		}
	}
	return "", false, nil
}

// ProductSKUs names a product id and lists its SKU ids.
//
// This is the other half of telling a caller what an id actually is: when a
// SKU lookup misses, the number is usually a product id, and naming the
// product's SKUs turns a dead end into the next command to run.
//
// Stock is deliberately not considered. What a number names is a catalog
// fact — a product whose SKUs are all sold out is still that product — and
// someone who typed a product id needs the SKU either way. Prices are not
// returned for the same reason: they would have to be invented for the
// sold-out SKUs this has to keep listing.
func (c *Client) ProductSKUs(productID string) (string, []string, error) {
	products, err := c.catalogByID("productId", productID)
	if err != nil {
		return "", nil, err
	}
	for _, p := range products {
		// The filter can answer with neighbours; only the requested
		// product may be reported back as what the id names.
		if p.ProductID != productID {
			continue
		}
		skus := make([]string, 0, len(p.Items))
		for _, item := range p.Items {
			skus = append(skus, item.ItemID)
		}
		if len(skus) == 0 {
			continue
		}
		return p.ProductName, skus, nil
	}
	return "", nil, nil
}

// catalogByID runs one exact catalog filter, e.g. fq=skuId:37014. An id
// absent from the catalog comes back as an empty array with HTTP 200.
func (c *Client) catalogByID(field, id string) ([]rawProduct, error) {
	if id == "" {
		return nil, fmt.Errorf("lookup by %s: empty id", field)
	}
	q := url.Values{"fq": {field + ":" + id}}
	body, err := c.Get("/api/catalog_system/pub/products/search/?" + q.Encode())
	if err != nil {
		return nil, fmt.Errorf("lookup %s %s: %w", field, id, err)
	}
	var products []rawProduct
	if err := json.Unmarshal(body, &products); err != nil {
		return nil, fmt.Errorf("lookup %s %s parse: %w", field, id, err)
	}
	return products, nil
}
