package escpos

import (
	"strings"
	"testing"
)

func TestBuildPreviewLinesInlineDirectivesMixedWithText(t *testing.T) {
	lines := buildPreviewLines("@LEFT GESAMT: @RIGHT 12,50 EUR @LEFT", 32)
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1 (directives + text on one source line must stay one line): %+v", len(lines), lines)
	}
	l := lines[0]
	if l.text != " GESAMT:  12,50 EUR " {
		t.Fatalf("got text %q", l.text)
	}
	// The last @LEFT before the line ends wins — matches how real ESC/POS
	// hardware applies justification to the whole buffered line at once.
	if l.align != 0 {
		t.Fatalf("got align %d, want 0 (left)", l.align)
	}
	// Only 2 runs, not 3: the trailing "@LEFT" has no text after it on the
	// line, so it doesn't produce an empty third run.
	if len(l.runs) != 2 {
		t.Fatalf("got %d runs, want 2: %+v", len(l.runs), l.runs)
	}
}

func TestBuildPreviewLinesPureDirectiveLineProducesNoLine(t *testing.T) {
	lines := buildPreviewLines("@CENTER\n@BOLD\nHello\n@/BOLD", 32)
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1 (only 'Hello' should produce a visible line): %+v", len(lines), lines)
	}
	if lines[0].text != "Hello" || !lines[0].bold || lines[0].align != 1 {
		t.Fatalf("got %+v", lines[0])
	}
}

func TestBuildPreviewLinesInlineBoldWithinLine(t *testing.T) {
	lines := buildPreviewLines("Status: @BOLD AKTIV @/BOLD - bitte warten", 32)
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1", len(lines))
	}
	l := lines[0]
	if len(l.runs) != 3 {
		t.Fatalf("got %d runs, want 3: %+v", len(l.runs), l.runs)
	}
	if l.runs[0].bold || !l.runs[1].bold || l.runs[2].bold {
		t.Fatalf("bold flags wrong across runs: %+v", l.runs)
	}
	if l.runs[0].text != "Status: " || l.runs[1].text != " AKTIV " || l.runs[2].text != " - bitte warten" {
		t.Fatalf("run text wrong: %+v", l.runs)
	}
}

func TestPreviewHTMLCenterWithSizeDoesNotForceInlineBlockOnOuterDiv(t *testing.T) {
	tpl, err := NewTemplate("t", "@CENTER\n@SIZE 2 2\nGESAMT\n@SIZE 1 1")
	if err != nil {
		t.Fatal(err)
	}
	htmlOut, err := tpl.PreviewHTML(&Order{}, 32)
	if err != nil {
		t.Fatal(err)
	}
	// The outer line div must carry the "center" class and must NOT itself
	// be forced into inline-block (that's what broke centering before this
	// fix — an inline-block div with no text-align on ITS parent just hugs
	// the left edge regardless of its own "center" class).
	if !strings.Contains(htmlOut, `<div class="line center">`) {
		t.Fatalf("expected outer div with exactly 'line center' classes and no inline style, got:\n%s", htmlOut)
	}
	// The scaling transform must be on the inner span instead.
	if !strings.Contains(htmlOut, "transform:scale(2.0,2.0)") {
		t.Fatalf("expected scale transform on inner span, got:\n%s", htmlOut)
	}
}
