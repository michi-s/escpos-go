package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/user/escpos"
)

func main() {
	// ── Connect ─────────────────────────────────────────────────────
	p, err := escpos.Connect("192.168.123.103", 9100)
	if err != nil {
		log.Fatal(err)
	}
	defer p.Close()

	// Optional: configure for your setup
	// p.SetCodePage(18)    // CP858 instead of default WPC1252
	// p.SetPaperWidth(32)  // 58mm paper instead of 80mm

	// ── Status ──────────────────────────────────────────────────────
	status := p.GetStatus()
	fmt.Println("Printer:", status)

	// ── Test page ───────────────────────────────────────────────────
	p.PrintTest()

	// ── Print a receipt with the built-in gastro template ────────────
	tmpl, err := escpos.NewTemplate("gastro", escpos.DefaultGastroTemplate)
	if err != nil {
		log.Fatal(err)
	}

	order := &escpos.Order{
		OrderNumber:   "B-00042",
		Timestamp:     time.Now(),
		Waiter:        "Lisa",
		TableNumber:   "7",
		Payment:       escpos.PaymentCash,
		AmountPaid:    30.00,
		CustomerCount: 2,
		Items: []escpos.OrderItem{
			{Name: "Weißbier 0,5l", Quantity: 2, UnitPrice: 3.90, Tax: escpos.TaxNormal},
			{Name: "Obatzda", Quantity: 1, UnitPrice: 6.90, Tax: escpos.TaxReduced},
			{Name: "Brezel", Quantity: 2, UnitPrice: 1.50, Tax: escpos.TaxReduced},
		},
	}

	if err := tmpl.Print(p, order); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Receipt printed: %s, Total: %s\n",
		order.OrderNumber, escpos.FormatMoney(order.Subtotal()))

	// ── Print a kitchen slip ────────────────────────────────────────
	kitchenTmpl, _ := escpos.NewTemplate("kitchen", escpos.KitchenTemplate)
	kitchenTmpl.Print(p, order)

	// ── Use a custom template string ────────────────────────────────
	myTemplate := `
@CENTER
@SIZE 2 2
@BOLD
MY SHOP
@/BOLD
@SIZE 1 1
Custom Street 1
@RULE -
{{range .Items}}
@TWOCOL {{fmtQty .Name .Quantity}} | {{money .Total}}
{{end}}
@RULE =
@BOLD
@TWOCOL TOTAL | {{money .Subtotal}}
@/BOLD
@CUT
`
	customTmpl, _ := escpos.NewTemplate("custom", myTemplate)
	customTmpl.Print(p, order)

	// ── Or load a template from a file ──────────────────────────────
	if len(os.Args) > 1 {
		data, err := os.ReadFile(os.Args[1])
		if err != nil {
			log.Fatal(err)
		}
		fileTmpl, err := escpos.NewTemplate("file", string(data))
		if err != nil {
			log.Fatal(err)
		}
		fileTmpl.Print(p, order)
	}

	// ── Direct printer control ──────────────────────────────────────
	p.OpenDrawer(0)
}
