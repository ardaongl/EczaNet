package stock

import (
	"math/big"
	"regexp"
	"sort"
	"strings"
	"time"
	_ "time/tzdata"
)

var istanbul, _ = time.LoadLocation("Europe/Istanbul")

func businessDay(at time.Time) string { return at.In(istanbul).Format("2006-01-02") }
func prescriptionValid(issued, at time.Time) bool {
	return !at.Before(issued) && at.Before(issued.Add(168*time.Hour))
}

// Completed age follows the documented demo calendar: Feb 29 becomes Feb 28.
func patientRate(birth, at time.Time, from, to *time.Time) int64 {
	if from != nil && to != nil && !at.Before(*from) && at.Before(*to) {
		return 0
	}
	today := at.In(istanbul)
	month, day := birth.Month(), birth.Day()
	if month == time.February && day == 29 && time.Date(today.Year(), time.March, 0, 0, 0, 0, 0, istanbul).Day() == 28 {
		day = 28
	}
	age := today.Year() - birth.Year()
	if today.Before(time.Date(today.Year(), month, day, 0, 0, 0, 0, istanbul)) {
		age--
	}
	if age < 18 {
		return 10
	}
	if age >= 65 {
		return 5
	}
	return 20
}

type Calculation struct {
	Policy                  string `json:"policy"`
	List                    string `json:"list_exact"`
	AfterInstitution        string `json:"after_institution_exact"`
	TierDiscount            string `json:"tier_discount_exact"`
	InstitutionDiscountRate string `json:"institution_discount_rate"`
	PatientRate             int64  `json:"patient_rate"`
	Total                   string `json:"total"`
	Patient                 string `json:"patient_total"`
	Institution             string `json:"institution_total"`
}

func halfUp(r *big.Rat) (int64, error) {
	if r.Sign() < 0 {
		return 0, Invalid()
	}
	n := new(big.Int).Mul(r.Num(), big.NewInt(2))
	n.Add(n, r.Denom())
	n.Quo(n, new(big.Int).Mul(r.Denom(), big.NewInt(2)))
	if !n.IsInt64() {
		return 0, Invalid()
	}
	return n.Int64(), nil
}
func calculate(price, qty, pack int64, rx bool, rate int64) (Calculation, error) {
	c := Calculation{Policy: "A2-normal-v1", InstitutionDiscountRate: "0", PatientRate: 100}
	if price <= 0 || qty <= 0 || pack <= 0 || rate < 0 || rate > 100 {
		return c, Invalid()
	}
	list := new(big.Rat).SetFrac(new(big.Int).Mul(big.NewInt(price), big.NewInt(qty)), big.NewInt(pack))
	c.List = list.RatString()
	net := new(big.Rat).Set(list)
	discount := new(big.Rat)
	if rx {
		c.Policy = "demo-rx-v1"
		c.InstitutionDiscountRate = "1/10"
		c.PatientRate = rate
		net.Mul(net, big.NewRat(9, 10))
		excess := new(big.Rat).Sub(net, big.NewRat(10000, 1))
		if excess.Sign() > 0 {
			if excess.Cmp(big.NewRat(10000, 1)) > 0 {
				excess.SetInt64(10000)
			}
			discount.Mul(excess, big.NewRat(5, 100))
		}
		excess.Sub(net, big.NewRat(20000, 1))
		if excess.Sign() > 0 {
			discount.Add(discount, new(big.Rat).Mul(excess, big.NewRat(10, 100)))
		}
	}
	c.AfterInstitution = net.RatString()
	c.TierDiscount = discount.RatString()
	net.Sub(net, discount)
	total, e := halfUp(net)
	if e != nil || total <= 0 {
		return c, Invalid()
	}
	patient, e := halfUp(new(big.Rat).Mul(new(big.Rat).SetInt64(total), big.NewRat(c.PatientRate, 100)))
	if e != nil {
		return c, e
	}
	c.Total = big.NewInt(total).String()
	c.Patient = big.NewInt(patient).String()
	c.Institution = big.NewInt(total - patient).String()
	return c, nil
}

// An ingredient's identity and its strength form a normalized composition.
// For combinations, dose is a '+' separated strength in sorted ingredient order.
// Exact rational mg/ml normalization avoids decimal floating point comparisons.
func composition(p Product) (string, error) {
	names := append([]string(nil), p.Ingredients...)
	sort.Strings(names)
	doses := strings.Split(strings.ToLower(strings.ReplaceAll(p.Dose, ",", ".")), "+")
	if len(names) != len(doses) {
		return "", Invalid()
	}
	rx := regexp.MustCompile(`^\s*([0-9]+(?:\.[0-9]+)?)\s*(mcg|mg|g)(?:\s*/\s*([0-9]+(?:\.[0-9]+)?)?\s*(ml))?\s*$`)
	parts := []string{strings.ToLower(strings.TrimSpace(p.Form)), p.BaseUnit, p.ControlledGroup}
	for i, d := range doses {
		m := rx.FindStringSubmatch(d)
		if m == nil {
			return "", Invalid()
		}
		q, ok := new(big.Rat).SetString(m[1])
		if !ok || q.Sign() <= 0 {
			return "", Invalid()
		}
		switch m[2] {
		case "g":
			q.Mul(q, big.NewRat(1000, 1))
		case "mcg":
			q.Quo(q, big.NewRat(1000, 1))
		}
		dimension := "mg"
		if m[4] != "" {
			den := big.NewRat(1, 1)
			if m[3] != "" {
				den, ok = new(big.Rat).SetString(m[3])
				if !ok || den.Sign() <= 0 {
					return "", Invalid()
				}
			}
			q.Quo(q, den)
			dimension = "mg/ml"
		}
		parts = append(parts, names[i]+":"+q.RatString()+dimension)
	}
	return strings.Join(parts, "|"), nil
}
