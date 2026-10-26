package stock

import (
	"math/big"
	"sort"
)

// ReferenceDemand and ReferenceStock are intentionally independent of SQL.
// All intermediate arithmetic is rational; only indivisible basic units round up.
func rationalCeil(x *big.Rat) *big.Int {
	q, r := new(big.Int), new(big.Int)
	q.QuoRem(x.Num(), x.Denom(), r)
	if r.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	return q
}
func ReferenceDemand(net int64, days, horizon int, season string, minimum, pack, effective, pending int64) (rate *big.Rat, target, suggested *big.Int, err error) {
	coefficient, ok := new(big.Rat).SetString(season)
	if !ok || coefficient.Sign() <= 0 || coefficient.Cmp(big.NewRat(100, 1)) > 0 || net < 0 || days < 0 || days > 90 || horizon < 1 || horizon > 90 || minimum < 0 || pack < 1 || effective < 0 || pending < 0 {
		return nil, nil, nil, Invalid()
	}
	rate = new(big.Rat)
	if days > 0 {
		rate.Mul(big.NewRat(net, int64(days)), coefficient)
	}
	target = rationalCeil(new(big.Rat).Mul(rate, big.NewRat(int64(horizon), 1)))
	if target.Cmp(big.NewInt(minimum)) < 0 {
		target.SetInt64(minimum)
	}
	need := new(big.Int).Sub(target, big.NewInt(effective))
	need.Sub(need, big.NewInt(pending))
	if need.Sign() < 0 {
		need.SetInt64(0)
	}
	boxes := rationalCeil(new(big.Rat).SetFrac(need, big.NewInt(pack)))
	return rate, target, new(big.Int).Mul(boxes, big.NewInt(pack)), nil
}

type ReferenceLot struct {
	ID, Quantity int64
	Days         int
	Eligible     bool
}

func ReferenceStock(rate *big.Rat, horizon int, lots []ReferenceLot) (physical, risk, effective *big.Int, err error) {
	if rate == nil || rate.Sign() < 0 || horizon < 1 || horizon > 90 {
		return nil, nil, nil, Invalid()
	}
	lots = append([]ReferenceLot{}, lots...)
	sort.Slice(lots, func(i, j int) bool {
		if lots[i].Days == lots[j].Days {
			return lots[i].ID < lots[j].ID
		}
		return lots[i].Days < lots[j].Days
	})
	physical, risk = new(big.Int), new(big.Int)
	used := new(big.Rat)
	for _, l := range lots {
		if l.Quantity < 0 {
			return nil, nil, nil, Invalid()
		}
		if !l.Eligible || l.Days < 1 {
			continue
		}
		physical.Add(physical, big.NewInt(l.Quantity))
		d := l.Days
		if d > horizon {
			d = horizon
		}
		available := new(big.Rat).Sub(new(big.Rat).Mul(rate, big.NewRat(int64(d), 1)), used)
		if available.Sign() < 0 {
			available.SetInt64(0)
		}
		q := big.NewRat(l.Quantity, 1)
		if available.Cmp(q) > 0 {
			available.Set(q)
		}
		used.Add(used, available)
		if l.Days <= horizon {
			risk.Add(risk, rationalCeil(new(big.Rat).Sub(q, available)))
		}
	}
	return physical, risk, new(big.Int).Sub(physical, risk), nil
}
