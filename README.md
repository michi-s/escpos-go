# Disclaimer

This code was vibecoded with Claude. However it is tested with a MUNBYN ITPP072.

# escpos — Go library for ESC/POS thermal receipt printers

A zero-fuss Go library for talking to thermal receipt printers over TCP/LAN. Handles UTF-8 → codepage transcoding, text styling, images, barcodes, QR codes, paper cutting, cash drawer, and receipt templating.

## Install

```bash
# In your project:
go get github.com/michi-s/escpos-go

# Or use a local replace directive in your go.mod:
# replace github.com/michi-s/escpos-go => ../escpos
```

## Quick Start

```go
package main

import (
    "log"
    "github.com/michi-s/escpos-go"
)

func main() {
    p, err := escpos.Connect("192.168.1.100", 9100)
    if err != nil { log.Fatal(err) }
    defer p.Close()

    // Print a test page (umlauts, €, styles, QR, image)
    p.PrintTest()

    // Print a receipt
    tmpl, _ := escpos.NewTemplate("receipt", escpos.DefaultGastroTemplate)
    order := escpos.NewDemoOrder()
    tmpl.Print(p, order)
}
```

## API Reference

### Printer

| Method | Description |
|---|---|
| `Connect(ip, port)` | Open TCP connection, returns `*Printer` |
| `p.Close()` | Close connection |
| `p.GetStatus()` | Query printer status → `PrinterStatus` |
| `p.PrintTest()` | Print diagnostic test page |
| `p.SetCodePage(n)` | Set codepage (16=WPC1252 recommended for German) |
| `p.SetCharset(n)` | Set international charset (2=Germany) |
| `p.SetPaperWidth(n)` | Set chars per line (48=80mm, 32=58mm) |

### Text & Styling

| Method | Description |
|---|---|
| `p.Text(s)` | Print text + newline |
| `p.TextF(fmt, args...)` | Print formatted text |
| `p.Feed(n)` | Feed n blank lines |
| `p.Bold(on)` | Bold on/off |
| `p.Underline(mode)` | 0=off, 1=single, 2=double |
| `p.Invert(on)` | White-on-black |
| `p.UpsideDown(on)` | Upside-down |
| `p.DoubleStrike(on)` | Double-strike |
| `p.Font(n)` | 0=A (12×24), 1=B (9×17) |
| `p.Size(w, h)` | Magnification 1–8 each |
| `p.Align(n)` | 0=left, 1=center, 2=right |

### Layout Helpers

| Method | Description |
|---|---|
| `p.HRule(char)` | Full-width horizontal rule |
| `p.TwoCol(left, right)` | Two-column justified line |
| `p.ThreeCol(l, c, r)` | Three-column justified line |

### Barcodes & QR

| Method | Description |
|---|---|
| `p.Barcode(typ, data)` | 1D barcode (2=EAN13, 73=CODE128, etc.) |
| `p.BarcodeHeight(n)` | Barcode height in dots |
| `p.BarcodeWidth(n)` | Module width 2–6 |
| `p.BarcodeHRI(n)` | HRI text position |
| `p.QRCode(data, size, ec)` | QR code (ec: 48=L, 49=M, 50=Q, 51=H) |

### Image, Cut, Drawer

| Method | Description |
|---|---|
| `p.PrintImage(img, maxWidth)` | Print `image.Image` as monochrome raster |
| `p.Cut()` / `p.PartialCut()` | Cut paper |
| `p.OpenDrawer(pin)` | Open cash drawer (pin 0 or 1) |
| `p.Beep(n, dur)` | Buzzer |

### Order

```go
order := &escpos.Order{
    OrderNumber:   "B-00042",
    Timestamp:     time.Now(),
    Waiter:        "Lisa",
    TableNumber:   "7",
    Payment:       escpos.PaymentCash,  // or PaymentCard, PaymentMobile
    AmountPaid:    50.00,
    CustomerCount: 2,
    Items: []escpos.OrderItem{
        {Name: "Weißbier", Quantity: 2, UnitPrice: 3.90, Tax: escpos.TaxNormal},
        {Name: "Brezel",   Quantity: 3, UnitPrice: 1.50, Tax: escpos.TaxReduced},
    },
}

order.Subtotal()       // gross total
order.TaxSummaries()   // grouped by tax rate
order.Change()         // change for cash payments
order.ItemCount()      // total quantity
```

