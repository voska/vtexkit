package cli

import (
	"fmt"

	"github.com/voska/vtexkit/cli/errfmt"
	"github.com/voska/vtexkit/cli/outfmt"
	"github.com/voska/vtexkit/money"
	"github.com/voska/vtexkit/vtex"
)

type CartCmd struct {
	Show    CartShowCmd    `cmd:"" default:"withargs" help:"Show the current cart."`
	Add     CartAddCmd     `cmd:"" help:"Add a SKU to the cart."`
	Update  CartUpdateCmd  `cmd:"" help:"Set an item's quantity by index."`
	Remove  CartRemoveCmd  `cmd:"" help:"Remove an item by index."`
	Clear   CartClearCmd   `cmd:"" help:"Remove everything from the cart."`
	Reorder CartReorderCmd `cmd:"" help:"Re-add the items from a previous order."`
}

// resolveCart fetches the active cart for an authenticated client.
func resolveCart(g *Globals) (*vtex.Client, *vtex.OrderForm, error) {
	client, err := g.RequireAuth()
	if err != nil {
		return nil, nil, err
	}
	of, err := client.GetOrderForm(g.OrderFormID(client))
	if err != nil {
		return nil, nil, err
	}
	g.PersistOrderFormID(of.OrderFormID)
	return client, of, nil
}

func printCart(g *Globals, of *vtex.OrderForm) error {
	if g.Formatter().IsJSON() || g.CLI.Plain || g.CLI.Quiet {
		return g.Formatter().Print(of.Items)
	}
	if len(of.Items) == 0 {
		outfmt.Hint("Cart is empty.")
		return nil
	}
	for i, item := range of.Items {
		fmt.Printf("%-4d %-10s %-40s x%-4d %10s\n",
			i, item.ID, truncate(item.Name, 40), item.Quantity,
			item.SellingPrice*money.Centavos(item.Quantity))
	}
	for _, t := range of.Totalizers {
		fmt.Printf("%-57s %10s\n", t.Name, t.Value)
	}
	return nil
}

type CartShowCmd struct{}

func (c *CartShowCmd) Run(g *Globals) error {
	_, of, err := resolveCart(g)
	if err != nil {
		return err
	}
	return printCart(g, of)
}

type CartAddCmd struct {
	SKU    string `arg:"" help:"SKU to add."`
	Qty    int    `help:"Quantity." default:"1"`
	Seller string `help:"Seller id. Discovered from the catalog when omitted."`
	DryRun bool   `short:"n" help:"Show what would be added without changing the cart."`
}

func (c *CartAddCmd) Run(g *Globals) error {
	if err := validateID(c.SKU); err != nil {
		return err
	}
	if c.Qty < 1 {
		return errfmt.Usage("--qty must be at least 1")
	}

	client, of, err := resolveCart(g)
	if err != nil {
		return err
	}

	seller := c.Seller
	if seller == "" {
		seller, err = discoverSeller(client, c.SKU)
		if err != nil {
			return err
		}
	}

	if c.DryRun {
		return g.Formatter().Print(map[string]any{
			"action": "add", "sku": c.SKU, "quantity": c.Qty,
			"seller": seller, "dryRun": true,
		})
	}

	updated, err := client.AddToCart(of.OrderFormID, c.SKU, seller, c.Qty)
	if err != nil {
		return err
	}
	return printCart(g, updated)
}

// discoverSeller finds which seller stocks a SKU. Sellers are per-item, so
// this is a lookup rather than a store-wide constant.
func discoverSeller(client *vtex.Client, sku string) (string, error) {
	results, err := client.Search(sku, 50)
	if err != nil {
		return "", err
	}
	for _, r := range results {
		if r.SKU == sku {
			return r.Seller, nil
		}
	}
	return "", errfmt.NotFound(fmt.Sprintf(
		"SKU %s not found in the catalog — pass --seller to add it anyway", sku))
}

type CartUpdateCmd struct {
	Index  int  `arg:"" help:"Item index from 'cart show'."`
	Qty    int  `help:"New absolute quantity. 0 removes the item." required:""`
	DryRun bool `short:"n" help:"Show the change without applying it."`
}

func (c *CartUpdateCmd) Run(g *Globals) error {
	client, of, err := resolveCart(g)
	if err != nil {
		return err
	}
	if c.Index < 0 || c.Index >= len(of.Items) {
		return errfmt.Usage(fmt.Sprintf(
			"index %d out of range (cart has %d items)", c.Index, len(of.Items)))
	}
	if c.DryRun {
		return g.Formatter().Print(map[string]any{
			"action": "update", "index": c.Index,
			"sku": of.Items[c.Index].ID, "quantity": c.Qty, "dryRun": true,
		})
	}
	updated, err := client.UpdateItemQuantity(of.OrderFormID, c.Index, c.Qty)
	if err != nil {
		return err
	}
	return printCart(g, updated)
}

type CartRemoveCmd struct {
	Index  int  `arg:"" help:"Item index from 'cart show'."`
	DryRun bool `short:"n" help:"Show the change without applying it."`
}

func (c *CartRemoveCmd) Run(g *Globals) error {
	return (&CartUpdateCmd{Index: c.Index, Qty: 0, DryRun: c.DryRun}).Run(g)
}

type CartClearCmd struct {
	DryRun bool `short:"n" help:"Show what would be removed."`
}

func (c *CartClearCmd) Run(g *Globals) error {
	client, of, err := resolveCart(g)
	if err != nil {
		return err
	}
	if c.DryRun {
		return g.Formatter().Print(map[string]any{
			"action": "clear", "items": len(of.Items), "dryRun": true,
		})
	}
	if err := client.RemoveAllItems(of.OrderFormID); err != nil {
		return err
	}
	outfmt.Success("Cart cleared.")
	return nil
}

type CartReorderCmd struct {
	OrderID string `arg:"" optional:"" help:"Order to copy. Defaults to the most recent."`
	DryRun  bool   `short:"n" help:"Show what would be added."`
}

func (c *CartReorderCmd) Run(g *Globals) error {
	client, of, err := resolveCart(g)
	if err != nil {
		return err
	}

	orderID := c.OrderID
	if orderID == "" {
		orders, err := client.ListOrders()
		if err != nil {
			return err
		}
		if len(orders) == 0 {
			return errfmt.Empty()
		}
		orderID = orders[0].OrderID
	}
	if err := validateID(orderID); err != nil {
		return err
	}

	detail, err := client.GetOrder(orderID)
	if err != nil {
		return err
	}
	if len(detail.Items) == 0 {
		return errfmt.Empty()
	}

	if c.DryRun {
		return g.Formatter().Print(map[string]any{
			"action": "reorder", "orderId": orderID,
			"items": detail.Items, "dryRun": true,
		})
	}

	added := 0
	for _, item := range detail.Items {
		sku := item.SKU
		if sku == "" {
			sku = item.ID
		}
		seller, err := discoverSeller(client, sku)
		if err != nil {
			outfmt.Warn("skipping %s (%s): %v", sku, item.Name, err)
			continue
		}
		if _, err := client.AddToCart(of.OrderFormID, sku, seller, item.Quantity); err != nil {
			outfmt.Warn("skipping %s (%s): %v", sku, item.Name, err)
			continue
		}
		added++
	}
	if added == 0 {
		return errfmt.Domain("nothing from that order is currently available")
	}
	outfmt.Success("Re-added %d of %d items from order %s.", added, len(detail.Items), orderID)

	refreshed, err := client.GetOrderForm(of.OrderFormID)
	if err != nil {
		return err
	}
	return printCart(g, refreshed)
}
