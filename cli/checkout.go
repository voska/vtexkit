package cli

import (
	"fmt"

	"github.com/voska/vtexkit/cli/errfmt"
	"github.com/voska/vtexkit/cli/outfmt"
	"github.com/voska/vtexkit/money"
	"github.com/voska/vtexkit/store"
	"github.com/voska/vtexkit/vtex"
)

type CheckoutCmd struct {
	Payments CheckoutPaymentsCmd `cmd:"" help:"List the payment methods this store accepts."`
	Run      CheckoutRunCmd      `cmd:"" default:"withargs" help:"Preview or place an order."`
}

type CheckoutPaymentsCmd struct {
	CEP string `help:"Postal code for the fallback simulation. Defaults to the configured address."`
	SKU string `help:"SKU for the fallback simulation."`
}

// Run lists the payment methods the store accepts and any cards saved on
// the account.
//
// VTEX only populates paymentSystems once a cart has items, so an empty
// cart falls back to a shipping simulation, which returns the same list
// without mutating anything.
func (c *CheckoutPaymentsCmd) Run(g *Globals) error {
	systems, savedCards, err := c.gather(g)
	if err != nil {
		return err
	}
	if len(systems) == 0 && len(savedCards) == 0 {
		return errfmt.Domain("store reported no payment methods")
	}

	if g.Formatter().IsJSON() || g.CLI.Plain || g.CLI.Quiet {
		return g.Formatter().Print(map[string]any{
			"accepted":   systems,
			"savedCards": savedCards,
		})
	}
	fmt.Println("Accepted by this store:")
	for _, ps := range systems {
		fmt.Printf("  %-6d %-24s %s\n", ps.ID, ps.Name, ps.GroupName)
	}
	fmt.Println()
	if len(savedCards) == 0 {
		outfmt.Hint("No cards saved on this account.")
		return nil
	}
	fmt.Println("Saved on your account:")
	for _, card := range savedCards {
		fmt.Printf("  %-24s %s\n", card.PaymentSystemName, card.CardNumber)
	}
	return nil
}

func (c *CheckoutPaymentsCmd) gather(g *Globals) ([]vtex.PaymentSystem, []vtex.SavedCard, error) {
	// Authenticated first: only a real order form carries saved cards, and
	// only once it has items — VTEX computes payment options from a value.
	if client, of, err := resolveCart(g); err == nil {
		if len(of.Items) > 0 {
			cards, _ := client.GetSavedCards(of.OrderFormID)
			return of.PaymentSystems, cards, nil
		}
		outfmt.Hint("Cart is empty, so saved cards cannot be listed — VTEX only exposes them once a cart has items.")
	}

	// Empty cart or not logged in: simulate instead of mutating the cart.
	systems, err := c.simulateSystems(g)
	if err != nil {
		return nil, nil, err
	}
	return systems, nil, nil
}

func (c *CheckoutPaymentsCmd) simulateSystems(g *Globals) ([]vtex.PaymentSystem, error) {
	cep := c.CEP
	if cep == "" {
		if cfg, err := g.Config().Load(); err == nil {
			cep = cfg.CEP
		}
	}
	if cep == "" {
		return nil, errfmt.Usage(
			"cart is empty, so payment methods need a simulation — pass --cep and --sku")
	}

	client := g.Client()
	sku := c.SKU
	if sku == "" {
		// Any purchasable SKU works; the list is store-wide.
		results, err := client.Search("a", 1)
		if err != nil || len(results) == 0 {
			return nil, errfmt.Usage(
				"cart is empty, so payment methods need a simulation — pass --sku")
		}
		sku = results[0].SKU
	}
	if err := validateID(sku); err != nil {
		return nil, err
	}
	seller, err := discoverSeller(client, sku)
	if err != nil {
		return nil, err
	}
	sim, err := client.Simulate([]vtex.SimulationItemRequest{
		{ID: sku, Quantity: 1, Seller: seller},
	}, cep)
	if err != nil {
		return nil, err
	}
	return sim.PaymentSystems, nil
}

type CheckoutRunCmd struct {
	Window  int    `help:"Delivery window index from 'delivery windows'." default:"-1"`
	Payment string `help:"Payment method name, e.g. pix. Run 'checkout payments' to list." default:"pix"`
	CVV     string `help:"Card security code, for saved-card payment."`
	DryRun  bool   `short:"n" help:"Print the priced order as JSON and stop."`
	Confirm bool   `help:"Actually place the order. Required safety gate."`
}

