package escpos

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"text/template"
)

// ---------------------------------------------------------------------------
// ReceiptTemplate defines how an Order is rendered on the printer.
//
// The template uses Go text/template syntax for data, plus @-directives
// on their own lines to control printer formatting:
//
//   @CENTER / @LEFT / @RIGHT          alignment
//   @BOLD / @/BOLD                    bold on/off
//   @UNDERLINE / @/UNDERLINE          underline on/off
//   @INVERT / @/INVERT               inverted on/off
//   @SIZE w h                         character magnification (1-8)
//   @FONT n                           font 0=A, 1=B
//   @RULE char                        horizontal rule (e.g. @RULE -)
//   @FEED n                           feed n blank lines
//   @TWOCOL left | right              two-column justified line
//   @THREECOL left | center | right   three-column justified line
//   @BARCODE type data                1D barcode (EAN13, CODE128, CODE39)
//   @QRCODE size data                 QR code
//   @CUT                              partial paper cut
//   @FULLCUT                          full paper cut
//   @DRAWER                           open cash drawer
//   @BEEP n                           beep n times
//
// Available template functions:
//   money(float64)      → "12,50 €"
//   date(time, layout)  → formatted date
//   pad(s, width)       → right-padded string
//   rpad(s, width)      → left-padded string
//   repeat(s, n)        → repeat string
//   upper(s)            → uppercase
//   fmtQty(name, qty)   → "Name x3" or just "Name" if qty=1
// ---------------------------------------------------------------------------

// ReceiptTemplate holds a compiled template for printing receipts.
type ReceiptTemplate struct {
	Name     string
	Source   string
	compiled *template.Template
}

var templateFuncs = template.FuncMap{
	"money": func(amount float64) string {
		s := fmt.Sprintf("%.2f", amount)
		s = strings.Replace(s, ".", ",", 1)
		return s + " €"
	},
	"date": func(t interface{ Format(string) string }, layout string) string {
		return t.Format(layout)
	},
	"pad": func(s string, width int) string {
		if len(s) >= width {
			return s[:width]
		}
		return s + strings.Repeat(" ", width-len(s))
	},
	"rpad": func(s string, width int) string {
		if len(s) >= width {
			return s[:width]
		}
		return strings.Repeat(" ", width-len(s)) + s
	},
	"repeat": strings.Repeat,
	"upper":  strings.ToUpper,
	"mul": func(a, b int) int {
		return a * b
	},
	"fmtQty": func(name string, qty int) string {
		if qty == 1 {
			return name
		}
		return fmt.Sprintf("%s x%d", name, qty)
	},
}

// NewTemplate creates and compiles a receipt template from a template string.
func NewTemplate(name, source string) (*ReceiptTemplate, error) {
	t, err := template.New(name).Funcs(templateFuncs).Parse(source)
	if err != nil {
		return nil, fmt.Errorf("parse template %q: %w", name, err)
	}
	return &ReceiptTemplate{
		Name:     name,
		Source:   source,
		compiled: t,
	}, nil
}

// Render executes the template with the given order and returns the
// intermediate text with @-directives (useful for debugging).
func (t *ReceiptTemplate) Render(order *Order) (string, error) {
	var buf bytes.Buffer
	if err := t.compiled.Execute(&buf, order); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}
	return buf.String(), nil
}

// Print renders the template for the given order and sends it to the printer.
func (t *ReceiptTemplate) Print(p *Printer, order *Order) error {
	text, err := t.Render(order)
	if err != nil {
		return err
	}

	p.Init()
	p.SetCodePage(p.codepage)
	p.SetCharset(2)

	lines := strings.Split(text, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "@") {
			executeDirective(p, trimmed)
		} else {
			p.Text(trimmed)
		}
	}

	return nil
}

func executeDirective(p *Printer, line string) {
	parts := strings.SplitN(line, " ", 2)
	cmd := strings.ToUpper(parts[0])
	arg := ""
	if len(parts) > 1 {
		arg = parts[1]
	}

	switch cmd {
	case "@CENTER":
		p.Align(1)
	case "@LEFT":
		p.Align(0)
	case "@RIGHT":
		p.Align(2)
	case "@BOLD":
		p.Bold(true)
	case "@/BOLD":
		p.Bold(false)
	case "@UNDERLINE":
		p.Underline(1)
	case "@/UNDERLINE":
		p.Underline(0)
	case "@INVERT":
		p.Invert(true)
	case "@/INVERT":
		p.Invert(false)
	case "@FONT":
		n, _ := strconv.Atoi(arg)
		p.Font(byte(n))
	case "@SIZE":
		var w, h int
		fmt.Sscanf(arg, "%d %d", &w, &h)
		if w < 1 {
			w = 1
		}
		if h < 1 {
			h = 1
		}
		p.Size(byte(w), byte(h))
	case "@RULE":
		ch := byte('-')
		if len(arg) > 0 {
			ch = arg[0]
		}
		p.HRule(ch)
	case "@FEED":
		n, _ := strconv.Atoi(arg)
		if n < 1 {
			n = 1
		}
		p.Feed(n)
	case "@TWOCOL":
		cols := strings.SplitN(arg, "|", 2)
		if len(cols) == 2 {
			p.TwoCol(strings.TrimSpace(cols[0]), strings.TrimSpace(cols[1]))
		} else {
			p.Text(arg)
		}
	case "@THREECOL":
		cols := strings.SplitN(arg, "|", 3)
		if len(cols) == 3 {
			p.ThreeCol(
				strings.TrimSpace(cols[0]),
				strings.TrimSpace(cols[1]),
				strings.TrimSpace(cols[2]),
			)
		} else {
			p.Text(arg)
		}
	case "@BARCODE":
		var typ, data string
		fmt.Sscanf(arg, "%s %s", &typ, &data)
		p.BarcodeHeight(80)
		p.BarcodeWidth(3)
		p.BarcodeHRI(2)
		switch strings.ToUpper(typ) {
		case "EAN13":
			p.Barcode(2, data)
		case "CODE128":
			p.Barcode(73, "{B"+data)
		case "CODE39":
			p.Barcode(4, data)
		default:
			p.Barcode(73, "{B"+data)
		}
	case "@QRCODE":
		var size int
		var data string
		n, _ := fmt.Sscanf(arg, "%d %s", &size, &data)
		if n < 2 {
			size = 5
			data = strings.TrimSpace(arg)
		}
		p.QRCode(data, byte(size), 49)
	case "@CUT":
		p.PartialCut()
	case "@FULLCUT":
		p.Cut()
	case "@DRAWER":
		p.OpenDrawer(0)
	case "@BEEP":
		n, _ := strconv.Atoi(arg)
		if n < 1 {
			n = 1
		}
		p.Beep(byte(n), 2)
	}
}

