package main

import (
	"strconv"
	"strings"
	"testing"
)

func TestDeterministicCatalogCycle(t *testing.T) {
	seen := map[string]bool{}
	for i := int64(0); i < 4995; i++ {
		barcode := normalBarcode("33906", i)
		if len(barcode) != 13 || !strings.HasPrefix(barcode, "0099") || seen[barcode] {
			t.Fatal("invalid or repeated catalog barcode", barcode)
		}
		n, e := strconv.Atoi(barcode[4:])
		if e != nil || n < 1 || n > 4995 {
			t.Fatal("outside seed catalog", barcode)
		}
		seen[barcode] = true
		if barcode != normalBarcode("33906", i+4995) {
			t.Fatal("restart/cycle nondeterminism")
		}
	}
	if normalBarcode("33906", 0) == normalBarcode("33907", 0) {
		t.Fatal("seed must affect catalog offset")
	}
	controlled := 0
	for n := int64(0); n < 3000; n++ {
		if n%6 == 4 && (n/6)%100 == 0 {
			controlled++
		}
	}
	if controlled != 5 {
		t.Fatal("controlled demo-day consumption", controlled)
	}
}
