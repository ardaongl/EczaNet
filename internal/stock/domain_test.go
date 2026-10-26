package stock

import (
	"math"
	"testing"
)

func TestConversion(t *testing.T) {
	for _, v := range []struct {
		q, p, b, m int64
		u          string
		want       int64
		bad        bool
	}{{2, 10, 5, 1, "pack", 20, false}, {2, 10, 5, 1, "blister", 10, false}, {3, 10, 5, 1, "unit", 3, false}, {1, 10, 0, 1, "blister", 0, true}, {3, 10, 0, 2, "unit", 0, true}, {math.MaxInt64, 10, 5, 1, "pack", 0, true}, {0, 10, 5, 1, "unit", 0, true}} {
		got, e := Convert(v.q, v.p, v.b, v.m, v.u)
		if (e != nil) != v.bad || got != v.want {
			t.Fatalf("%+v got=%d err=%v", v, got, e)
		}
	}
}
func TestExactPrice(t *testing.T) {
	for q, w := range map[int64]int64{1: 333, 2: 667, 3: 1000} {
		n, e := Price(1000, q, 3)
		if e != nil || n != w {
			t.Fatal(q, n, e)
		}
	}
	if _, e := Price(math.MaxInt64, 2, 1); e == nil {
		t.Fatal("overflow")
	}
}
func TestNormalization(t *testing.T) {
	p := Product{Barcode: "001234", Name: " Basic ", Dose: "5 mg", Form: " TABLET ", BaseUnit: "tablet", PackSize: "10", BlisterSize: "5", SaleMultiple: "1", Ingredients: []string{" ABC ", "abc"}}
	if e := p.Normalize(); e != nil || p.Barcode != "001234" || len(p.Ingredients) != 1 {
		t.Fatal(p, e)
	}
	p.PackSize = "0"
	if p.Normalize() == nil {
		t.Fatal("zero pack")
	}
}
