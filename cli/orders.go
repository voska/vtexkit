package cli

import (
	"fmt"

	"github.com/voska/vtexkit/cli/errfmt"
)

type OrdersCmd struct {
	OrderID string `arg:"" optional:"" help:"Show one order's items instead of the list."`
}

func (c *OrdersCmd) Run(g *Globals) error {
	client, err := g.RequireAuth()
	if err != nil {
		return err
	}

	if c.OrderID != "" {
		if err := validateID(c.OrderID); err != nil {
			return err
		}
		detail, err := client.GetOrder(c.OrderID)
		if err != nil {
			return err
		}
		if g.Formatter().IsJSON() || g.CLI.Plain || g.CLI.Quiet {
			return g.Formatter().Print(detail.Items)
		}
		for _, item := range detail.Items {
			fmt.Printf("%-10s %-48s x%d\n", item.SKU, truncate(item.Name, 48), item.Quantity)
		}
		return nil
	}

	orders, err := client.ListOrders()
	if err != nil {
		return err
	}
	if len(orders) == 0 {
		return errfmt.Empty()
	}
	if g.Formatter().IsJSON() || g.CLI.Plain || g.CLI.Quiet {
		return g.Formatter().Print(orders)
	}
	for _, o := range orders {
		fmt.Printf("%-18s %-26s %10s  %d items\n",
			o.OrderID, truncate(o.StatusDescription, 26), o.TotalValue, o.TotalItems)
	}
	return nil
}