func (c *CheckoutRunCmd) Run(g *Globals) error {
	client, of, err := resolveCart(g)
	if err != nil {
		return err
	}
	if len(of.Items) == 0 {
		return errfmt.New(errfmt.ExitEmpty, "cart is empty — add items first")
	}

	// Store minimums are assessed on items, before discounts and shipping.
	if min := g.Store.MinOrder; min > 0 && of.ItemsTotal() < min {
		return errfmt.Domain(fmt.Sprintf(
			"minimum order %s, current total %s", min, of.ItemsTotal()))
	}

	if err := client.SetAddress(of.OrderFormID, len(of.Items)); err != nil {
		return err
	}

	if c.Window >= 0 {
		windows, err := client.GetDeliveryWindows(of.OrderFormID)
		if err != nil {
			return err
		}
		if c.Window >= len(windows) {
			return errfmt.Usage(fmt.Sprintf(
				"window %d out of range (0-%d) — run: %s delivery windows",
				c.Window, len(windows)-1, g.Store.Name))
		}
		if err := client.SetShippingWindow(of.OrderFormID, windows[c.Window], len(of.Items)); err != nil {
			return err
		}
	}

	// Re-read: the address and window change shipping totals.
	of, err = client.GetOrderForm(of.OrderFormID)
	if err != nil {
		return err
	}
	total := of.Total()

	useCard := c.CVV != ""
	var card vtex.SavedCard
	if useCard {
		cards, err := client.GetSavedCards(of.OrderFormID)
		if err != nil {
			return err
		}
		if len(cards) == 0 {
			return errfmt.Domain("no saved card on this account — add one on the website first")
		}
		card = cards[0]
		if err := client.SetPaymentWithSavedCard(of.OrderFormID, card, total); err != nil {
			return err
		}
	} else {
		psID, err := client.ResolvePaymentSystem(of, c.Payment)
		if err != nil {
			return err
		}
		if err := client.SetPayment(of.OrderFormID, psID, total); err != nil {
			return err
		}
	}

	if !c.Confirm || c.DryRun {
		return preview(g, of, c.Payment, useCard, card, total)
	}

	tx, err := client.PlaceOrder(of.OrderFormID, total)
	if err != nil {
		return err
	}

	if useCard {
		outfmt.Hint("Processing card payment with %s %s...", card.PaymentSystemName, card.CardNumber)
		if err := client.PayWithSavedCard(tx, card, c.CVV, total); err != nil {
			return err
		}
		if g.Store.Quirks.Has(store.GatewayCallback) {
			if err := client.GatewayCallback(tx.OrderGroup); err != nil {
				outfmt.Warn("gateway callback: %v", err)
			}
		}
	}

	if g.Formatter().IsJSON() {
		return g.Formatter().Print(map[string]any{
			"orderId": tx.OrderGroup, "status": "placed", "total": total,
		})
	}
	outfmt.Success("Order placed. ID: %s", tx.OrderGroup)
	return nil
}

func preview(g *Globals, of *vtex.OrderForm, payment string, useCard bool, card vtex.SavedCard, total money.Centavos) error {
	method := payment
	if useCard {
		method = fmt.Sprintf("%s %s", card.PaymentSystemName, card.CardNumber)
	}
	if g.Formatter().IsJSON() {
		return g.Formatter().Print(map[string]any{
			"orderFormId": of.OrderFormID,
			"items":       of.Items,
			"totalizers":  of.Totalizers,
			"total":       total,
			"payment":     method,
			"placed":      false,
			"message":     "pass --confirm to place this order",
		})
	}
	for i, item := range of.Items {
		fmt.Printf("%-4d %-45s x%-4d %10s\n",
			i, truncate(item.Name, 45), item.Quantity,
			item.SellingPrice*money.Centavos(item.Quantity))
	}
	for _, t := range of.Totalizers {
		fmt.Printf("%-56s %10s\n", t.Name, t.Value)
	}
	fmt.Printf("%-56s %10s\n", "TOTAL", total)
	fmt.Printf("%-56s %10s\n", "payment", method)
	outfmt.Warn("Nothing was ordered. Pass --confirm to place this order.")
	return nil
}
