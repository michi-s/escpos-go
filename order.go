package escpos

import (
	"fmt"
	"time"
)

// ---------------------------------------------------------------------------
// PaymentMethod
// ---------------------------------------------------------------------------

// PaymentMethod represents how an order was paid.
type PaymentMethod string

const (
	PaymentCash   PaymentMethod = "Bar"
	PaymentCard   PaymentMethod = "Karte"
	PaymentMobile PaymentMethod = "Mobile"
)

// ---------------------------------------------------------------------------
// TaxRate
// ---------------------------------------------------------------------------

// TaxRate represents a tax rate with a label and percentage.
type TaxRate struct {
	Label   string  // e.g. "A" or "B"
	Percent float64 // e.g. 19.0 or 7.0
}

// Standard German VAT rates.
var (
	TaxNormal  = TaxRate{Label: "A", Percent: 19.0}
	TaxReduced = TaxRate{Label: "B", Percent: 7.0}
)

// ---------------------------------------------------------------------------
// OrderItem
// ---------------------------------------------------------------------------

// OrderItem represents a single line item in an order.
type OrderItem struct {
	Name      string
	Quantity  int
	UnitPrice float64 // price per unit including tax
	Tax       TaxRate
}

// Total returns the line total (quantity × unit price).
func (i OrderItem) Total() float64 {
	return float64(i.Quantity) * i.UnitPrice
}

// NetPrice returns the net price for this line.
func (i OrderItem) NetPrice() float64 {
	return i.Total() / (1 + i.Tax.Percent/100)
}

// TaxAmount returns the tax portion for this line.
func (i OrderItem) TaxAmount() float64 {
	return i.Total() - i.NetPrice()
}

// ---------------------------------------------------------------------------
// TaxSummary
// ---------------------------------------------------------------------------

// TaxSummary contains aggregated tax information for a single rate.
type TaxSummary struct {
	Rate  TaxRate
	Net   float64
	Tax   float64
	Gross float64
}

// ---------------------------------------------------------------------------
// Order
// ---------------------------------------------------------------------------

// Order represents a complete order ready to be printed as a receipt.
type Order struct {
	OrderNumber   string
	Timestamp     time.Time
	Items         []OrderItem
	Waiter        string
	TableNumber   string
	Payment       PaymentMethod
	AmountPaid    float64 // what the customer gave (relevant for cash)
	CustomerCount int
	Note          string
}

// Subtotal returns the sum of all line totals (gross).
func (o *Order) Subtotal() float64 {
	total := 0.0
	for _, item := range o.Items {
		total += item.Total()
	}
	return total
}

// TaxSummaries returns tax totals grouped by tax rate.
func (o *Order) TaxSummaries() []TaxSummary {
	byLabel := map[string]*TaxSummary{}
	for _, item := range o.Items {
		key := item.Tax.Label
		if _, ok := byLabel[key]; !ok {
			byLabel[key] = &TaxSummary{Rate: item.Tax}
		}
		s := byLabel[key]
		s.Gross += item.Total()
		s.Net += item.NetPrice()
		s.Tax += item.TaxAmount()
	}
	result := []TaxSummary{}
	for _, label := range []string{"A", "B", "C", "D"} {
		if s, ok := byLabel[label]; ok {
			result = append(result, *s)
		}
	}
	return result
}

// Change returns the change due. Returns 0 for non-cash payments.
func (o *Order) Change() float64 {
	if o.Payment != PaymentCash {
		return 0
	}
	c := o.AmountPaid - o.Subtotal()
	if c < 0 {
		return 0
	}
	return c
}

// ItemCount returns the total number of items (sum of quantities).
func (o *Order) ItemCount() int {
	n := 0
	for _, item := range o.Items {
		n += item.Quantity
	}
	return n
}

// FormatMoney formats a float as a German currency string: "12,50 €".
func FormatMoney(amount float64) string {
	return fmt.Sprintf("%.2f €", amount)
}

// ---------------------------------------------------------------------------
// NewDemoOrder creates a sample order for testing.
// ---------------------------------------------------------------------------

// NewDemoOrder returns a pre-filled demo order useful for testing
// the printer and templates.
func NewDemoOrder() *Order {
	return &Order{
		OrderNumber:   fmt.Sprintf("B-%05d", time.Now().Unix()%100000),
		Timestamp:     time.Now(),
		Waiter:        "Max",
		TableNumber:   "12",
		Payment:       PaymentCash,
		AmountPaid:    50.00,
		CustomerCount: 2,
		Items: []OrderItem{
			{Name: "Weißbier 0,5l", Quantity: 2, UnitPrice: 3.90, Tax: TaxNormal},
			{Name: "Brezel", Quantity: 3, UnitPrice: 1.50, Tax: TaxReduced},
			{Name: "Obatzda", Quantity: 1, UnitPrice: 6.90, Tax: TaxReduced},
			{Name: "Schweinshaxe", Quantity: 1, UnitPrice: 14.50, Tax: TaxReduced},
			{Name: "Kaiserschmarrn", Quantity: 1, UnitPrice: 9.80, Tax: TaxReduced},
			{Name: "Espresso", Quantity: 2, UnitPrice: 2.40, Tax: TaxNormal},
		},
	}
}
