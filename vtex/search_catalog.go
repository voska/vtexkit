package vtex

import (
	"encoding/json"
	"fmt"
	"net/url"
)

// searchCatalog queries the legacy public catalog API. It is the fallback
// for stores that block Intelligent Search: Carrefour, for instance, answers
// one and refuses the other.
//
// This endpoint answers 206 Partial Content for a bounded range, which the
// client treats as success since it is below 400.
func (c *Client) searchCatalog(query string, limit int) ([]SearchResult, error) {
	path := fmt.Sprintf("/api/catalog_system/pub/products/search/?ft=%s&_from=0&_to=%d",
		url.QueryEscape(query), limit-1)
	body, err := c.Get(path)
	if err != nil {
		return nil, fmt.Errorf("search (catalog): %w", err)
	}

	var products []rawProduct
	if err := json.Unmarshal(body, &products); err != nil {
		return nil, fmt.Errorf("search (catalog) parse: %w", err)
	}
	return toResults(products), nil
}
