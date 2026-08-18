package cli

import (
	"fmt"

	"github.com/voska/vtexkit/cli/errfmt"
	"github.com/voska/vtexkit/vtex"
)

type OrdersCmd struct {
	OrderID string `arg:"" optional:"" help:"Show one order instead of the list."`
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
			// The whole order, not just its line items: --results-only
			// unwraps back to the items array for callers that want it.
			return g.Formatter().Print(detail)
		}
		printOrderDetail(detail)
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

// printOrderDetail leads with what the order actually is. The line items are
// printed exactly as before, after the state that says whether they are
// coming: an order whose payment never settled is cancelled within minutes.
func printOrderDetail(d *vtex.OrderDetail) {
	status := d.Status
	if d.StatusDescription != "" {
		status = fmt.Sprintf("%s (%s)", d.Status, d.StatusDescription)
	}
	payment := "not authorized — no payment has settled on this order"
	if d.Authorized {
		payment = "authorized"
		if d.TID != "" {
			payment += " · tid " + d.TID
		}
		if d.AuthorizedDate != "" {
			payment += " · " + d.AuthorizedDate
		}
	}

	fmt.Printf("%-10s %s\n", "order", d.OrderID)
	fmt.Printf("%-10s %s\n", "status", status)
	fmt.Printf("%-10s %s\n", "payment", payment)
	fmt.Printf("%-10s %s\n", "total", d.Value)
	if len(d.Items) == 0 {
		return
	}
	fmt.Println()
	for _, item := range d.Items {
		fmt.Printf("%-10s %-48s x%d\n", item.SKU, truncate(item.Name, 48), item.Quantity)
	}
}
