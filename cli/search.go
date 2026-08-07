package cli

import (
	"fmt"

	"github.com/voska/vtexkit/cli/errfmt"
	"github.com/voska/vtexkit/vtex"
)

type SearchCmd struct {
	Query string `arg:"" help:"Search terms, in Portuguese."`
	Limit int    `help:"Maximum results (max 50)." default:"20"`
}

func (c *SearchCmd) Run(g *Globals) error {
	results, err := g.Client().Search(c.Query, c.Limit)
	if err != nil {
		return err
	}
	if len(results) == 0 {
		return errfmt.Empty()
	}
	if g.Formatter().IsJSON() || g.CLI.Plain || g.CLI.Quiet {
		return g.Formatter().Print(results)
	}
	printResults(results)
	return nil
}

func printResults(results []vtex.SearchResult) {
	for _, r := range results {
		unit := r.Unit
		if unit == "" {
			unit = "un"
		}
		fmt.Printf("%-10s %-48s %10s  %s\n", r.SKU, truncate(r.Name, 48), r.Price, unit)
	}
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}

type ProductCmd struct {
	SKU string `arg:"" help:"SKU to look up."`
}

// Run finds a SKU by searching for it. VTEX has no public single-SKU
// endpoint that returns pricing, so this filters a search instead.
func (c *ProductCmd) Run(g *Globals) error {
	if err := validateID(c.SKU); err != nil {
		return err
	}
	found, err := lookupSKU(g.Client(), c.SKU)
	if err != nil {
		return err
	}
	{
		r := *found
		{
			if g.Formatter().IsJSON() || g.CLI.Plain || g.CLI.Quiet {
				return g.Formatter().Print(r)
			}
			fmt.Printf("%-12s %s\n", "sku", r.SKU)
			fmt.Printf("%-12s %s\n", "name", r.Name)
			fmt.Printf("%-12s %s\n", "price", r.Price)
			fmt.Printf("%-12s %s\n", "seller", r.Seller)
			fmt.Printf("%-12s %d\n", "available", r.Available)
			return nil
		}
	}
}
