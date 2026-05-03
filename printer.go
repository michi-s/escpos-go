// Package escpos provides a driver for ESC/POS thermal receipt printers
// connected over TCP/LAN.
//
// Basic usage:
//
//	p, err := escpos.Connect("192.168.1.100", 9100)
//	if err != nil { log.Fatal(err) }
//	defer p.Close()
//
//	p.PrintTest()
//
//	tmpl, _ := escpos.NewTemplate("receipt", escpos.DefaultGastroTemplate)
//	order := escpos.NewDemoOrder()
//	tmpl.Print(p, order)
package escpos

import (
	"fmt"
	"image"
	"image/color"
	"log"
	"net"
	"strings"
	"time"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
)

// ---------------------------------------------------------------------------
// ESC/POS command constants
// ---------------------------------------------------------------------------

const (
	esc = 0x1B
	gs  = 0x1D
	fs  = 0x1C
	dle = 0x10
	lf  = 0x0A
)

// ---------------------------------------------------------------------------
// Codepage mapping: ESC/POS code page number → Go encoding
// ---------------------------------------------------------------------------

var escposCodePages = map[byte]encoding.Encoding{
	0:  charmap.CodePage437,
	2:  charmap.CodePage850,
	3:  charmap.CodePage860,
	13: charmap.CodePage862,
	16: charmap.Windows1252,
	17: charmap.Windows1251,
	18: charmap.CodePage858,
	19: charmap.Windows1256,
	21: charmap.ISO8859_2,
	25: charmap.ISO8859_15,
	33: charmap.Windows1250,
	37: charmap.ISO8859_7,
	40: charmap.Windows1257,
	47: charmap.ISO8859_1,
}

// ---------------------------------------------------------------------------
// PrinterStatus holds decoded status information.
// ---------------------------------------------------------------------------

// PrinterStatus contains the result of a status query to the printer.
type PrinterStatus struct {
	Connected       bool
	Online          bool
	PaperOK         bool
	CoverClosed     bool
	ErrorState      bool
	StatusSupported bool // true if the printer responded to status queries
	RawPrinter      byte
	RawOffline      byte
	RawError        byte
	RawPaper        byte
	StatusErrors    []string
}

// String returns a human-readable summary of the printer status.
func (s PrinterStatus) String() string {
	if !s.Connected {
		return "DISCONNECTED — TCP connection failed"
	}
	if !s.StatusSupported {
		return "CONNECTED — status queries not supported by this printer (printing works fine)"
	}
	parts := []string{"CONNECTED"}
	if s.Online {
		parts = append(parts, "Online")
	} else {
		parts = append(parts, "OFFLINE")
	}
	if s.PaperOK {
		parts = append(parts, "Paper OK")
	} else {
		parts = append(parts, "PAPER LOW/OUT")
	}
	if s.CoverClosed {
		parts = append(parts, "Cover closed")
	} else {
		parts = append(parts, "COVER OPEN")
	}
	if s.ErrorState {
		parts = append(parts, "ERROR")
	}
	return strings.Join(parts, " | ")
}

// ---------------------------------------------------------------------------
// Printer — ESC/POS driver over TCP
// ---------------------------------------------------------------------------

// Printer represents a connection to an ESC/POS thermal receipt printer.
type Printer struct {
	address    string
	conn       net.Conn
	encoder    *encoding.Encoder
	codepage   byte
	paperWidth int
}

// Connect opens a TCP connection to the thermal printer at the given
// IP address and port (usually 9100).
func Connect(ip string, port int) (*Printer, error) {
	address := fmt.Sprintf("%s:%d", ip, port)
	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", address, err)
	}
	p := &Printer{
		address:    address,
		conn:       conn,
		codepage:   16,
		paperWidth: 48,
	}
	p.Init()
	p.SetCodePage(16)
	return p, nil
}

// Close closes the printer connection.
func (p *Printer) Close() error {
	if p.conn != nil {
		return p.conn.Close()
	}
	return nil
}

// SetPaperWidth sets the number of characters per line.
// Use 48 for 80mm paper (default) or 32 for 58mm paper.
func (p *Printer) SetPaperWidth(w int) {
	p.paperWidth = w
}

// PaperWidth returns the configured paper width in characters.
func (p *Printer) PaperWidth() int {
	return p.paperWidth
}

