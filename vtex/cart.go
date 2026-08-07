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
}

// orderFormWire is the wire shape. PaymentSystems is nested under
// paymentData on the wire but promoted to the top level for callers.
type orderFormWire struct {
	OrderFormID string          `json:"orderFormId"`
	Items       []OrderFormItem `json:"items"`
	Totalizers  []Totalizer     `json:"totalizers"`
	Value       money.Centavos  `json:"value"`
	PaymentData struct {
		PaymentSystems []PaymentSystem `json:"paymentSystems"`
	} `json:"paymentData"`
}

func (w orderFormWire) toOrderForm() *OrderForm {
	return &OrderForm{
		OrderFormID:    w.OrderFormID,
		Items:          w.Items,
		Totalizers:     w.Totalizers,
		Value:          w.Value,
		PaymentSystems: w.PaymentData.PaymentSystems,
	}
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
