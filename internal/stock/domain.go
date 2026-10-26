package stock

import (
	"errors"
	"math"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Error struct {
	Status        int
	Code, Message string
	NewQuote      any
}

func (e *Error) Error() string { return e.Code }
func Reject(status int, code, message string) error {
	return &Error{Status: status, Code: code, Message: message}
}
func Invalid() error {
	return Reject(422, "INVALID_INPUT", "Alanlari, miktarlari ve birimleri kontrol edin")
}
func Positive(s string) (int64, error) {
	n, e := strconv.ParseInt(s, 10, 64)
	if e != nil || n <= 0 || strconv.FormatInt(n, 10) != s {
		return 0, Invalid()
	}
	return n, nil
}
func Convert(q, pack, blister, multiple int64, unit string) (int64, error) {
	factor := int64(1)
	switch unit {
	case "unit":
	case "pack":
		factor = pack
	case "blister":
		factor = blister
	default:
		return 0, Invalid()
	}
	if q <= 0 || factor <= 0 || q > math.MaxInt64/factor || multiple <= 0 {
		return 0, Invalid()
	}
	n := q * factor
	if n%multiple != 0 {
		return 0, Invalid()
	}
	return n, nil
}
func Price(packPrice, qty, pack int64) (int64, error) {
	if packPrice <= 0 || qty <= 0 || pack <= 0 {
		return 0, Invalid()
	}
	n := new(big.Int).Mul(big.NewInt(packPrice), big.NewInt(qty))
	n.Mul(n, big.NewInt(2))
	n.Add(n, big.NewInt(pack))
	n.Div(n, new(big.Int).Mul(big.NewInt(pack), big.NewInt(2)))
	if !n.IsInt64() || n.Sign() <= 0 {
		return 0, Invalid()
	}
	return n.Int64(), nil
}

type Item struct {
	ProductID string `json:"product_id"`
	Quantity  string `json:"quantity"`
	Unit      string `json:"unit"`
}
type ReturnInput struct {
	Quote      string `json:"quote,omitempty"`
	SaleID     string `json:"sale_id"`
	SaleLineID string `json:"sale_line_id"`
	LotID      string `json:"lot_id"`
	Quantity   string `json:"quantity"`
}
type DecisionInput struct {
	Action   string `json:"action"`
	Quantity string `json:"quantity"`
	Sealed   bool   `json:"sealed"`
}
type DeliveryLine struct {
	LotID   string `json:"lot_id"`
	Good    string `json:"good"`
	Damaged string `json:"damaged"`
	Missing string `json:"missing"`
}
type DeliveryInput struct {
	Final bool           `json:"final"`
	Lines []DeliveryLine `json:"lines"`
}
type CountLine struct {
	LotID   string `json:"lot_id"`
	Account string `json:"account"`
	Actual  string `json:"actual"`
}
type CountInput struct {
	ProductID string      `json:"product_id"`
	Lines     []CountLine `json:"lines,omitempty"`
}
type NeedInput struct {
	ProductID string `json:"product_id"`
	Quantity  string `json:"quantity"`
}
type OrderLineInput struct {
	ProductID string   `json:"product_id"`
	Quantity  string   `json:"quantity"`
	Needs     []string `json:"needs,omitempty"`
}
type OrderInput struct {
	Lines []OrderLineInput `json:"lines"`
}
type Input struct {
	SuggestionPolicy *SuggestionPolicyInput `json:"suggestion_policy,omitempty"`
	ExpectedOn       string                 `json:"expected_on,omitempty"`
	AsOf             string                 `json:"as_of,omitempty"`
	Need             *NeedInput             `json:"need,omitempty"`
	Order            *OrderInput            `json:"order,omitempty"`
	OrderID          string                 `json:"order_id,omitempty"`
	ReceiptMode      string                 `json:"receipt_mode,omitempty"`
	AllocationID     string                 `json:"allocation_id,omitempty"`
	Count            *CountInput            `json:"count,omitempty"`
	Delivery         *DeliveryInput         `json:"delivery,omitempty"`
	Return           *ReturnInput           `json:"return,omitempty"`
	Decision         *DecisionInput         `json:"decision,omitempty"`
	PaymentMethod    string                 `json:"payment_method,omitempty"`
	Prescription     *PrescriptionInput     `json:"prescription,omitempty"`
	PrescriptionID   string                 `json:"prescription_id,omitempty"`
	Dispense         []DispenseItem         `json:"dispense,omitempty"`
	Price            *PriceInput            `json:"price,omitempty"`

	Product    *Product      `json:"product,omitempty"`
	LocationID string        `json:"location_id"`
	TargetID   string        `json:"target_id,omitempty"`
	SupplierID string        `json:"supplier_id,omitempty"`
	DocumentNo string        `json:"document_no,omitempty"`
	Reason     string        `json:"reason,omitempty"`
	Lines      []ReceiptLine `json:"lines,omitempty"`
	Items      []Item        `json:"items,omitempty"`
	Quote      string        `json:"quote,omitempty"`
}
type ReceiptLine struct {
	ProductID string `json:"product_id"`
	Lot       string `json:"lot"`
	ExpiresOn string `json:"expires_on"`
	Quantity  string `json:"quantity"`
}
type Product struct {
	ID                   string   `json:"id"`
	Barcode              string   `json:"barcode"`
	Name                 string   `json:"name"`
	Dose                 string   `json:"dose"`
	Form                 string   `json:"form"`
	BaseUnit             string   `json:"base_unit"`
	PackSize             string   `json:"pack_size"`
	BlisterSize          string   `json:"blister_size"`
	SaleMultiple         string   `json:"sale_multiple"`
	Ingredients          []string `json:"ingredients"`
	PrescriptionRequired bool     `json:"prescription_required"`
	ControlledGroup      string   `json:"controlled_group"`
	Active               bool     `json:"active"`
	PackPrice            string   `json:"pack_price,omitempty"`
}

func (p *Product) Normalize() error {
	p.Name = strings.TrimSpace(p.Name)
	p.Dose = strings.TrimSpace(p.Dose)
	p.Form = strings.ToLower(strings.TrimSpace(p.Form))
	p.ControlledGroup = strings.TrimSpace(p.ControlledGroup)
	if len(p.Barcode) < 4 || len(p.Barcode) > 40 || p.Name == "" || len(p.Name) > 200 || p.Dose == "" || len(p.Dose) > 100 || p.Form == "" || len(p.Form) > 100 || len(p.ControlledGroup) > 100 {
		return Invalid()
	}
	for _, c := range p.Barcode {
		if c < '0' || c > '9' {
			return Invalid()
		}
	}
	doseNumber := regexp.MustCompile(`^[0-9]+(?:[.,][0-9]+)?`).FindString(p.Dose)
	doseRat, ok := new(big.Rat).SetString(strings.ReplaceAll(doseNumber, ",", "."))
	if !ok || doseRat.Sign() <= 0 || len(doseNumber) == len(p.Dose) {
		return Invalid()
	}
	if p.ControlledGroup != "" && (p.ControlledGroup != "CONTROL-TAB" || p.BaseUnit != "tablet") {
		return Invalid()
	}
	if p.BaseUnit != "tablet" && p.BaseUnit != "ml" && p.BaseUnit != "adet" {
		return Invalid()
	}
	pack, e := Positive(p.PackSize)
	if e != nil || pack > 1000000000 {
		return Invalid()
	}
	m, e := Positive(p.SaleMultiple)
	if e != nil || pack%m != 0 {
		return Invalid()
	}
	b, e := strconv.ParseInt(p.BlisterSize, 10, 64)
	if e != nil || b < 0 || b > pack || b%m != 0 {
		return Invalid()
	}
	if len(p.Ingredients) == 0 || len(p.Ingredients) > 20 {
		return Invalid()
	}
	seen := map[string]bool{}
	a := []string{}
	for _, i := range p.Ingredients {
		i = strings.ToLower(strings.TrimSpace(i))
		if i == "" || len(i) > 100 {
			return Invalid()
		}
		if !seen[i] {
			a = append(a, i)
			seen[i] = true
		}
	}
	sort.Strings(a)
	p.Ingredients = a
	if p.PackPrice != "" {
		if _, e = Positive(p.PackPrice); e != nil {
			return e
		}
	}
	return nil
}
func IsBusiness(err error) bool { var b *Error; return errors.As(err, &b) }
