package vtex

import (
	"encoding/json"
	"fmt"

	"github.com/voska/vtexkit/money"
)

type OrderFormItem struct {
	ID           string         `json:"id"`
	ProductID    string         `json:"productId"`
	Name         string         `json:"name"`
	Quantity     int            `json:"quantity"`
	Price        money.Centavos `json:"price"`
	SellingPrice money.Centavos `json:"sellingPrice"`
	Seller       string         `json:"seller"`
	Unit         string         `json:"measurementUnit"`
	UnitMult     float64        `json:"unitMultiplier"`
}

type Totalizer struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Value money.Centavos `json:"value"`
}

// PaymentSystem is one payment method the store accepts. Discovered from the
// orderForm rather than hardcoded, because the set differs per store.
type PaymentSystem struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	GroupName string `json:"groupName"`
}

type OrderForm struct {
	OrderFormID    string          `json:"orderFormId"`
	Items          []OrderFormItem `json:"items"`
	Totalizers     []Totalizer     `json:"totalizers"`
	PaymentSystems []PaymentSystem `json:"-"`
	Value          money.Centavos  `json:"value"`
	LoggedIn       bool            `json:"loggedIn"`
	// AddressCount is how many delivery addresses this cart can see. VTEX
	// snapshots account data into an order form when it is created, so a
	// cart minted before the account had an address reports zero here
	// forever — it can never complete a checkout.
	AddressCount int `json:"addressCount"`
}

// Checkoutable reports whether this cart can reach a completed order. A
// cart with no visible address cannot, regardless of what the account has.
func (o *OrderForm) Checkoutable() bool { return o.AddressCount > 0 }

// orderFormWire is the wire shape. PaymentSystems is nested under
// paymentData on the wire but promoted to the top level for callers.
type orderFormWire struct {
	OrderFormID string          `json:"orderFormId"`
	Items       []OrderFormItem `json:"items"`
	Totalizers  []Totalizer     `json:"totalizers"`
	Value       money.Centavos  `json:"value"`
	LoggedIn    bool            `json:"loggedIn"`
	PaymentData struct {
		PaymentSystems []PaymentSystem `json:"paymentSystems"`
	} `json:"paymentData"`
	ShippingData struct {
		AvailableAddresses []map[string]any `json:"availableAddresses"`
		SelectedAddresses  []map[string]any `json:"selectedAddresses"`
	} `json:"shippingData"`
}

func (w orderFormWire) toOrderForm() *OrderForm {
	count := len(w.ShippingData.AvailableAddresses)
	if count == 0 {
		count = len(w.ShippingData.SelectedAddresses)
	}
	return &OrderForm{
		OrderFormID:    w.OrderFormID,
		Items:          w.Items,
		Totalizers:     w.Totalizers,
		Value:          w.Value,
		LoggedIn:       w.LoggedIn,
		AddressCount:   count,
		PaymentSystems: w.PaymentData.PaymentSystems,
	}
}

// UsableCart returns a cart that can actually complete a checkout.
//
// VTEX snapshots account data into an order form at creation time and never
// refreshes it — refreshOutdatedData does not help. A cart minted before the
// account had a profile or address is therefore permanently unusable, and
// because the CLI persists a cart id across invocations, a user who tried
// the CLI before completing their profile would be stuck forever with no
// way out but deleting the config by hand.
//
// When the persisted cart cannot check out, this mints a fresh one and
// carries the items across, so nothing the user added is lost.
func (c *Client) UsableCart(persistedID string) (*OrderForm, bool, error) {
	current, err := c.GetOrderForm(persistedID)
	if err != nil {
		return nil, false, err
	}
	if persistedID == "" || current.Checkoutable() || c.authToken == "" {
		return current, false, nil
	}

	fresh, err := c.NewOrderForm()
	if err != nil {
		return current, false, nil //nolint:nilerr // keep the old cart if minting fails
	}
	if !fresh.Checkoutable() {
		// The account genuinely has no address; the old cart is no worse.
		return current, false, nil
	}

	for _, item := range current.Items {
		if _, addErr := c.AddToCart(fresh.OrderFormID, item.ID, item.Seller, item.Quantity); addErr != nil {
			return nil, false, fmt.Errorf("migrating cart to a usable order form: %w", addErr)
		}
	}
	migrated, err := c.GetOrderForm(fresh.OrderFormID)
	if err != nil {
		return nil, false, err
	}
	return migrated, true, nil
}

