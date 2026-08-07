package vtex_test

import (
	"net/http"
	"testing"

	"github.com/voska/vtexkit/money"
)

// Neither zonasul nor frescatto had any test for order history; these are
// its first.
func TestListOrdersParsesEnvelope(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/oms/user/orders" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"list":[
			{"orderId":"1234-01","status":"ready-for-handling",
			 "statusDescription":"Pronto para manuseio",
			 "totalValue":15372,"totalItems":3}]}`))
	})
	got, err := c.ListOrders()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].OrderID != "1234-01" {
		t.Fatalf("orders = %+v", got)
	}
	if got[0].TotalValue != money.Centavos(15372) {
		t.Errorf("TotalValue = %d, want centavos", int64(got[0].TotalValue))
	}
	if got[0].TotalItems != 3 {
		t.Errorf("TotalItems = %d", got[0].TotalItems)
	}
}

func TestListOrdersEmptyHistory(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"list":[]}`))
	})
	got, err := c.ListOrders()
	if err != nil {
		t.Fatal(err)
	}
	// A new account has no orders; that is exit code 3, not an error.
	if len(got) != 0 {
		t.Errorf("got %d orders", len(got))
	}
}

func TestGetOrderParsesItems(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/oms/user/orders/1234-01" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"orderId":"1234-01","status":"invoiced","items":[
			{"id":"134","sellerSku":"134","name":"Kit Peixe","quantity":2,"price":15372}]}`))
	})
	got, err := c.GetOrder("1234-01")
	if err != nil {
		t.Fatal(err)
	}
	if got.OrderID != "1234-01" || got.Status != "invoiced" {
		t.Errorf("order = %+v", got)
	}
	if len(got.Items) != 1 || got.Items[0].Quantity != 2 || got.Items[0].SKU != "134" {
		t.Errorf("items = %+v", got.Items)
	}
}
