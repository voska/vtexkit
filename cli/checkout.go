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
	Run      CheckoutRunCmd      `cmd:"" default:"1" help:"Preview or place an order."`
}

type CheckoutPaymentsCmd struct{}

func (c *CheckoutPaymentsCmd) Run(g *Globals) error {
	client, of, err := resolveCart(g)
	if err != nil {
		return err
	}
	if len(of.PaymentSystems) == 0 {
		return errfmt.Domain("store reported no payment methods — add an item to the cart first")
	}
	_ = client
	if g.Formatter().IsJSON() || g.CLI.Plain || g.CLI.Quiet {
		return g.Formatter().Print(of.PaymentSystems)
	}
	for _, ps := range of.PaymentSystems {
		fmt.Printf("%-6d %-24s %s\n", ps.ID, ps.Name, ps.GroupName)
	}
	return nil
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
