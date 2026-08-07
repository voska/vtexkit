// Package money represents Brazilian currency amounts as integer centavos.
//
// VTEX is inconsistent about how it reports prices: the catalog search APIs
// return decimal reais as float64 (153.72) while the checkout orderForm and
// delivery simulation return integer centavos (15372). Converting at the API
// boundary means no downstream code ever handles a float price, which removes
// a class of rounding bug from checkout arithmetic.
package money

import (
	"fmt"
	"math"
	"strings"
)

// Centavos is an amount in Brazilian centavos. It is an integer type, so it
// marshals to JSON as a plain number and agent-facing output stays
// arithmetic-safe.
type Centavos int64

// Reais converts a decimal-reais amount to Centavos, rounding half away from
// zero. Rounding matters: 0.1+0.2 is 0.30000000000000004 in float64, and
// truncating would price it at 29 centavos.
func Reais(r float64) Centavos {
	return Centavos(math.Round(r * 100))
}

// Reais returns the amount as decimal reais. Use it only at boundaries that
// demand a float, such as building a VTEX request body that expects one.
func (c Centavos) Reais() float64 {
	return float64(c) / 100
}

// String renders the amount in pt-BR currency format: R$1.234.567,89
func (c Centavos) String() string {
	neg := c < 0
	if neg {
		c = -c
	}

	whole := fmt.Sprintf("%d", int64(c)/100)
	frac := int64(c) % 100

	var groups []string
	for len(whole) > 3 {
		groups = append([]string{whole[len(whole)-3:]}, groups...)
		whole = whole[:len(whole)-3]
	}
	groups = append([]string{whole}, groups...)

	out := fmt.Sprintf("R$%s,%02d", strings.Join(groups, "."), frac)
	if neg {
		return "-" + out
	}
	return out
}
