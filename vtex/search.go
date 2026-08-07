package vtex

import (
	"github.com/voska/vtexkit/money"
	"github.com/voska/vtexkit/store"
)

// SearchResult is one purchasable SKU.
//
// Seller is carried per result rather than assumed from a constant: it is
// the value the cart API needs, and it differs per store (and in principle
// per item within a store).
type SearchResult struct {
	ProductID string         `json:"productId"`
	SKU       string         `json:"sku"`
	Name      string         `json:"name"`
	Price     money.Centavos `json:"price"`
	ListPrice money.Centavos `json:"listPrice"`
	Available int            `json:"available"`
	Seller    string         `json:"seller"`
	Unit      string         `json:"unit"`
	UnitMult  float64        `json:"unitMultiplier"`
}

const (
	defaultSearchLimit = 20
	maxSearchLimit     = 50
)

// Search queries the store catalog using the descriptor's configured mode.
//
// SearchAuto tries Intelligent Search REST first and falls back to the
// catalog REST API. Neither needs a persisted GraphQL hash, which is what
// made the previous implementation brittle: VTEX rotates that hash on every
// search-graphql release and a stale one returns no results.
func (c *Client) Search(query string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}

	switch c.store.Search {
	case store.SearchIntelligentREST:
		return c.searchIntelligent(query, limit)
	case store.SearchCatalogREST:
		return c.searchCatalog(query, limit)
	case store.SearchGraphQL:
		return c.searchGraphQL(query, limit)
	default:
		results, err := c.searchIntelligent(query, limit)
		if err == nil {
			return results, nil
		}
		return c.searchCatalog(query, limit)
	}
}

// rawSeller is the seller shape both REST backends share.
type rawSeller struct {
	SellerID        string `json:"sellerId"`
	SellerName      string `json:"sellerName"`
	SellerDefault   bool   `json:"sellerDefault"`
	CommertialOffer struct {
		Price             float64 `json:"Price"`
		ListPrice         float64 `json:"ListPrice"`
		AvailableQuantity int     `json:"AvailableQuantity"`
		IsAvailable       bool    `json:"IsAvailable"`
	} `json:"commertialOffer"`
}

type rawItem struct {
	ItemID          string      `json:"itemId"`
	Name            string      `json:"name"`
	MeasurementUnit string      `json:"measurementUnit"`
	UnitMultiplier  float64     `json:"unitMultiplier"`
	Sellers         []rawSeller `json:"sellers"`
}

type rawProduct struct {
	ProductID   string    `json:"productId"`
	ProductName string    `json:"productName"`
	Items       []rawItem `json:"items"`
}

// pickSeller chooses which seller to order from: the default seller when it
// has stock, otherwise the first seller that does. Hardcoding a seller ID is
// what made the earlier implementations store-specific.
func pickSeller(sellers []rawSeller) (rawSeller, bool) {
	for _, s := range sellers {
		if s.SellerDefault && (s.CommertialOffer.IsAvailable || s.CommertialOffer.AvailableQuantity > 0) {
			return s, true
		}
	}
	for _, s := range sellers {
		if s.CommertialOffer.IsAvailable || s.CommertialOffer.AvailableQuantity > 0 {
			return s, true
		}
	}
	return rawSeller{}, false
}

// toResults flattens products to purchasable SKUs, dropping anything with no
// seller in stock. An out-of-stock result is worse than no result: an agent
// would add it to a cart and fail at checkout.
func toResults(products []rawProduct) []SearchResult {
	var out []SearchResult
	for _, p := range products {
		for _, item := range p.Items {
			seller, ok := pickSeller(item.Sellers)
			if !ok {
				continue
			}
			name := item.Name
			if name == "" {
				name = p.ProductName
			}
			out = append(out, SearchResult{
				ProductID: p.ProductID,
				SKU:       item.ItemID,
				Name:      name,
				Price:     money.Reais(seller.CommertialOffer.Price),
				ListPrice: money.Reais(seller.CommertialOffer.ListPrice),
				Available: seller.CommertialOffer.AvailableQuantity,
				Seller:    seller.SellerID,
				Unit:      item.MeasurementUnit,
				UnitMult:  item.UnitMultiplier,
			})
		}
	}
	return out
}