// ---------------------------------------------------------------------------
// Built-in templates
// ---------------------------------------------------------------------------

// DefaultGastroTemplate is a full-featured German gastro receipt template.
var DefaultGastroTemplate = `
@CENTER
@SIZE 2 2
@BOLD
GASTHAUS MUSTER
@/BOLD
@SIZE 1 1
@FEED 1
Musterstraße 42
80331 München
Tel: 089 / 123 456 78
USt-IdNr: DE123456789
@LEFT
@RULE -
@TWOCOL Bon: {{.OrderNumber}} | Datum: {{date .Timestamp "02.01.2006"}}
@TWOCOL Tisch: {{.TableNumber}} | Kellner: {{.Waiter}}
@TWOCOL Gäste: {{.CustomerCount}} | Zeit: {{date .Timestamp "15:04"}}
@RULE -
@BOLD
@TWOCOL Artikel | Betrag
@/BOLD
@RULE -
{{range .Items}}
@TWOCOL {{fmtQty .Name .Quantity}} | {{money .Total}}
{{end}}
@RULE =
@BOLD
@SIZE 1 2
@TWOCOL GESAMT | {{money .Subtotal}}
@SIZE 1 1
@/BOLD
@FEED 1
{{range .TaxSummaries}}
@TWOCOL MwSt {{.Rate.Label}} ({{printf "%.0f" .Rate.Percent}}%) Netto | {{money .Net}}
@TWOCOL MwSt {{.Rate.Label}} ({{printf "%.0f" .Rate.Percent}}%) Steuer | {{money .Tax}}
{{end}}
@RULE -
@TWOCOL Zahlung: {{printf "%s" .Payment}} | {{money .AmountPaid}}
{{if eq (printf "%s" .Payment) "Bar"}}
@BOLD
@TWOCOL Rückgeld | {{money .Change}}
@/BOLD
{{end}}
@RULE -
@CENTER
@FEED 1
Vielen Dank für Ihren Besuch!
{{if .Note}}
{{.Note}}
{{end}}
@FEED 1
@QRCODE 5 https://example.com/receipt/{{.OrderNumber}}
@FEED 1
@FONT 1
TSE-Signatur: abc123def456
TSE-Nr: DE-0001-0042
@FONT 0
@FEED 1
@CUT
`

// MinimalTemplate is a stripped-down receipt for quick orders.
var MinimalTemplate = `
@CENTER
@BOLD
@SIZE 2 1
BON {{.OrderNumber}}
@SIZE 1 1
@/BOLD
{{date .Timestamp "02.01.2006 15:04"}}
Tisch {{.TableNumber}} / {{.Waiter}}
@LEFT
@RULE -
{{range .Items}}
@TWOCOL {{fmtQty .Name .Quantity}} | {{money .Total}}
{{end}}
@RULE =
@BOLD
@TWOCOL SUMME | {{money .Subtotal}}
@/BOLD
@RULE -
@CENTER
Danke!
@CUT
`

// KitchenTemplate prints a kitchen order slip (no prices).
var KitchenTemplate = `
@CENTER
@SIZE 2 2
@BOLD
@INVERT
 KÜCHE 
@/INVERT
@/BOLD
@SIZE 1 1
@FEED 1
@LEFT
@BOLD
@TWOCOL Tisch: {{.TableNumber}} | {{date .Timestamp "15:04"}}
@TWOCOL Kellner: {{.Waiter}} | Gäste: {{.CustomerCount}}
@/BOLD
@RULE =
{{range .Items}}
@BOLD
@SIZE 2 1
{{fmtQty .Name .Quantity}}
@SIZE 1 1
@/BOLD
{{end}}
@RULE =
@FEED 1
@BOLD
Bon: {{.OrderNumber}}
@/BOLD
@CUT
`
