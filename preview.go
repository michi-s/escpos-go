package escpos

import (
	"bytes"
	"fmt"
	"html"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// ---------------------------------------------------------------------------
// Preview line — intermediate representation of a receipt line with styling
// ---------------------------------------------------------------------------

type previewLine struct {
	text      string
	align     byte // 0=left, 1=center, 2=right
	bold      bool
	underline byte // 0=off, 1=single, 2=double
	invert    bool
	sizeW     byte // 1–8
	sizeH     byte // 1–8
	fontSmall bool // font B (smaller)
	lineType  lineType
	data      string // extra data for QR/barcode
}

type lineType int

const (
	lineText    lineType = iota
	lineFeed             // blank line
	lineCut              // dashed cut line
	lineQR               // QR code placeholder
	lineBarcode          // barcode placeholder
)

// effectiveHeight returns the pixel height of this line at the given scale.
func (l previewLine) effectiveHeight(charH, scale int) int {
	switch l.lineType {
	case lineFeed:
		return charH * scale
	case lineCut:
		return 20 * scale
	case lineQR:
		return 80 * scale
	case lineBarcode:
		return 60 * scale
	default:
		return charH * int(l.sizeH) * scale
	}
}

// ---------------------------------------------------------------------------
// Build preview lines from rendered template output
// ---------------------------------------------------------------------------

type previewState struct {
	align     byte
	bold      bool
	underline byte
	invert    bool
	sizeW     byte
	sizeH     byte
	fontSmall bool
}

func buildPreviewLines(rendered string, paperChars int) []previewLine {
	state := previewState{sizeW: 1, sizeH: 1}
	var lines []previewLine

	addLine := func(text string, lt lineType, data string) {
		lines = append(lines, previewLine{
			text:      text,
			align:     state.align,
			bold:      state.bold,
			underline: state.underline,
			invert:    state.invert,
			sizeW:     state.sizeW,
			sizeH:     state.sizeH,
			fontSmall: state.fontSmall,
			lineType:  lt,
			data:      data,
		})
	}

	rawLines := strings.Split(rendered, "\n")
	for _, line := range rawLines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if !strings.HasPrefix(trimmed, "@") {
			addLine(trimmed, lineText, "")
			continue
		}

		parts := strings.SplitN(trimmed, " ", 2)
		cmd := strings.ToUpper(parts[0])
		arg := ""
		if len(parts) > 1 {
			arg = parts[1]
		}

		switch cmd {
		case "@CENTER":
			state.align = 1
		case "@LEFT":
			state.align = 0
		case "@RIGHT":
			state.align = 2
		case "@BOLD":
			state.bold = true
		case "@/BOLD":
			state.bold = false
		case "@UNDERLINE":
			state.underline = 1
		case "@/UNDERLINE":
			state.underline = 0
		case "@INVERT":
			state.invert = true
		case "@/INVERT":
			state.invert = false
		case "@FONT":
			state.fontSmall = (arg == "1")
		case "@SIZE":
			var w, h int
			fmt.Sscanf(arg, "%d %d", &w, &h)
			if w < 1 {
				w = 1
			}
			if h < 1 {
				h = 1
			}
			state.sizeW = byte(w)
			state.sizeH = byte(h)
		case "@RULE":
			ch := "-"
			if len(arg) > 0 {
				ch = string(arg[0])
			}
			addLine(strings.Repeat(ch, paperChars), lineText, "")
		case "@FEED":
			n := 1
			fmt.Sscanf(arg, "%d", &n)
			for i := 0; i < n; i++ {
				addLine("", lineFeed, "")
			}
		case "@TWOCOL":
			cols := strings.SplitN(arg, "|", 2)
			if len(cols) == 2 {
				left := strings.TrimSpace(cols[0])
				right := strings.TrimSpace(cols[1])
				gap := paperChars - len(left) - len(right)
				if gap < 1 {
					gap = 1
				}
				addLine(left+strings.Repeat(" ", gap)+right, lineText, "")
			} else {
				addLine(arg, lineText, "")
			}
		case "@THREECOL":
			cols := strings.SplitN(arg, "|", 3)
			if len(cols) == 3 {
				l := strings.TrimSpace(cols[0])
				c := strings.TrimSpace(cols[1])
				r := strings.TrimSpace(cols[2])
				total := len(l) + len(c) + len(r)
				gap := paperChars - total
				if gap < 2 {
					addLine(l+" "+c+" "+r, lineText, "")
				} else {
					gl := gap / 2
					gr := gap - gl
					addLine(l+strings.Repeat(" ", gl)+c+strings.Repeat(" ", gr)+r, lineText, "")
				}
			} else {
				addLine(arg, lineText, "")
			}
		case "@QRCODE":
			addLine("", lineQR, arg)
		case "@BARCODE":
			addLine("", lineBarcode, arg)
		case "@CUT", "@FULLCUT":
			addLine("", lineCut, "")
		case "@DRAWER", "@BEEP":
			// no visual output
		}
	}

	return lines
}

