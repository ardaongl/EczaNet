package stock

import (
	"math"
	"testing"
	"time"
)

func instant(s string) time.Time {
	t, e := time.Parse(time.RFC3339, s)
	if e != nil {
		panic(e)
	}
	return t
}
func TestA3ExactPricing(t *testing.T) {
	for _, v := range []struct {
		price, qty, pack, rate      int64
		rx                          bool
		total, patient, institution string
	}{
		{20000, 1, 1, 20, true, "17600", "3520", "14080"},
		{20000, 1, 1, 0, true, "17600", "0", "17600"},
		{10000, 1, 1, 20, true, "9000", "1800", "7200"},
		{100000, 1, 9, 20, true, "10000", "2000", "8000"},
		{200000, 1, 9, 20, true, "19500", "3900", "15600"},
		{30000, 1, 1, 20, true, "25800", "5160", "20640"},
		{1000, 1, 3, 100, false, "333", "333", "0"},
		{1000, 2, 3, 100, false, "667", "667", "0"},
		{1000, 3, 3, 100, false, "1000", "1000", "0"},
		{1, 1, 1, 20, true, "1", "0", "1"},
	} {
		c, e := calculate(v.price, v.qty, v.pack, v.rx, v.rate)
		if e != nil || c.Total != v.total || c.Patient != v.patient || c.Institution != v.institution {
			t.Fatalf("%+v got=%+v err=%v", v, c, e)
		}
	}
	if _, e := calculate(math.MaxInt64, 2, 1, true, 20); e == nil {
		t.Fatal("overflow accepted")
	}
}
func TestA3AgeAndReportBoundaries(t *testing.T) {
	for _, v := range []struct {
		birth, at string
		want      int64
	}{
		{"2008-10-25T00:00:00Z", "2026-10-24T21:00:00Z", 20},
		{"2008-10-25T00:00:00Z", "2026-10-24T20:59:59Z", 10},
		{"1961-10-25T00:00:00Z", "2026-10-24T21:00:00Z", 5},
		{"1961-10-25T00:00:00Z", "2026-10-24T20:59:59Z", 20},
		{"2008-02-29T00:00:00Z", "2026-02-27T21:00:00Z", 20},
		{"2008-02-29T00:00:00Z", "2026-02-27T20:59:59Z", 10},
	} {
		if got := patientRate(instant(v.birth), instant(v.at), nil, nil); got != v.want {
			t.Fatalf("%+v got=%d", v, got)
		}
	}
	birth := instant("1990-01-01T00:00:00Z")
	from := instant("2026-10-25T00:00:00Z")
	to := from.Add(time.Hour)
	if patientRate(birth, from, &from, &to) != 0 || patientRate(birth, to, &from, &to) != 20 {
		t.Fatal("report half open boundary")
	}
}
func TestA3PrescriptionTimeWindow(t *testing.T) {
	issued := instant("2026-10-20T00:00:00Z")
	for _, v := range []struct {
		delta time.Duration
		want  bool
	}{{-time.Nanosecond, false}, {0, true}, {168*time.Hour - time.Nanosecond, true}, {168 * time.Hour, false}} {
		if prescriptionValid(issued, issued.Add(v.delta)) != v.want {
			t.Fatal(v)
		}
	}
}
func TestA3NormalizedComposition(t *testing.T) {
	p := Product{Dose: "500 mg", Form: "tablet", BaseUnit: "tablet", Ingredients: []string{"a"}}
	key, e := composition(p)
	if e != nil {
		t.Fatal(e)
	}
	for _, dose := range []string{"0.5 g", "500000 mcg"} {
		q := p
		q.Dose = dose
		v, e := composition(q)
		if e != nil || v != key {
			t.Fatal(dose, v, e)
		}
	}
	for _, q := range []Product{
		{Dose: "250mg", Form: "tablet", BaseUnit: "tablet", Ingredients: []string{"a"}},
		{Dose: "500mg", Form: "capsule", BaseUnit: "tablet", Ingredients: []string{"a"}},
		{Dose: "500mg", Form: "tablet", BaseUnit: "tablet", Ingredients: []string{"b"}},
		{Dose: "500mg+5mg", Form: "tablet", BaseUnit: "tablet", Ingredients: []string{"a", "b"}},
	} {
		v, e := composition(q)
		if e == nil && v == key {
			t.Fatal("invalid equivalent", q)
		}
	}
	a := Product{Dose: "500mg/5ml", Form: "solution", BaseUnit: "ml", Ingredients: []string{"a"}}
	b := a
	b.Dose = "0.1g/ml"
	x, _ := composition(a)
	y, _ := composition(b)
	if x != y {
		t.Fatal(x, y)
	}
}
