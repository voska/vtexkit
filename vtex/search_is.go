package vtex

import (
	"encoding/json"
	"fmt"
	"net/url"
)

// searchIntelligent queries VTEX Intelligent Search over its public REST
// wrapper. This is the preferred backend: same ranking as the storefront,
// but with no persisted-query hash to go stale.
func (c *Client) searchIntelligent(query string, limit int) ([]SearchResult, error) {
	q := url.Values{
		"query":  {query},
		"count":  {fmt.Sprintf("%d", limit)},
		"locale": {"pt-BR"},
	}
	body, err := c.Get("/api/io/_v/api/intelligent-search/product_search/?" + q.Encode())
	if err != nil {
		return nil, fmt.Errorf("search (intelligent): %w", err)
	}

	var raw struct {
		Products []rawProduct `json:"products"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("search (intelligent) parse: %w", err)
	}
	return toResults(raw.Products), nil
}
