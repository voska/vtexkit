package cli

import (
	"fmt"

	"github.com/voska/vtexkit/cli/errfmt"
	"github.com/voska/vtexkit/cli/outfmt"
	"github.com/voska/vtexkit/vtex"
)

// FavCmd manages the store's own wishlist — what the heart icons on the
// website save.
//
// There is deliberately no bulk-order subcommand. A wishlist is things the
// shopper likes, not things they intend to buy now; "add all of it to the
// cart" is never what anyone means. Curated lists meant to be ordered
// wholesale live under `list`.
type FavCmd struct {
	Show   FavShowCmd   `cmd:"" default:"withargs" help:"Show favorites."`
	Add    FavAddCmd    `cmd:"" help:"Save a product to favorites."`
	Remove FavRemoveCmd `cmd:"" help:"Remove a product from favorites."`
}

// favoritesList is the local list used by stores whose wishlist API is not
// reachable. It predates wishlist support and must keep working: zonasul
// v0.5.0 shipped `fav` backed by it.
const favoritesList = "favorites"

// wishlistSession resolves the logged-in shopper for wishlist operations.
func wishlistSession(g *Globals) (*vtex.Client, string, error) {
	client, err := g.RequireAuth()
	if err != nil {
		return nil, "", err
	}
	email, err := client.AuthenticatedUser()
	if err != nil {
		return nil, "", err
	}
	return client, email, nil
}

// favorites returns the shopper's saved items and the list's name, from the
// store's own wishlist when it is reachable and from a local list otherwise.
func favorites(g *Globals) (*vtex.Client, string, []vtex.WishlistItem, string, error) {
	if !g.Store.Wishlist.CanRead() {
		items, err := localFavorites(g)
		return nil, "", items, favoritesList + " (local)", err
	}
	client, email, err := wishlistSession(g)
	if err != nil {
		return nil, "", nil, "", err
	}
	lists, err := client.Wishlists(email)
	if err != nil {
		return nil, "", nil, "", err
	}
	var items []vtex.WishlistItem
	name := vtex.DefaultWishlistName
	for _, l := range lists {
		if l.Name != "" {
			name = l.Name
		}
		items = append(items, l.Items...)
	}
	return client, email, items, name, nil
}

// localFavorites reads the on-disk favorites list.
func localFavorites(g *Globals) ([]vtex.WishlistItem, error) {
	lists, err := g.Config().LoadLists()
	if err != nil {
		return nil, errfmt.Wrap(errfmt.ExitConfig, "load lists", err)
	}
	var items []vtex.WishlistItem
	for _, sku := range lists[favoritesList] {
		items = append(items, vtex.WishlistItem{SKU: sku, ProductID: sku})
	}
	return items, nil
}

type FavShowCmd struct{}

func (c *FavShowCmd) Run(g *Globals) error {
	_, _, items, name, err := favorites(g)
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
	outfmt.Hint("Add one to the cart with: %s cart add <sku>", g.Store.Name)
	return nil
}

type FavAddCmd struct {
	SKU    string `arg:"" help:"SKU to save."`
	DryRun bool   `short:"n" help:"Show what would be saved."`
}

func (c *FavAddCmd) Run(g *Globals) error {
	if err := validateID(c.SKU); err != nil {
		return err
	}
	if !g.Store.Wishlist.CanWrite() {
		return (&ListAddCmd{Name: favoritesList, SKU: c.SKU}).Run(g)
	}
	client, email, items, listName, err := favorites(g)
	if err != nil {
		return err
	}
	for _, it := range items {
		if it.SKU == c.SKU || it.ProductID == c.SKU {
			outfmt.Hint("%s is already saved: %s", c.SKU, it.Title)
			return nil
		}
	}

	// The API stores productId, sku, and title, and productId differs from
	// sku for products with variants — so look the product up rather than
	// assuming the SKU is both.
	found, err := lookupSKU(client, c.SKU)
	if err != nil {
		return err
	}
	item := vtex.WishlistItem{ProductID: found.ProductID, SKU: found.SKU, Title: found.Name}

	if c.DryRun {
		return g.Formatter().Print(map[string]any{
			"action": "fav-add", "item": item, "list": listName, "dryRun": true,
		})
	}
	if err := client.AddToWishlist(email, listName, item); err != nil {
		return err
	}
	outfmt.Success("Saved %s — %s", item.SKU, item.Title)
	return nil
}

type FavRemoveCmd struct {
	SKU    string `arg:"" help:"SKU to remove."`
	DryRun bool   `short:"n" help:"Show what would be removed."`
}

func (c *FavRemoveCmd) Run(g *Globals) error {
	if err := validateID(c.SKU); err != nil {
		return err
	}
	if !g.Store.Wishlist.CanWrite() {
		return (&ListRemoveCmd{Name: favoritesList, SKU: c.SKU}).Run(g)
	}
	client, email, items, listName, err := favorites(g)
	if err != nil {
		return err
	}

	// RemoveFromList takes the wishlist item's own id, not the SKU, so the
	// list has to be read first. Removing by SKU directly would delete
	// whichever item happened to sit at that numeric position.
	for _, it := range items {
		if it.SKU == c.SKU || it.ProductID == c.SKU {
			if c.DryRun {
				return g.Formatter().Print(map[string]any{
					"action": "fav-remove", "item": it, "list": listName, "dryRun": true,
				})
			}
			removed, err := client.RemoveFromWishlist(email, listName, it.ID)
			if err != nil {
				return err
			}
			if !removed {
				return errfmt.Domain(fmt.Sprintf("store declined to remove %s", c.SKU))
			}
			outfmt.Success("Removed %s — %s", it.SKU, it.Title)
			return nil
		}
	}
	return errfmt.NotFound(fmt.Sprintf("%s is not in your wishlist", c.SKU))
}

// lookupSKU finds a catalog entry by SKU or product id.
func lookupSKU(client *vtex.Client, id string) (*vtex.SearchResult, error) {
	results, err := client.Search(id, 50)
	if err != nil {
		return nil, err
	}
	for _, r := range results {
		if r.SKU == id || r.ProductID == id {
			return &r, nil
		}
	}
	return nil, errfmt.NotFound(fmt.Sprintf("SKU %s not found in the catalog", id))
}
