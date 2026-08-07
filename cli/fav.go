package cli

import (
	"fmt"

	"github.com/voska/vtexkit/cli/errfmt"
	"github.com/voska/vtexkit/cli/outfmt"
	"github.com/voska/vtexkit/vtex"
)

// FavCmd reads the store's own wishlist — the list the heart icons on the
// website write to — when the store supports it. Stores that do not fall
// back to a locally stored list of the same name.
type FavCmd struct {
	Show   FavShowCmd   `cmd:"" default:"withargs" help:"Show favorites."`
	Order  FavOrderCmd  `cmd:"" help:"Add every favorite to the cart."`
	Add    FavAddCmd    `cmd:"" help:"Add a SKU to the local favorites list."`
	Remove FavRemoveCmd `cmd:"" help:"Remove a SKU from the local favorites list."`
}

// favorites returns the shopper's favorites, preferring the store's own
// wishlist and falling back to the local list.
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

type FavShowCmd struct{}

func (c *FavShowCmd) Run(g *Globals) error {
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
	return nil
}

type FavOrderCmd struct {
	Qty    int  `help:"Quantity for each item." default:"1"`
	DryRun bool `short:"n" help:"Show what would be added."`
}

func (c *FavOrderCmd) Run(g *Globals) error {
	items, _, err := favorites(g)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return errfmt.Empty()
	}
	if c.DryRun {
		return g.Formatter().Print(map[string]any{
			"action": "order-favorites", "items": items,
			"quantity": c.Qty, "dryRun": true,
		})
	}

	client, of, err := resolveCart(g)
	if err != nil {
		return err
	}
	added := 0
	for _, it := range items {
		// The wishlist carries both ids and they differ for products with
		// variants; the cart needs the SKU.
		sku := it.SKU
		if sku == "" {
			sku = it.ProductID
		}
		seller, err := discoverSeller(client, sku)
		if err != nil {
			outfmt.Warn("skipping %s (%s): %v", sku, it.Title, err)
			continue
		}
		if _, err := client.AddToCart(of.OrderFormID, sku, seller, c.Qty); err != nil {
			outfmt.Warn("skipping %s (%s): %v", sku, it.Title, err)
			continue
		}
		added++
	}
	if added == 0 {
		return errfmt.Domain("nothing in your favorites is currently available")
	}
	outfmt.Success("Added %d of %d favorites to the cart.", added, len(items))

	refreshed, err := client.GetOrderForm(of.OrderFormID)
	if err != nil {
		return err
	}
	return printCart(g, refreshed)
}

// FavAddCmd and FavRemoveCmd operate on the local list. Writing to the
// store's wishlist needs its addToList persisted-query hash, which has not
// been captured yet.
type FavAddCmd struct {
	SKU string `arg:"" help:"SKU to add."`
}

func (c *FavAddCmd) Run(g *Globals) error {
	if g.Store.WishlistHash != "" {
		outfmt.Hint("Writes go to a local list; %s shows the store's own wishlist.", g.Store.Name+" fav")
	}
	return (&ListAddCmd{Name: favoritesList, SKU: c.SKU}).Run(g)
}

type FavRemoveCmd struct {
	SKU string `arg:"" help:"SKU to remove."`
}

func (c *FavRemoveCmd) Run(g *Globals) error {
	return (&ListRemoveCmd{Name: favoritesList, SKU: c.SKU}).Run(g)
}