// Codepage returns the currently active codepage number.
func (p *Printer) Codepage() byte {
	return p.codepage
}

// ---------------------------------------------------------------------------
// Low-level I/O
// ---------------------------------------------------------------------------

func (p *Printer) raw(data ...byte) {
	p.conn.Write(data)
}

func (p *Printer) writeBytes(data []byte) {
	p.conn.Write(data)
}

func (p *Printer) encode(s string) []byte {
	if p.encoder == nil {
		return []byte(s)
	}
	encoded, err := p.encoder.String(s)
	if err != nil {
		var result []byte
		for _, r := range s {
			b, err := p.encoder.Bytes([]byte(string(r)))
			if err != nil {
				result = append(result, '?')
			} else {
				result = append(result, b...)
			}
		}
		return result
	}
	return []byte(encoded)
}

// ---------------------------------------------------------------------------
// Initialization
// ---------------------------------------------------------------------------

// Init resets the printer to default settings (ESC @).
func (p *Printer) Init() {
	p.raw(esc, '@')
}

// ---------------------------------------------------------------------------
// Code page & character set
// ---------------------------------------------------------------------------

// SetCodePage selects the code page on the printer and activates the
// matching Go-side encoder for UTF-8 → single-byte transcoding.
//
// Common values: 0=CP437, 2=CP850, 16=WPC1252 (recommended for German),
// 18=CP858, 25=ISO8859-15, 255=UTF-8 (if printer supports it).
func (p *Printer) SetCodePage(n byte) {
	p.codepage = n
	p.raw(esc, 't', n)

	if n == 255 {
		p.encoder = nil
		return
	}
	if enc, ok := escposCodePages[n]; ok && enc != nil {
		p.encoder = enc.NewEncoder()
	} else {
		log.Printf("warning: no Go encoder for ESC/POS codepage %d, falling back to WPC1252", n)
		p.encoder = charmap.Windows1252.NewEncoder()
	}
}

// SetCharset selects an international character set (ESC R n).
// Common values: 0=USA, 1=France, 2=Germany, 3=UK, 4=Denmark, 7=Spain, 8=Japan.
func (p *Printer) SetCharset(n byte) {
	p.raw(esc, 'R', n)
}

// ---------------------------------------------------------------------------
// Text output
// ---------------------------------------------------------------------------

// Text prints a string followed by a newline.
// The string is transcoded from UTF-8 to the active codepage.
func (p *Printer) Text(s string) {
	p.writeBytes(p.encode(s))
	p.raw(lf)
}

// TextF prints formatted text followed by a newline.
func (p *Printer) TextF(format string, a ...any) {
	p.Text(fmt.Sprintf(format, a...))
}

// Feed advances paper by n lines.
func (p *Printer) Feed(n int) {
	p.raw(esc, 'd', byte(n))
}

// ---------------------------------------------------------------------------
// Text styling
// ---------------------------------------------------------------------------

// Bold enables or disables bold printing.
func (p *Printer) Bold(on bool) {
	if on {
		p.raw(esc, 'E', 1)
	} else {
		p.raw(esc, 'E', 0)
	}
}

// Underline sets underline mode: 0=off, 1=single, 2=double.
func (p *Printer) Underline(mode byte) {
	p.raw(esc, '-', mode)
}

// DoubleStrike enables or disables double-strike printing.
func (p *Printer) DoubleStrike(on bool) {
	if on {
		p.raw(esc, 'G', 1)
	} else {
		p.raw(esc, 'G', 0)
	}
}

// Invert enables or disables white-on-black printing.
func (p *Printer) Invert(on bool) {
	if on {
		p.raw(gs, 'B', 1)
	} else {
		p.raw(gs, 'B', 0)
	}
}

// UpsideDown enables or disables upside-down printing.
func (p *Printer) UpsideDown(on bool) {
	if on {
		p.raw(esc, '{', 1)
	} else {
		p.raw(esc, '{', 0)
	}
}

// Font selects the font: 0=A (12×24), 1=B (9×17).
func (p *Printer) Font(n byte) {
	p.raw(esc, 'M', n)
}

// Size sets character magnification. Width and height are multipliers 1–8.
func (p *Printer) Size(width, height byte) {
	n := ((width - 1) << 4) | (height - 1)
	p.raw(gs, '!', n)
}