// ---------------------------------------------------------------------------
// PNG preview
// ---------------------------------------------------------------------------

const (
	baseCharW   = 7  // basicfont.Face7x13 advance
	baseCharH   = 13 // basicfont.Face7x13 height
	baseAscent  = 11 // basicfont.Face7x13 ascent
	lineGap     = 4  // extra pixels between lines (at 1x)
	marginX     = 16 // horizontal margin (at 1x)
	marginY     = 16 // vertical margin (at 1x)
	paperBGGray = 245
)

// PreviewPNG renders the receipt as a PNG image.
// paperChars is the paper width in characters (48 for 80mm, 32 for 58mm).
// scale controls the resolution: 1 = basic, 2 = recommended, 3 = high-res.
func (t *ReceiptTemplate) PreviewPNG(order *Order, paperChars int, scale int) ([]byte, error) {
	rendered, err := t.Render(order)
	if err != nil {
		return nil, err
	}
	if scale < 1 {
		scale = 1
	}

	lines := buildPreviewLines(rendered, paperChars)

	// Compute image dimensions
	imgW := (paperChars*baseCharW + 2*marginX) * scale
	totalH := 2 * marginY
	for _, l := range lines {
		totalH += l.effectiveHeight(baseCharH+lineGap, scale)
	}
	imgH := totalH

	// Create image with paper background
	img := image.NewRGBA(image.Rect(0, 0, imgW, imgH))
	paperColor := color.RGBA{paperBGGray, paperBGGray, paperBGGray, 255}
	draw.Draw(img, img.Bounds(), &image.Uniform{paperColor}, image.Point{}, draw.Src)

	// Draw each line
	y := marginY * scale
	face := basicfont.Face7x13

	for _, l := range lines {
		switch l.lineType {
		case lineFeed:
			y += (baseCharH + lineGap) * scale
			continue

		case lineCut:
			drawCutLine(img, y, imgW, scale)
			y += 20 * scale
			continue

		case lineQR:
			drawPlaceholder(img, "[ QR Code ]", y, imgW, 80*scale, scale, face)
			y += 80 * scale
			continue

		case lineBarcode:
			drawPlaceholder(img, "[ Barcode ]", y, imgW, 60*scale, scale, face)
			y += 60 * scale
			continue
		}

		// Regular text line
		lineH := int(l.sizeH) * (baseCharH + lineGap) * scale
		paperW := paperChars * baseCharW * scale

		// For invert: draw black background
		if l.invert {
			bgRect := image.Rect(marginX*scale, y, marginX*scale+paperW, y+int(l.sizeH)*baseCharH*scale)
			draw.Draw(img, bgRect, &image.Uniform{color.Black}, image.Point{}, draw.Src)
		}

		// Render text at 1x into temp image, then scale
		textLen := len(l.text)
		if textLen == 0 {
			y += lineH
			continue
		}

		tmpW := textLen * baseCharW
		tmpH := baseCharH
		tmp := image.NewRGBA(image.Rect(0, 0, tmpW, tmpH))

		// Fill with transparent (or bg color for invert)
		bgCol := color.RGBA{0, 0, 0, 0}
		if l.invert {
			bgCol = color.RGBA{0, 0, 0, 255}
		}
		draw.Draw(tmp, tmp.Bounds(), &image.Uniform{bgCol}, image.Point{}, draw.Src)

		// Draw text
		fgCol := image.Black
		if l.invert {
			fgCol = image.White
		}

		d := &font.Drawer{
			Dst:  tmp,
			Src:  fgCol,
			Face: face,
			Dot:  fixed.P(0, baseAscent),
		}
		d.DrawString(l.text)

		// Bold: draw again offset by 1px
		if l.bold {
			d.Dot = fixed.P(1, baseAscent)
			d.DrawString(l.text)
		}

		// Compute x position based on alignment
		scaledTextW := tmpW * int(l.sizeW) * scale
		var xOff int
		switch l.align {
		case 0: // left
			xOff = marginX * scale
		case 1: // center
			xOff = marginX*scale + (paperW-scaledTextW)/2
		case 2: // right
			xOff = marginX*scale + paperW - scaledTextW
		}
		if xOff < marginX*scale {
			xOff = marginX * scale
		}

		// Scale and blit to main image
		blitScaled(img, tmp, xOff, y, int(l.sizeW)*scale, int(l.sizeH)*scale, l.invert)

		// Underline
		if l.underline > 0 {
			ulY := y + int(l.sizeH)*baseCharH*scale + 1*scale
			for px := xOff; px < xOff+scaledTextW && px < imgW; px++ {
				for t := 0; t < scale; t++ {
					img.Set(px, ulY+t, color.Black)
				}
			}
			if l.underline == 2 {
				ulY2 := ulY + 2*scale
				for px := xOff; px < xOff+scaledTextW && px < imgW; px++ {
					for t := 0; t < scale; t++ {
						img.Set(px, ulY2+t, color.Black)
					}
				}
			}
		}

		y += lineH
	}

	// Crop image to actual content height
	cropped := image.NewRGBA(image.Rect(0, 0, imgW, y+marginY*scale))
	draw.Draw(cropped, cropped.Bounds(), img, image.Point{}, draw.Src)

	var buf bytes.Buffer
	if err := png.Encode(&buf, cropped); err != nil {
		return nil, fmt.Errorf("encode PNG: %w", err)
	}
	return buf.Bytes(), nil
}

