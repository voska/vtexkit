package cli

import (
	"fmt"

	"github.com/voska/vtexkit/cli/errfmt"
	"github.com/voska/vtexkit/cli/outfmt"
	"github.com/voska/vtexkit/vtex"
)

// FavCmd shows the store's own wishlist — what the heart icons on the
// website save.
//
// It is deliberately read-only and has no bulk-order subcommand. A wishlist
// is things the shopper likes, not things they intend to buy right now;
// "add all of it to the cart" is never what someone means. Curated lists
// that ARE meant to be ordered wholesale live under `list`.
type FavCmd struct{}

func (c *FavCmd) Run(g *Globals) error {
	items, name, err := favorites(g)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return errfmt.Empty()
	}
	if g.Formatter().IsJSON() || g.CLI.Plain || g.CLI.Quiet {
		return g.Formatter().Print(items)
	}
	outfmt.Hint("%s — %d items", name, len(items))
	for _, it := range items {
		fmt.Printf("%-10s %s\n", it.SKU, it.Title)
	}
	outfmt.Hint("Add one with: %s cart add <sku>", g.Store.Name)
	return nil
}

// favorites returns the shopper's favorites, preferring the store's own
// wishlist and falling back to a local list for stores without one.
func favorites(g *Globals) ([]vtex.WishlistItem, string, error) {
	if g.Store.WishlistHash != "" {
		client, err := g.RequireAuth()
		if err != nil {
			return nil, "", err
		}
		email, err := client.AuthenticatedUser()
		if err != nil {
			return nil, "", err
		}
		lists, err := client.Wishlists(email)
		if err != nil {
			return nil, "", err
		}
		var items []vtex.WishlistItem
		name := "Wishlist"
		for _, l := range lists {
			if l.Name != "" {
				name = l.Name
			}
			items = append(items, l.Items...)
		}
		return items, name, nil
	}

	lists, err := g.Config().LoadLists()
	if err != nil {
		return nil, "", errfmt.Wrap(errfmt.ExitConfig, "load lists", err)
	}
	var items []vtex.WishlistItem
	for _, sku := range lists[favoritesList] {
		items = append(items, vtex.WishlistItem{SKU: sku, ProductID: sku})
	}
	return items, favoritesList + " (local)", nil
}