// Align sets text justification: 0=left, 1=center, 2=right.
func (p *Printer) Align(n byte) {
	p.raw(esc, 'a', n)
}

// LineSpacing sets the line spacing in dots. Pass 0 to reset to default.
func (p *Printer) LineSpacing(n byte) {
	if n == 0 {
		p.raw(esc, '2')
	} else {
		p.raw(esc, '3', n)
	}
}

// ---------------------------------------------------------------------------
// Layout helpers
// ---------------------------------------------------------------------------

// HRule prints a horizontal rule filling the paper width.
func (p *Printer) HRule(char byte) {
	p.writeBytes([]byte(strings.Repeat(string(char), p.paperWidth)))
	p.raw(lf)
}

// TwoCol prints a left + right justified line across the paper width.
func (p *Printer) TwoCol(left, right string) {
	gap := p.paperWidth - len(left) - len(right)
	if gap < 1 {
		gap = 1
	}
	p.Text(left + strings.Repeat(" ", gap) + right)
}

// ThreeCol prints a left + center + right justified line.
func (p *Printer) ThreeCol(left, center, right string) {
	totalContent := len(left) + len(center) + len(right)
	totalGap := p.paperWidth - totalContent
	if totalGap < 2 {
		p.Text(left + " " + center + " " + right)
		return
	}
	gapLeft := totalGap / 2
	gapRight := totalGap - gapLeft
	p.Text(left + strings.Repeat(" ", gapLeft) + center + strings.Repeat(" ", gapRight) + right)
}

// ---------------------------------------------------------------------------
// Barcode
// ---------------------------------------------------------------------------

// BarcodeHeight sets the barcode height in dots.
func (p *Printer) BarcodeHeight(n byte) { p.raw(gs, 'h', n) }

// BarcodeWidth sets the barcode module width (2–6).
func (p *Printer) BarcodeWidth(n byte) { p.raw(gs, 'w', n) }

// BarcodeHRI sets where to print human-readable text: 0=none, 1=above, 2=below, 3=both.
func (p *Printer) BarcodeHRI(n byte) { p.raw(gs, 'H', n) }

// Barcode prints a 1D barcode.
// typ: 0=UPC-A, 1=UPC-E, 2=EAN13, 3=EAN8, 4=CODE39, 5=ITF, 6=CODABAR, 73=CODE128.
func (p *Printer) Barcode(typ byte, data string) {
	if typ >= 65 {
		p.raw(gs, 'k', typ, byte(len(data)))
		p.writeBytes([]byte(data))
	} else {
		p.raw(gs, 'k', typ)
		p.writeBytes([]byte(data))
		p.raw(0x00)
	}
}

// ---------------------------------------------------------------------------
// QR Code
// ---------------------------------------------------------------------------

// QRCode prints a QR code.
// moduleSize: 1–16 (dot size per module). errorCorrection: 48=L, 49=M, 50=Q, 51=H.
func (p *Printer) QRCode(data string, moduleSize byte, errorCorrection byte) {
	p.raw(gs, '(', 'k', 4, 0, 49, 65, 50, 0)
	p.raw(gs, '(', 'k', 3, 0, 49, 67, moduleSize)
	p.raw(gs, '(', 'k', 3, 0, 49, 69, errorCorrection)
	store := len(data) + 3
	p.raw(gs, '(', 'k', byte(store&0xFF), byte((store>>8)&0xFF), 49, 80, 48)
	p.writeBytes([]byte(data))
	p.raw(gs, '(', 'k', 3, 0, 49, 81, 48)
}

// ---------------------------------------------------------------------------
// Image printing (GS v 0 raster bit-image)
// ---------------------------------------------------------------------------