// SavePreviewPNG renders the receipt preview and saves it to a file.
func (t *ReceiptTemplate) SavePreviewPNG(order *Order, path string, paperChars int, scale int) error {
	data, err := t.PreviewPNG(order, paperChars, scale)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// blitScaled copies src into dst at (dstX, dstY) with the given pixel scale.
func blitScaled(dst *image.RGBA, src *image.RGBA, dstX, dstY, scaleW, scaleH int, invertBG bool) {
	bounds := src.Bounds()
	transparentBG := !invertBG

	for sy := bounds.Min.Y; sy < bounds.Max.Y; sy++ {
		for sx := bounds.Min.X; sx < bounds.Max.X; sx++ {
			c := src.RGBAAt(sx, sy)

			// Skip transparent/paper-colored pixels
			if transparentBG && c.A == 0 {
				continue
			}
			// Skip black pixels on inverted background (they're the BG)
			if invertBG && c.R == 0 && c.G == 0 && c.B == 0 && c.A == 255 {
				continue
			}

			for dy := 0; dy < scaleH; dy++ {
				for dx := 0; dx < scaleW; dx++ {
					px := dstX + sx*scaleW + dx
					py := dstY + sy*scaleH + dy
					if px >= 0 && py >= 0 && px < dst.Bounds().Max.X && py < dst.Bounds().Max.Y {
						dst.Set(px, py, c)
					}
				}
			}
		}
	}
}

// drawCutLine draws a dashed scissors-cut line.
func drawCutLine(img *image.RGBA, y, imgW, scale int) {
	midY := y + 10*scale
	dashLen := 6 * scale
	gapLen := 4 * scale
	cutColor := color.RGBA{180, 180, 180, 255}

	x := marginX * scale
	for x < imgW-marginX*scale {
		for dx := 0; dx < dashLen && x+dx < imgW-marginX*scale; dx++ {
			for t := 0; t < scale; t++ {
				img.Set(x+dx, midY+t, cutColor)
			}
		}
		x += dashLen + gapLen
	}

	// Scissors symbol: small "✂" approximation
	scissorX := marginX * scale / 2
	for t := 0; t < 3*scale; t++ {
		img.Set(scissorX+t, midY-2*scale+t, cutColor)
		img.Set(scissorX+t, midY+2*scale-t, cutColor)
	}
}

// drawPlaceholder draws a centered placeholder box with a label.
func drawPlaceholder(img *image.RGBA, label string, y, imgW, h, scale int, face font.Face) {
	boxW := 100 * scale
	boxH := h - 10*scale
	boxX := (imgW - boxW) / 2
	boxY := y + 5*scale
	borderColor := color.RGBA{160, 160, 160, 255}
	bgColor := color.RGBA{240, 240, 240, 255}

	// Fill
	for py := boxY; py < boxY+boxH; py++ {
		for px := boxX; px < boxX+boxW; px++ {
			img.Set(px, py, bgColor)
		}
	}
	// Border
	for px := boxX; px < boxX+boxW; px++ {
		for t := 0; t < scale; t++ {
			img.Set(px, boxY+t, borderColor)
			img.Set(px, boxY+boxH-1-t, borderColor)
		}
	}
	for py := boxY; py < boxY+boxH; py++ {
		for t := 0; t < scale; t++ {
			img.Set(boxX+t, py, borderColor)
			img.Set(boxX+boxW-1-t, py, borderColor)
		}
	}

	// Label centered in box
	labelW := len(label) * baseCharW * scale
	labelX := boxX + (boxW-labelW)/2
	labelY := boxY + (boxH-baseCharH*scale)/2

	// Draw label at 1x then scale
	tmpW := len(label) * baseCharW
	tmp := image.NewRGBA(image.Rect(0, 0, tmpW, baseCharH))
	draw.Draw(tmp, tmp.Bounds(), &image.Uniform{color.RGBA{0, 0, 0, 0}}, image.Point{}, draw.Src)
	d := &font.Drawer{
		Dst:  tmp,
		Src:  &image.Uniform{borderColor},
		Face: face,
		Dot:  fixed.P(0, baseAscent),
	}
	d.DrawString(label)
	blitScaled(img, tmp, labelX, labelY, scale, scale, false)
}

// ---------------------------------------------------------------------------
// HTML preview
// ---------------------------------------------------------------------------

// PreviewHTML renders the receipt as a self-contained HTML page.
// paperChars is the paper width in characters (48 for 80mm, 32 for 58mm).
func (t *ReceiptTemplate) PreviewHTML(order *Order, paperChars int) (string, error) {
	rendered, err := t.Render(order)
	if err != nil {
		return "", err
	}

	lines := buildPreviewLines(rendered, paperChars)

	var body strings.Builder
	for _, l := range lines {
		switch l.lineType {
		case lineFeed:
			body.WriteString(`<div class="line feed">&nbsp;</div>` + "\n")
			continue
		case lineCut:
			body.WriteString(`<div class="cut"><span>✂</span></div>` + "\n")
			continue
		case lineQR:
			body.WriteString(fmt.Sprintf(
				`<div class="placeholder">[ QR Code: %s ]</div>`+"\n",
				html.EscapeString(l.data)))
			continue
		case lineBarcode:
			body.WriteString(fmt.Sprintf(
				`<div class="placeholder">[ Barcode: %s ]</div>`+"\n",
				html.EscapeString(l.data)))
			continue
		}

		// Build CSS classes
		classes := []string{"line"}
		switch l.align {
		case 1:
			classes = append(classes, "center")
		case 2:
			classes = append(classes, "right")
		}
		if l.bold {
			classes = append(classes, "bold")
		}
		if l.underline == 1 {
			classes = append(classes, "underline")
		}
		if l.underline == 2 {
			classes = append(classes, "underline-double")
		}
		if l.invert {
			classes = append(classes, "invert")
		}
		if l.fontSmall {
			classes = append(classes, "font-b")
		}

		// Inline style for size scaling
		style := ""
		if l.sizeW > 1 || l.sizeH > 1 {
			scaleX := float64(l.sizeW)
			scaleY := float64(l.sizeH)
			// Use transform to scale from the left origin
			style = fmt.Sprintf(
				` style="transform:scale(%.1f,%.1f);transform-origin:left top;line-height:%.1f;margin-bottom:%.1fpx;display:inline-block"`,
				scaleX, scaleY, scaleY, (scaleY-1)*14)
		}

		escaped := html.EscapeString(l.text)
		// Preserve whitespace runs
		escaped = strings.ReplaceAll(escaped, "  ", " &nbsp;")

		body.WriteString(fmt.Sprintf(`<div class="%s"%s>%s</div>`+"\n",
			strings.Join(classes, " "), style, escaped))
	}

	htmlPage := fmt.Sprintf(`<!DOCTYPE html>
<html lang="de">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Receipt Preview — %s</title>
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body {
    background: #e0e0e0;
    display: flex;
    justify-content: center;
    padding: 30px 10px;
    font-family: sans-serif;
  }
  .receipt {
    background: #fff;
    width: %dch;
    padding: 20px 16px;
    font-family: 'Courier New', 'Consolas', 'Liberation Mono', monospace;
    font-size: 14px;
    line-height: 1.3;
    color: #1a1a1a;
    box-shadow: 0 4px 20px rgba(0,0,0,0.15);
    border-radius: 2px 2px 0 0;
    position: relative;
    /* Torn bottom edge */
    mask-image: linear-gradient(to bottom, black calc(100%% - 12px),transparent);
    -webkit-mask-image: linear-gradient(to bottom, black calc(100%% - 12px),transparent);
  }
  .receipt::before {
    content: '';
    position: absolute;
    top: -8px; left: 0; right: 0;
    height: 8px;
    background: repeating-linear-gradient(
      90deg, transparent, transparent 4px, #fff 4px, #fff 8px
    );
  }
  .line {
    white-space: pre;
    min-height: 1.3em;
    word-break: break-all;
  }
  .feed { height: 1.3em; }
  .center { text-align: center; }
  .right { text-align: right; }
  .bold { font-weight: bold; }
  .underline { text-decoration: underline; }
  .underline-double { text-decoration: underline double; }
  .invert {
    background: #1a1a1a;
    color: #fff;
    padding: 0 4px;
    display: inline-block;
  }
  .font-b { font-size: 11px; }
  .cut {
    border-top: 2px dashed #ccc;
    text-align: left;
    color: #bbb;
    font-size: 12px;
    padding: 4px 0;
    margin: 8px 0;
  }
  .cut span {
    position: relative;
    top: -2px;
  }
  .placeholder {
    border: 2px dashed #ccc;
    border-radius: 4px;
    padding: 16px;
    text-align: center;
    color: #999;
    font-size: 12px;
    margin: 8px 0;
    background: #fafafa;
  }
</style>
</head>
<body>
<div class="receipt">
%s
</div>
</body>
</html>`,
		html.EscapeString(t.Name),
		paperChars+4, // a bit wider than chars for padding
		body.String(),
	)

	return htmlPage, nil
}

// SavePreviewHTML renders the receipt preview and saves it to an HTML file.
func (t *ReceiptTemplate) SavePreviewHTML(order *Order, path string, paperChars int) error {
	data, err := t.PreviewHTML(order, paperChars)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(data), 0644)
}
