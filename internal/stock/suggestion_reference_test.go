package stock

import (
	"math/big"
	"testing"
)

func TestSuggestionReferenceDemand(t *testing.T) {
	for _, c := range []struct {
		net                     int64
		days                    int
		season                  string
		min, pack, eff, pending int64
		target, want            string
	}{
		{900, 90, "1.2", 0, 1, 20, 14, "84", "50"}, {900, 90, "1.2", 0, 12, 20, 14, "84", "60"},
		{0, 0, "1", 9, 1, 0, 0, "9", "9"}, {1, 1, "1", 0, 1, 0, 0, "7", "7"}, {89, 89, "1", 0, 1, 0, 0, "7", "7"},
		{1, 90, "1", 0, 1, 0, 0, "1", "1"}, {0, 90, "1", 0, 1, 0, 0, "0", "0"}} {
		_, target, got, e := ReferenceDemand(c.net, c.days, 7, c.season, c.min, c.pack, c.eff, c.pending)
		if e != nil || target.String() != c.target || got.String() != c.want {
			t.Fatal(c, target, got, e)
		}
	}
	for _, s := range []string{"0", "-1", "101", "bad"} {
		if _, _, _, e := ReferenceDemand(1, 90, 7, s, 0, 1, 0, 0); e == nil {
			t.Fatal(s)
		}
	}
}
func TestSuggestionReferenceFEFO(t *testing.T) {
	for _, c := range []struct {
		rate    *big.Rat
		lots    []ReferenceLot
		p, r, e string
	}{
		{big.NewRat(2, 1), []ReferenceLot{{1, 10, 2, true}, {2, 3, 100, true}}, "13", "6", "7"},
		{big.NewRat(0, 1), []ReferenceLot{{1, 10, 7, true}, {2, 3, 8, true}}, "13", "10", "3"},
		{big.NewRat(1, 2), []ReferenceLot{{2, 2, 1, true}, {1, 2, 1, true}, {3, 1, 0, true}, {4, 8, 3, false}}, "4", "4", "0"},
		{big.NewRat(2, 1), []ReferenceLot{{1, 10, 2, true}, {2, 10, 3, true}}, "20", "14", "6"},
	} {
		p, r, e, err := ReferenceStock(c.rate, 7, c.lots)
		if err != nil || p.String() != c.p || r.String() != c.r || e.String() != c.e {
			t.Fatal(c, p, r, e, err)
		}
	}
}
