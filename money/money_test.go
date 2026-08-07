package money_test

import (
	"encoding/json"
	"testing"

	"github.com/voska/vtexkit/money"
)

func TestReaisRoundsHalfAwayFromZero(t *testing.T) {
	tests := []struct {
		name string
		in   float64
		want money.Centavos
	}{
		{"frescatto kit price", 153.72, 15372},
		{"zonasul item price", 8.79, 879},
		{"float noise must not truncate", 0.1 + 0.2, 30},
		{"whole reais", 100, 10000},
		{"zero", 0, 0},
		{"negative discount", -17.08, -1708},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := money.Reais(tt.in); got != tt.want {
				t.Errorf("Reais(%v) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestStringFormatsPtBR(t *testing.T) {
	tests := []struct {
		in   money.Centavos
		want string
	}{
		{15372, "R$153,72"},
		{5, "R$0,05"},
		{0, "R$0,00"},
		{100000, "R$1.000,00"},
		{123456789, "R$1.234.567,89"},
		{-1708, "-R$17,08"},
	}
	for _, tt := range tests {
		if got := tt.in.String(); got != tt.want {
			t.Errorf("Centavos(%d).String() = %q, want %q", int64(tt.in), got, tt.want)
		}
	}
}

func TestReaisRoundTrip(t *testing.T) {
	if got := money.Centavos(15372).Reais(); got != 153.72 {
		t.Errorf("Reais() = %v, want 153.72", got)
	}
}

func TestJSONMarshalsAsInteger(t *testing.T) {
	b, err := json.Marshal(struct {
		Price money.Centavos `json:"price"`
	}{15372})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"price":15372}` {
		t.Errorf("json = %s, want {\"price\":15372}", b)
	}
}

func TestJSONUnmarshalsFromInteger(t *testing.T) {
	var v struct {
		Price money.Centavos `json:"price"`
	}
	if err := json.Unmarshal([]byte(`{"price":15372}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.Price != 15372 {
		t.Errorf("Price = %d, want 15372", int64(v.Price))
	}
}

func TestUnmarshalAcceptsBothWireEncodings(t *testing.T) {
	// The checkout orderForm sends integers; the OMS order list sends
	// decimals for the same kind of value. Both are centavos.
	tests := []struct {
		name string
		json string
		want money.Centavos
	}{
		{"orderForm integer", `{"v":15372}`, 15372},
		{"OMS decimal", `{"v":26511.0}`, 26511},
		{"decimal with fraction", `{"v":26510.6}`, 26511},
		{"string", `{"v":"15372"}`, 15372},
		{"null", `{"v":null}`, 0},
		{"zero", `{"v":0}`, 0},
		{"negative discount", `{"v":-1708.0}`, -1708},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var v struct {
				V money.Centavos `json:"v"`
			}
			if err := json.Unmarshal([]byte(tt.json), &v); err != nil {
				t.Fatalf("%s: %v", tt.json, err)
			}
			if v.V != tt.want {
				t.Errorf("%s -> %d, want %d", tt.json, int64(v.V), int64(tt.want))
			}
		})
	}
}

func TestUnmarshalRejectsGarbage(t *testing.T) {
	var v struct {
		V money.Centavos `json:"v"`
	}
	if err := json.Unmarshal([]byte(`{"v":"not a number"}`), &v); err == nil {
		t.Error("garbage must not silently become zero")
	}
}