// PrintImage prints an image using the raster bit-image command.
// The image is converted to 1-bit monochrome and scaled to maxWidthDots.
// Use 384 for 80mm paper or 384 for 58mm paper.
func (p *Printer) PrintImage(img image.Image, maxWidthDots int) {
	bounds := img.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()

	if w > maxWidthDots {
		ratio := float64(maxWidthDots) / float64(w)
		w = maxWidthDots
		h = int(float64(h) * ratio)
	}

	byteWidth := (w + 7) / 8
	w = byteWidth * 8

	raster := make([]byte, byteWidth*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			srcX := bounds.Min.X + x*bounds.Dx()/w
			srcY := bounds.Min.Y + y*bounds.Dy()/h
			if srcX >= bounds.Max.X {
				srcX = bounds.Max.X - 1
			}
			if srcY >= bounds.Max.Y {
				srcY = bounds.Max.Y - 1
			}
			r, g, b, _ := img.At(srcX, srcY).RGBA()
			lum := (299*r + 587*g + 114*b) / 1000
			if lum < 0x8000 {
				raster[y*byteWidth+x/8] |= 1 << uint(7-(x%8))
			}
		}
	}

	p.raw(gs, 'v', '0', 0,
		byte(byteWidth&0xFF), byte((byteWidth>>8)&0xFF),
		byte(h&0xFF), byte((h>>8)&0xFF))
	p.writeBytes(raster)
}

// ---------------------------------------------------------------------------
// Paper cutting
// ---------------------------------------------------------------------------

// Cut performs a full paper cut with a small feed.
func (p *Printer) Cut() {
	p.Feed(3)
	p.raw(gs, 'V', 0)
}

// PartialCut performs a partial paper cut with a small feed.
func (p *Printer) PartialCut() {
	p.Feed(3)
	p.raw(gs, 'V', 1)
}

// ---------------------------------------------------------------------------
// Cash drawer & buzzer
// ---------------------------------------------------------------------------

// OpenDrawer sends a pulse to open the cash drawer.
// pin: 0 (connector pin 2) or 1 (connector pin 5).
func (p *Printer) OpenDrawer(pin byte) {
	p.raw(esc, 'p', pin, 25, 250)
}

// Beep sounds the buzzer n times, each for duration×100ms.
// Note: not all printers support this command.
func (p *Printer) Beep(times, duration byte) {
	p.raw(esc, 'B', times, duration)
}

// ---------------------------------------------------------------------------
// Status queries
// ---------------------------------------------------------------------------

func (p *Printer) pingTCP() bool {
	p.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_, err := p.conn.Write([]byte{esc, '@'})
	p.conn.SetWriteDeadline(time.Time{})
	return err == nil
}

func (p *Printer) readWithTimeout(timeout time.Duration) (byte, error) {
	buf := make([]byte, 1)
	p.conn.SetReadDeadline(time.Now().Add(timeout))
	_, err := p.conn.Read(buf)
	p.conn.SetReadDeadline(time.Time{})
	if err != nil {
		return 0, err
	}
	return buf[0], nil
}

func (p *Printer) drainInput() {
	buf := make([]byte, 64)
	p.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	p.conn.Read(buf)
	p.conn.SetReadDeadline(time.Time{})
}

func (p *Printer) queryDLEEOT(n byte) (byte, error) {
	p.conn.Write([]byte{dle, 0x04, n})
	return p.readWithTimeout(2 * time.Second)
}

func (p *Printer) queryGSr(n byte) (byte, error) {
	p.conn.Write([]byte{gs, 'r', n})
	return p.readWithTimeout(2 * time.Second)
}

// GetStatus queries the printer and returns a decoded PrinterStatus.
// It checks TCP connectivity first, then tries DLE EOT (standard),
// and falls back to GS r. If neither works, the printer is reported
// as connected but without detailed status.
func (p *Printer) GetStatus() PrinterStatus {
	status := PrinterStatus{}

	if !p.pingTCP() {
		status.Connected = false
		status.StatusErrors = append(status.StatusErrors, "TCP write failed — connection lost")
		return status
	}
	status.Connected = true

	p.drainInput()

	// Try DLE EOT
	dleSupported := false
	raw1, err := p.queryDLEEOT(1)
	if err == nil {
		dleSupported = true
		status.StatusSupported = true
		status.RawPrinter = raw1
		status.Online = (raw1 & 0x08) == 0
	}

	if dleSupported {
		raw2, err := p.queryDLEEOT(2)
		if err == nil {
			status.RawOffline = raw2
			status.CoverClosed = (raw2 & 0x04) == 0
		} else {
			status.CoverClosed = true
			status.StatusErrors = append(status.StatusErrors, "offline status query timed out")
		}

		raw3, err := p.queryDLEEOT(3)
		if err == nil {
			status.RawError = raw3
			status.ErrorState = (raw3 & 0x40) != 0
		} else {
			status.StatusErrors = append(status.StatusErrors, "error status query timed out")
		}

		raw4, err := p.queryDLEEOT(4)
		if err == nil {
			status.RawPaper = raw4
			status.PaperOK = (raw4 & 0x60) == 0
		} else {
			status.PaperOK = true
			status.StatusErrors = append(status.StatusErrors, "paper status query timed out")
		}

		return status
	}

	// Fallback: GS r
	status.StatusErrors = append(status.StatusErrors, "DLE EOT not supported")

	raw, err := p.queryGSr(1)
	if err == nil {
		status.StatusSupported = true
		status.RawPaper = raw
		status.PaperOK = (raw & 0x03) == 0
		status.StatusErrors = append(status.StatusErrors, "using GS r fallback")
	}

	if !status.StatusSupported {
		status.StatusErrors = append(status.StatusErrors,
			"GS r also not supported — printer does not support status queries")
		status.Online = true
		status.PaperOK = true
		status.CoverClosed = true
		status.ErrorState = false
	}

	p.Init()
	p.SetCodePage(p.codepage)

	return status
}

