package vtex_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/voska/vtexkit/money"
	"github.com/voska/vtexkit/vtex"
)

func TestAddToCartSendsCallerSuppliedSeller(t *testing.T) {
	var got struct {
		OrderItems []struct {
			ID       string `json:"id"`
			Quantity int    `json:"quantity"`
			Seller   string `json:"seller"`
		} `json:"orderItems"`
	}
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/items") {
			t.Errorf("path = %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"orderFormId":"OF","items":[],"totalizers":[]}`))
	})

	if _, err := c.AddToCart("OF", "134", "1", 2); err != nil {
		t.Fatal(err)
	}
	if len(got.OrderItems) != 1 {
		t.Fatalf("orderItems = %+v", got.OrderItems)
	}
	item := got.OrderItems[0]
	// The seller must be whatever search reported, not a package constant.
	if item.Seller != "1" || item.ID != "134" || item.Quantity != 2 {
		t.Errorf("item = %+v", item)
	}
}

func TestAddToCartCreatesOrderFormWhenNoneGiven(t *testing.T) {
	var createdCart bool
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/checkout/pub/orderForm" {
			createdCart = true
			_, _ = w.Write([]byte(`{"orderFormId":"NEW","items":[],"totalizers":[]}`))
			return
		}
		if !strings.Contains(r.URL.Path, "/orderForm/NEW/items") {
			t.Errorf("items posted to %s, want the newly created cart", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"orderFormId":"NEW","items":[],"totalizers":[]}`))
	})

	if _, err := c.AddToCart("", "134", "1", 1); err != nil {
		t.Fatal(err)
	}
	if !createdCart {
		t.Error("an empty orderFormID must mint a new cart")
	}
}

func TestOrderFormPromotesPaymentSystems(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"orderFormId":"OF","items":[],"totalizers":[],
			"paymentData":{"paymentSystems":[
				{"id":125,"name":"Pix","groupName":"instantPaymentPaymentGroup"},
				{"id":2,"name":"Visa","groupName":"creditCardPaymentGroup"}]}}`))
	})
	of, err := c.GetOrderForm("OF")
	if err != nil {
		t.Fatal(err)
	}
	// Nested under paymentData on the wire, promoted for callers.
	if len(of.PaymentSystems) != 2 {
		t.Fatalf("payment systems = %+v", of.PaymentSystems)
	}
	if of.PaymentSystems[0].ID != 125 || of.PaymentSystems[0].Name != "Pix" {
		t.Errorf("first system = %+v", of.PaymentSystems[0])
	}
}

func TestOrderFormTotals(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		// Recorded live from Frescatto: items 17080, discount -1708.
		_, _ = w.Write([]byte(`{"orderFormId":"OF","items":[],"totalizers":[
			{"id":"Items","name":"Total dos Itens","value":17080},
			{"id":"Discounts","name":"Total dos Descontos","value":-1708}]}`))
	})
	of, err := c.GetOrderForm("OF")
	if err != nil {
		t.Fatal(err)
	}
	// Minimums are assessed on items, before discounts and shipping.
	if of.ItemsTotal() != money.Centavos(17080) {
		t.Errorf("ItemsTotal = %d, want 17080", int64(of.ItemsTotal()))
	}
	if of.Total() != money.Centavos(15372) {
		t.Errorf("Total = %d, want 15372", int64(of.Total()))
	}
}

func TestItemsTotalIsZeroWhenAbsent(t *testing.T) {
	of := &vtex.OrderForm{}
	if of.ItemsTotal() != 0 || of.Total() != 0 {
		t.Error("an empty order form must total zero, not panic")
	}
}

func TestUpdateItemQuantitySetsAbsoluteValue(t *testing.T) {
	var got struct {
		OrderItems []struct {
			Index    int `json:"index"`
			Quantity int `json:"quantity"`
		} `json:"orderItems"`
	}
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/items/update") {
			t.Errorf("path = %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"orderFormId":"OF","items":[],"totalizers":[]}`))
	})

	if _, err := c.UpdateItemQuantity("OF", 2, 6); err != nil {
		t.Fatal(err)
	}
	// Absolute, not a delta — 6 means six, not six more.
	if got.OrderItems[0].Index != 2 || got.OrderItems[0].Quantity != 6 {
		t.Errorf("orderItems[0] = %+v", got.OrderItems[0])
	}
}

func TestRemoveAllItems(t *testing.T) {
	var path string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(`{"orderFormId":"OF","items":[],"totalizers":[]}`))
	})
	if err := c.RemoveAllItems("OF"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, "/items/removeAll") {
		t.Errorf("path = %s", path)
	}
}

func TestOrderFormItemPricesAreCentavos(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"orderFormId":"OF","totalizers":[],"items":[
			{"id":"134","name":"Kit","quantity":1,"price":17080,
			 "sellingPrice":15372,"seller":"1","measurementUnit":"un","unitMultiplier":1}]}`))
	})
	of, err := c.GetOrderForm("OF")
	if err != nil {
		t.Fatal(err)
	}
	// The orderForm already speaks centavos; no conversion should occur.
	if of.Items[0].SellingPrice != money.Centavos(15372) {
		t.Errorf("SellingPrice = %d, want 15372", int64(of.Items[0].SellingPrice))
	}
	if of.Items[0].Seller != "1" {
		t.Errorf("Seller = %q", of.Items[0].Seller)
	}
}