// ItemsTotal returns the Items totalizer, which excludes shipping. Checkout
// minimums are assessed against this, not the order total.
func (o *OrderForm) ItemsTotal() money.Centavos {
	for _, t := range o.Totalizers {
		if t.ID == "Items" {
			return t.Value
		}
	}
	return 0
}

// Total sums every totalizer: items, discounts, and shipping.
func (o *OrderForm) Total() money.Centavos {
	var sum money.Centavos
	for _, t := range o.Totalizers {
		sum += t.Value
	}
	return sum
}

// NewOrderForm mints a genuinely new cart.
//
// It must use its own cookie jar: VTEX pins a client to its current cart via
// a checkout cookie, so calling GET /orderForm on a client that has already
// touched a cart hands back that same cart. Without a clean jar this
// silently returns the cart you were trying to escape.
func (c *Client) NewOrderForm() (*OrderForm, error) {
	clean := New(c.store, c.authToken)
	body, err := clean.PostJSON("/api/checkout/pub/orderForm", map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("new order form: %w", err)
	}
	var w orderFormWire
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, fmt.Errorf("new order form parse: %w", err)
	}
	return w.toOrderForm(), nil
}

func (c *Client) GetOrderForm(orderFormID string) (*OrderForm, error) {
	path := "/api/checkout/pub/orderForm"
	if orderFormID != "" {
		path = fmt.Sprintf("/api/checkout/pub/orderForm/%s", orderFormID)
	}
	body, err := c.Get(path)
	if err != nil {
		return nil, fmt.Errorf("get order form: %w", err)
	}
	var w orderFormWire
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, fmt.Errorf("get order form parse: %w", err)
	}
	return w.toOrderForm(), nil
}

// AddToCart adds a SKU. The seller comes from the caller — normally straight
// off a SearchResult — because it is not a store-wide constant.
func (c *Client) AddToCart(orderFormID, skuID, seller string, quantity int) (*OrderForm, error) {
	if orderFormID == "" {
		of, err := c.GetOrderForm("")
		if err != nil {
			return nil, fmt.Errorf("add to cart: %w", err)
		}
		orderFormID = of.OrderFormID
	}
	payload := map[string]any{
		"orderItems": []map[string]any{
			{"id": skuID, "quantity": quantity, "seller": seller},
		},
	}
	path := fmt.Sprintf("/api/checkout/pub/orderForm/%s/items", orderFormID)
	body, err := c.PostJSON(path, payload)
	if err != nil {
		return nil, fmt.Errorf("add to cart: %w", err)
	}
	var w orderFormWire
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, fmt.Errorf("add to cart parse: %w", err)
	}
	return w.toOrderForm(), nil
}

// UpdateItemQuantity sets an absolute quantity, not a delta. Setting 0
// removes the item.
func (c *Client) UpdateItemQuantity(orderFormID string, index, quantity int) (*OrderForm, error) {
	payload := map[string]any{
		"orderItems": []map[string]any{
			{"index": index, "quantity": quantity},
		},
	}
	path := fmt.Sprintf("/api/checkout/pub/orderForm/%s/items/update", orderFormID)
	body, err := c.PostJSON(path, payload)
	if err != nil {
		return nil, fmt.Errorf("update item: %w", err)
	}
	var w orderFormWire
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, fmt.Errorf("update item parse: %w", err)
	}
	return w.toOrderForm(), nil
}

func (c *Client) RemoveAllItems(orderFormID string) error {
	path := fmt.Sprintf("/api/checkout/pub/orderForm/%s/items/removeAll", orderFormID)
	if _, err := c.PostJSON(path, map[string]any{}); err != nil {
		return fmt.Errorf("remove all items: %w", err)
	}
	return nil
}