// ---------------------------------------------------------------------------
// PrintTest prints a small test page with German characters and styles.
// ---------------------------------------------------------------------------

// PrintTest prints a diagnostic test page covering umlauts, €, text styles,
// alignment, QR code, and a test image.
func (p *Printer) PrintTest() {
	p.Init()
	p.SetCodePage(p.codepage)
	p.SetCharset(2)

	p.Align(1)
	p.Size(2, 2)
	p.Bold(true)
	p.Text("DRUCKERTEST")
	p.Bold(false)
	p.Size(1, 1)
	p.Feed(1)

	p.Align(0)
	p.HRule('-')

	p.Bold(true)
	p.Text("Zeichensatz / Character Set:")
	p.Bold(false)
	p.Text("Umlaute: Ä Ö Ü ä ö ü")
	p.Text("Eszett:  ß")
	p.Text("Euro:    €")
	p.Text("Weitere: £ ¥ § ° ± © ® ™")
	p.Text("French:  é è ê ë à â ç")
	p.Text("Spanish: ñ ¿ ¡")
	p.HRule('-')

	p.Bold(true)
	p.Text("Textstile / Text Styles:")
	p.Bold(false)
	p.Bold(true)
	p.Text("Fettdruck (Bold)")
	p.Bold(false)
	p.Underline(1)
	p.Text("Unterstrichen (Underline)")
	p.Underline(0)
	p.Invert(true)
	p.Text(" Invertiert (Inverted) ")
	p.Invert(false)
	p.Size(2, 2)
	p.Text("Groß (2x2)")
	p.Size(1, 1)
	p.HRule('-')

	p.Bold(true)
	p.Text("Ausrichtung / Alignment:")
	p.Bold(false)
	p.Align(0)
	p.Text("← Links")
	p.Align(1)
	p.Text("— Mitte —")
	p.Align(2)
	p.Text("Rechts →")
	p.Align(0)
	p.HRule('-')

	p.TwoCol("Zweispaltig links", "rechts")
	p.TwoCol("Weißbier 0,5l x2", "7,80 €")
	p.HRule('-')

	p.Align(1)
	p.Text("QR-Code Test:")
	p.QRCode("https://example.com/test", 5, 49)
	p.Feed(1)

	p.Text("Bild-Test:")
	p.PrintImage(generateTestPattern(), 384)
	p.Feed(1)

	p.Align(0)
	p.HRule('=')
	p.Align(1)
	p.TextF("Druckzeit: %s", time.Now().Format("02.01.2006 15:04:05"))
	p.TextF("Codepage: %d", p.codepage)
	p.Text("Test abgeschlossen")
	p.Align(0)

	p.PartialCut()
}

func generateTestPattern() image.Image {
	w, h := 200, 60
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.White)
		}
	}
	for x := 0; x < w; x++ {
		img.Set(x, 0, color.Black)
		img.Set(x, h-1, color.Black)
	}
	for y := 0; y < h; y++ {
		img.Set(0, y, color.Black)
		img.Set(w-1, y, color.Black)
	}
	for i := 0; i < h; i++ {
		img.Set(i*w/h, i, color.Black)
		img.Set(w-1-i*w/h, i, color.Black)
	}
	for y := 15; y < 45; y++ {
		for x := 85; x < 115; x++ {
			img.Set(x, y, color.Black)
		}
	}
	return img
}
