package cli

import "github.com/voska/vtexkit/cli/errfmt"

// FavCmd is shorthand for operating on the "favorites" list.
type FavCmd struct {
	Show   FavShowCmd   `cmd:"" default:"withargs" help:"Show favorites."`
	Add    FavAddCmd    `cmd:"" help:"Add a SKU to favorites."`
	Remove FavRemoveCmd `cmd:"" help:"Remove a SKU from favorites."`
	Order  FavOrderCmd  `cmd:"" help:"Add every favorite to the cart."`
}

type FavShowCmd struct{}

// Run shows favorites. Unlike `list show <name>`, an absent favorites list
// is emptiness rather than a missing resource: the user never named it, so
// exit 3 is the honest code, not 5.
func (c *FavShowCmd) Run(g *Globals) error {
	lists, err := g.Config().LoadLists()
	if err != nil {
		return errfmt.Wrap(errfmt.ExitConfig, "load lists", err)
	}
	skus := lists[favoritesList]
	if len(skus) == 0 {
		return errfmt.Empty()
	}
	return g.Formatter().Print(skus)
}

type FavAddCmd struct {
	SKU string `arg:"" help:"SKU to add."`
}

func (c *FavAddCmd) Run(g *Globals) error {
	return (&ListAddCmd{Name: favoritesList, SKU: c.SKU}).Run(g)
}

type FavRemoveCmd struct {
	SKU string `arg:"" help:"SKU to remove."`
}

func (c *FavRemoveCmd) Run(g *Globals) error {
	return (&ListRemoveCmd{Name: favoritesList, SKU: c.SKU}).Run(g)
}

type FavOrderCmd struct {
	Qty    int  `help:"Quantity for each SKU." default:"1"`
	DryRun bool `short:"n" help:"Show what would be added."`
}

func (c *FavOrderCmd) Run(g *Globals) error {
	return (&ListOrderCmd{Name: favoritesList, Qty: c.Qty, DryRun: c.DryRun}).Run(g)
}
