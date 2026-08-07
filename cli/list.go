package cli

import (
	"fmt"

	"github.com/voska/vtexkit/cli/config"
	"github.com/voska/vtexkit/cli/errfmt"
	"github.com/voska/vtexkit/cli/outfmt"
)

type ListCmd struct {
	Show   ListShowCmd   `cmd:"" default:"withargs" help:"Show a list, or all lists."`
	Add    ListAddCmd    `cmd:"" help:"Add a SKU to a list."`
	Remove ListRemoveCmd `cmd:"" help:"Remove a SKU from a list."`
	Order  ListOrderCmd  `cmd:"" help:"Add every SKU in a list to the cart."`
	Delete ListDeleteCmd `cmd:"" help:"Delete a list."`
}

type ListShowCmd struct {
	Name string `arg:"" optional:"" help:"List name. Omit to show all lists."`
}

func (c *ListShowCmd) Run(g *Globals) error {
	lists, err := g.Config().LoadLists()
	if err != nil {
		return errfmt.Wrap(errfmt.ExitConfig, "load lists", err)
	}
	if c.Name != "" {
		skus, ok := lists[c.Name]
		if !ok {
			return errfmt.NotFound(fmt.Sprintf("no list named %q", c.Name))
		}
		if len(skus) == 0 {
			return errfmt.Empty()
		}
		return g.Formatter().Print(skus)
	}
	if len(lists) == 0 {
		return errfmt.Empty()
	}
	return g.Formatter().Print(lists)
}

type ListAddCmd struct {
	Name string `arg:"" help:"List name."`
	SKU  string `arg:"" help:"SKU to add."`
}

func (c *ListAddCmd) Run(g *Globals) error {
	return mutateList(g, c.Name, func(skus map[string][]string) (string, error) {
		if err := validateID(c.SKU); err != nil {
			return "", err
		}
		if !config.Lists(skus).Add(c.Name, c.SKU) {
			return fmt.Sprintf("%s is already in %q.", c.SKU, c.Name), nil
		}
		return fmt.Sprintf("Added %s to %q.", c.SKU, c.Name), nil
	})
}

type ListRemoveCmd struct {
	Name string `arg:"" help:"List name."`
	SKU  string `arg:"" help:"SKU to remove."`
}

func (c *ListRemoveCmd) Run(g *Globals) error {
	return mutateList(g, c.Name, func(skus map[string][]string) (string, error) {
		if !config.Lists(skus).Remove(c.Name, c.SKU) {
			return "", errfmt.NotFound(fmt.Sprintf("%s is not in %q", c.SKU, c.Name))
		}
		return fmt.Sprintf("Removed %s from %q.", c.SKU, c.Name), nil
	})
}

type ListDeleteCmd struct {
	Name string `arg:"" help:"List name."`
}

func (c *ListDeleteCmd) Run(g *Globals) error {
	return mutateList(g, c.Name, func(skus map[string][]string) (string, error) {
		if _, ok := skus[c.Name]; !ok {
			return "", errfmt.NotFound(fmt.Sprintf("no list named %q", c.Name))
		}
		delete(skus, c.Name)
		return fmt.Sprintf("Deleted list %q.", c.Name), nil
	})
}

type ListOrderCmd struct {
	Name   string `arg:"" help:"List name."`
	Qty    int    `help:"Quantity for each SKU." default:"1"`
	DryRun bool   `short:"n" help:"Show what would be added."`
}

func (c *ListOrderCmd) Run(g *Globals) error {
	lists, err := g.Config().LoadLists()
	if err != nil {
		return errfmt.Wrap(errfmt.ExitConfig, "load lists", err)
	}
	skus, ok := lists[c.Name]
	if !ok || len(skus) == 0 {
		return errfmt.Empty()
	}

	client, of, err := resolveCart(g)
	if err != nil {
		return err
	}

	if c.DryRun {
		return g.Formatter().Print(map[string]any{
			"action": "order-list", "list": c.Name,
			"skus": skus, "quantity": c.Qty, "dryRun": true,
		})
	}

	added := 0
	for _, sku := range skus {
		seller, err := discoverSeller(client, sku)
		if err != nil {
			outfmt.Warn("skipping %s: %v", sku, err)
			continue
		}
		if _, err := client.AddToCart(of.OrderFormID, sku, seller, c.Qty); err != nil {
			outfmt.Warn("skipping %s: %v", sku, err)
			continue
		}
		added++
	}
	if added == 0 {
		return errfmt.Domain(fmt.Sprintf("nothing in %q is currently available", c.Name))
	}
	outfmt.Success("Added %d of %d items from %q.", added, len(skus), c.Name)

	refreshed, err := client.GetOrderForm(of.OrderFormID)
	if err != nil {
		return err
	}
	return printCart(g, refreshed)
}

// mutateList loads, applies, and persists a list change in one place so
// every mutation shares the same read-modify-write.
func mutateList(g *Globals, name string, apply func(map[string][]string) (string, error)) error {
	if name == "" {
		return errfmt.Usage("list name required")
	}
	lists, err := g.Config().LoadLists()
	if err != nil {
		return errfmt.Wrap(errfmt.ExitConfig, "load lists", err)
	}
	msg, err := apply(lists)
	if err != nil {
		return err
	}
	if err := g.Config().SaveLists(lists); err != nil {
		return errfmt.Wrap(errfmt.ExitConfig, "save lists", err)
	}
	outfmt.Success("%s", msg)
	return nil
}
