package vtex

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/voska/vtexkit/cli/errfmt"
)

// WishlistItem is one saved product from the store's own wishlist.
//
// ProductID and SKU differ for some products (a product with variants), and
// the cart API needs the SKU, so both are kept.
type WishlistItem struct {
	ProductID string `json:"productId"`
	SKU       string `json:"sku"`
	Title     string `json:"title"`
}

type Wishlist struct {
	Name   string         `json:"name"`
	Public bool           `json:"public"`
	Items  []WishlistItem `json:"items"`
}

// Wishlists returns the shopper's server-side wishlists — the same lists the
// storefront's heart icons write to.
//
// This has to go through vtex.wish-list's persisted GraphQL query. A plain
// query against the same provider is rejected with a 500, so unlike search
// there is no hash-free path available. Two things are easy to get wrong and
// both fail as an opaque 500:
//
//   - sender must be vtex.wish-list@1.x, not the usual vtex.store@0.x
//   - variables travel base64-encoded inside extensions, with the top-level
//     variables object left empty
//
// The hash is per store because it is tied to the installed app version.
func (c *Client) Wishlists(shopperID string) ([]Wishlist, error) {
	if c.store.WishlistHash == "" {
		return nil, errfmt.Config(fmt.Sprintf(
			"store %q has no WishlistHash in its descriptor, so the store wishlist cannot be read",
			c.store.Name))
	}
	if shopperID == "" {
		return nil, errfmt.Auth("wishlist requires a logged-in shopper")
	}

	vars, _ := json.Marshal(map[string]string{"shopperId": shopperID})
	payload := map[string]any{
		"operationName": "ViewLists",
		"variables":     map[string]any{},
		"extensions": map[string]any{
			"persistedQuery": map[string]any{
				"version":    1,
				"sha256Hash": c.store.WishlistHash,
				"sender":     "vtex.wish-list@1.x",
				"provider":   "vtex.wish-list@1.x",
			},
			"variables": base64.StdEncoding.EncodeToString(vars),
		},
	}

	body, err := c.PostJSON(
		"/_v/private/graphql/v1?workspace=master&maxAge=zero&appsEtag=remove&domain=store&locale=pt-BR",
		payload)
	if err != nil {
		return nil, fmt.Errorf("wishlist: %w", err)
	}

	var raw struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
		Data struct {
			ViewLists []struct {
				Name   string         `json:"name"`
				Public bool           `json:"public"`
				Data   []WishlistItem `json:"data"`
			} `json:"viewLists"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("wishlist parse: %w", err)
	}
	if len(raw.Errors) > 0 {
		// Same failure mode as a stale search hash: it reads as "no
		// wishlist" rather than "broken", so say so plainly.
		return nil, fmt.Errorf(
			"wishlist: store rejected the request (%s); the WishlistHash may be stale after a vtex.wish-list upgrade — re-capture it from a live browser session",
			raw.Errors[0].Message)
	}

	out := make([]Wishlist, 0, len(raw.Data.ViewLists))
	for _, l := range raw.Data.ViewLists {
		out = append(out, Wishlist{Name: l.Name, Public: l.Public, Items: l.Data})
	}
	return out, nil
}
