package cli

// FavCmd is shorthand for operating on the "favorites" list.
type FavCmd struct {
	Show   FavShowCmd   `cmd:"" default:"1" help:"Show favorites."`
	Add    FavAddCmd    `cmd:"" help:"Add a SKU to favorites."`
	Remove FavRemoveCmd `cmd:"" help:"Remove a SKU from favorites."`
	Order  FavOrderCmd  `cmd:"" help:"Add every favorite to the cart."`
}

type FavShowCmd struct{}

func (c *FavShowCmd) Run(g *Globals) error {
	return (&ListShowCmd{Name: favoritesList}).Run(g)
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
