package vtex

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/voska/vtexkit/cli/errfmt"
)

// WishlistItem is one saved product from the store's own wishlist.
//
// ID is the item's position-derived identifier within the list, and it is
// what RemoveFromList takes — not the SKU. ProductID and SKU genuinely
// differ for products with variants, and AddToList wants both.
type WishlistItem struct {
	ID        int    `json:"id"`
	ProductID string `json:"productId"`
	SKU       string `json:"sku"`
	Title     string `json:"title"`
}

type Wishlist struct {
	Name   string         `json:"name"`
	Public bool           `json:"public"`
	Items  []WishlistItem `json:"items"`
}

// DefaultWishlistName is the list vtex.wish-list writes to when the
// storefront's heart icon is used.
const DefaultWishlistName = "Wishlist"

// wishlistCall issues one vtex.wish-list persisted-query operation.
//
// Three things about this API are easy to get wrong and all fail as an
// opaque HTTP 500: sender must be vtex.wish-list@1.x rather than the usual
// vtex.store@0.x; variables travel base64-encoded inside extensions with
// the top-level variables object left empty; and a plain, non-persisted
// query is rejected outright, so the hash is mandatory.
func (c *Client) wishlistCall(operation, hash string, vars any, out any) error {
	if hash == "" {
		return errfmt.Config(fmt.Sprintf(
			"store %q has no wishlist %s hash in its descriptor", c.store.Name, operation))
	}
	encoded, err := json.Marshal(vars)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"operationName": operation,
		"variables":     map[string]any{},
		"extensions": map[string]any{
			"persistedQuery": map[string]any{
				"version":    1,
				"sha256Hash": hash,
				"sender":     "vtex.wish-list@1.x",
				"provider":   "vtex.wish-list@1.x",
			},
			"variables": base64.StdEncoding.EncodeToString(encoded),
		},
	}

	body, err := c.PostJSON(
		"/_v/private/graphql/v1?workspace=master&maxAge=zero&appsEtag=remove&domain=store&locale=pt-BR",
		payload)
	if err != nil {
		return fmt.Errorf("wishlist %s: %w", operation, err)
	}

	var envelope struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("wishlist %s parse: %w", operation, err)
	}
	if len(envelope.Errors) > 0 {
		// A stale hash reads as "no wishlist" rather than "broken", so
		// name the cause instead of returning an empty result.
		return fmt.Errorf(
			"wishlist %s: store rejected the request (%s); the hash may be stale after a vtex.wish-list upgrade — re-capture it from a live browser session",
			operation, envelope.Errors[0].Message)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(envelope.Data, out)
}

// Wishlists returns the shopper's server-side wishlists — the same lists the
// storefront's heart icons write to.
func (c *Client) Wishlists(shopperID string) ([]Wishlist, error) {
	if shopperID == "" {
		return nil, errfmt.Auth("wishlist requires a logged-in shopper")
	}
	var data struct {
		ViewLists []struct {
			Name   string         `json:"name"`
			Public bool           `json:"public"`
			Data   []WishlistItem `json:"data"`
		} `json:"viewLists"`
	}
	if err := c.wishlistCall("ViewLists", c.store.Wishlist.View,
		map[string]string{"shopperId": shopperID}, &data); err != nil {
		return nil, err
	}
	out := make([]Wishlist, 0, len(data.ViewLists))
	for _, l := range data.ViewLists {
		out = append(out, Wishlist{Name: l.Name, Public: l.Public, Items: l.Data})
	}
	return out, nil
}

// AddToWishlist saves a product to the shopper's wishlist. productID and sku
// differ for products with variants and the API wants both.
func (c *Client) AddToWishlist(shopperID, listName string, item WishlistItem) error {
	if shopperID == "" {
		return errfmt.Auth("wishlist requires a logged-in shopper")
	}
	if listName == "" {
		listName = DefaultWishlistName
	}
	return c.wishlistCall("AddToList", c.store.Wishlist.Add, map[string]any{
		"listItem": map[string]string{
			"productId": item.ProductID,
			"sku":       item.SKU,
			"title":     item.Title,
		},
		"shopperId": shopperID,
		"name":      listName,
	}, nil)
}

// RemoveFromWishlist deletes an item by its wishlist ID, which is not the
// SKU. Callers resolve a SKU to an ID by reading the list first.
func (c *Client) RemoveFromWishlist(shopperID, listName string, id int) (bool, error) {
	if shopperID == "" {
		return false, errfmt.Auth("wishlist requires a logged-in shopper")
	}
	if listName == "" {
		listName = DefaultWishlistName
	}
	var data struct {
		RemoveFromList bool `json:"removeFromList"`
	}
	if err := c.wishlistCall("RemoveFromList", c.store.Wishlist.Remove, map[string]any{
		"id":        id,
		"shopperId": shopperID,
		"name":      listName,
	}, &data); err != nil {
		return false, err
	}
	return data.RemoveFromList, nil
}