### Templates

Templates combine Go `text/template` syntax (for data) with `@COMMANDS` (for printer control):

```go
tmpl, err := escpos.NewTemplate("myreceipt", `
@CENTER
@SIZE 2 2
@BOLD
MY SHOP
@/BOLD
@SIZE 1 1
@RULE -
{{range .Items}}
@TWOCOL {{fmtQty .Name .Quantity}} | {{money .Total}}
{{end}}
@RULE =
@BOLD
@TWOCOL TOTAL | {{money .Subtotal}}
@/BOLD
@CUT
`)
tmpl.Print(printer, order)
```

**Available @-commands:**

| Command | Effect |
|---|---|
| `@CENTER` / `@LEFT` / `@RIGHT` | Alignment |
| `@BOLD` / `@/BOLD` | Bold on/off |
| `@UNDERLINE` / `@/UNDERLINE` | Underline on/off |
| `@INVERT` / `@/INVERT` | Inverted on/off |
| `@SIZE w h` | Character magnification |
| `@FONT n` | Font selection |
| `@RULE char` | Horizontal rule |
| `@FEED n` | Feed n lines |
| `@TWOCOL left \| right` | Two-column line |
| `@THREECOL l \| c \| r` | Three-column line |
| `@BARCODE type data` | 1D barcode (EAN13, CODE128, CODE39) |
| `@QRCODE size data` | QR code |
| `@CUT` / `@FULLCUT` | Paper cut |
| `@DRAWER` | Open cash drawer |
| `@BEEP n` | Buzzer |

**Template functions:**

| Function | Example output |
|---|---|
| `money .Total` | `12,50 €` |
| `date .Timestamp "02.01.2006"` | `25.04.2026` |
| `fmtQty .Name .Quantity` | `Brezel x3` |
| `pad "hello" 20` | `hello               ` |
| `rpad "hello" 20` | `               hello` |
| `upper "hello"` | `HELLO` |

**Built-in templates:** `DefaultGastroTemplate`, `MinimalTemplate`, `KitchenTemplate`

### Preview (no printer needed)

Preview receipts as PNG images or HTML pages before printing:

```go
tmpl, _ := escpos.NewTemplate("gastro", escpos.DefaultGastroTemplate)
order := escpos.NewDemoOrder()

// Save to files
tmpl.SavePreviewPNG(order, "receipt.png", 48, 2)   // paperChars=48, scale=2
tmpl.SavePreviewHTML(order, "receipt.html", 48)

// Or get bytes/string for serving via HTTP
pngBytes, _ := tmpl.PreviewPNG(order, 48, 2)
htmlString, _ := tmpl.PreviewHTML(order, 48)
```

| Method | Description |
|---|---|
| `tmpl.PreviewPNG(order, paperChars, scale)` | Render to PNG `[]byte` |
| `tmpl.PreviewHTML(order, paperChars)` | Render to HTML `string` |
| `tmpl.SavePreviewPNG(order, path, paperChars, scale)` | Save PNG to file |
| `tmpl.SavePreviewHTML(order, path, paperChars)` | Save HTML to file |

Parameters:
- `paperChars`: characters per line (48 for 80mm paper, 32 for 58mm)
- `scale`: PNG resolution multiplier (1=basic, 2=recommended, 3=high-res)

The HTML preview opens in any browser and looks like a thermal receipt. The PNG preview uses a monospace bitmap font and simulates text styling, sizes, alignment, cut lines, and placeholders for QR/barcodes.

## File Structure

```
escpos/
├── go.mod          # module definition
├── printer.go      # ESC/POS driver (Connect, GetStatus, PrintTest, all commands)
├── order.go        # Order, OrderItem, TaxRate, PaymentMethod
├── template.go     # ReceiptTemplate engine + built-in templates
├── preview.go      # PNG and HTML preview rendering
├── example/
│   └── main.go     # usage example (preview + print)
└── README.md
```
