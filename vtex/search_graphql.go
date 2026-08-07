package vtex

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/voska/vtexkit/cli/errfmt"
)

// searchGraphQL queries VTEX Intelligent Search through a persisted GraphQL
// query. It exists only for stores that block both REST paths.
//
// It requires SearchHash and BindingID on the descriptor. VTEX rotates the
// hash on every search-graphql release, and a stale hash returns
// PERSISTED_QUERY_NOT_FOUND — which this surfaces loudly rather than
// reporting as an empty result set.
func (c *Client) searchGraphQL(query string, limit int) ([]SearchResult, error) {
	if c.store.SearchHash == "" {
		return nil, errfmt.Config(
			"SearchGraphQL requires SearchHash on the store descriptor")
	}

	vars := map[string]any{
		"hideUnavailableItems": true,
		"skusFilter":           "ALL",
		"simulationBehavior":   "default",
		"installmentCriteria":  "MAX_WITHOUT_INTEREST",
		"productOriginVtex":    true,
		"map":                  "ft",
		"query":                query,
		"orderBy":              "OrderByScoreDESC",
		"from":                 0,
		"to":                   limit - 1,
		"selectedFacets":       []map[string]string{{"key": "ft", "value": query}},
		"fullText":             query,
		"facetsBehavior":       "Static",
		"categoryTreeBehavior": "default",
		"withFacets":           false,
		"variant":              "null-null",
	}
	varsJSON, _ := json.Marshal(vars)

	extensions := map[string]any{
		"persistedQuery": map[string]any{
			"version":    1,
			"sha256Hash": c.store.SearchHash,
			"sender":     "vtex.store-resources@0.x",
			"provider":   "vtex.search-graphql@0.x",
		},
		"variables": base64.StdEncoding.EncodeToString(varsJSON),
	}
	extJSON, _ := json.Marshal(extensions)

	path := fmt.Sprintf(
		"/_v/segment/graphql/v1?workspace=master&maxAge=short&appsEtag=remove&domain=store&locale=pt-BR&__bindingId=%s&operationName=productSearchV3&variables=%%7B%%7D&extensions=%s",
		c.store.BindingID, url.QueryEscape(string(extJSON)))

	body, err := c.Get(path)
	if err != nil {
		return nil, fmt.Errorf("search (graphql): %w", err)
	}

	var raw struct {
		Errors []struct {
			Message    string `json:"message"`
			Extensions struct {
				Code string `json:"code"`
			} `json:"extensions"`
		} `json:"errors"`
		Data struct {
			ProductSearch struct {
				Products []rawProduct `json:"products"`
			} `json:"productSearch"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("search (graphql) parse: %w", err)
	}
	if len(raw.Errors) > 0 {
		first := raw.Errors[0]
		detail := first.Message
		if first.Extensions.Code != "" {
			detail = first.Extensions.Code + ": " + first.Message
		}
		return nil, fmt.Errorf(
			"search (graphql): VTEX rejected the request (%s); the persisted-query hash may be stale — re-capture it from a live browser session, or switch the descriptor to SearchAuto",
			detail)
	}
	return toResults(raw.Data.ProductSearch.Products), nil
}
