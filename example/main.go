package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/michi-s/escpos-go"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println(`Usage:
  example preview              Generate preview files (no printer needed)
  example print <ip> [port]    Print to a real printer`)
		os.Exit(1)
	}

	// Create a sample order
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
			{Name: "Kaiserschmarrn", Quantity: 1, UnitPrice: 9.80, Tax: escpos.TaxReduced},
			{Name: "Espresso", Quantity: 2, UnitPrice: 2.40, Tax: escpos.TaxNormal},
		},
	}

	switch os.Args[1] {
	case "preview":
		doPreview(order)
	case "print":
		if len(os.Args) < 3 {
			log.Fatal("print requires an IP address")
		}
		ip := os.Args[2]
		port := 9100
		if len(os.Args) > 3 {
			fmt.Sscanf(os.Args[3], "%d", &port)
		}
		doPrint(order, ip, port)
	default:
		fmt.Printf("Unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func doPreview(order *escpos.Order) {
	paperChars := 48 // 80mm paper

	// ── Gastro template previews ────────────────────────────────────
	gastro, err := escpos.NewTemplate("gastro", escpos.DefaultGastroTemplate)
	if err != nil {
		log.Fatal(err)
	}

	// PNG preview (scale 2 = recommended)
	if err := gastro.SavePreviewPNG(order, "preview_gastro.png", paperChars, 2); err != nil {
		log.Fatal(err)
	}
	fmt.Println("✓ preview_gastro.png")

	// HTML preview
	if err := gastro.SavePreviewHTML(order, "preview_gastro.html", paperChars); err != nil {
		log.Fatal(err)
	}
	fmt.Println("✓ preview_gastro.html")

	// ── Minimal template ────────────────────────────────────────────
	minimal, _ := escpos.NewTemplate("minimal", escpos.MinimalTemplate)
	minimal.SavePreviewPNG(order, "preview_minimal.png", paperChars, 2)
	minimal.SavePreviewHTML(order, "preview_minimal.html", paperChars)
	fmt.Println("✓ preview_minimal.png + .html")

	// ── Kitchen template ────────────────────────────────────────────
	kitchen, _ := escpos.NewTemplate("kitchen", escpos.KitchenTemplate)
	kitchen.SavePreviewPNG(order, "preview_kitchen.png", paperChars, 2)
	kitchen.SavePreviewHTML(order, "preview_kitchen.html", paperChars)
	fmt.Println("✓ preview_kitchen.png + .html")

	// ── In-memory preview (e.g. for serving via HTTP) ───────────────
	pngBytes, _ := gastro.PreviewPNG(order, paperChars, 2)
	htmlStr, _ := gastro.PreviewHTML(order, paperChars)
	fmt.Printf("✓ In-memory: PNG=%d bytes, HTML=%d bytes\n", len(pngBytes), len(htmlStr))

	fmt.Println("\nOpen the .html files in a browser or view the .png files.")
}

func doPrint(order *escpos.Order, ip string, port int) {
	p, err := escpos.Connect(ip, port)
	if err != nil {
		log.Fatal(err)
	}
	defer p.Close()

	fmt.Println("Printer:", p.GetStatus())

	tmpl, _ := escpos.NewTemplate("gastro", escpos.DefaultGastroTemplate)
	if err := tmpl.Print(p, order); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Printed: %s, Total: %s\n",
		order.OrderNumber, escpos.FormatMoney(order.Subtotal()))
}
