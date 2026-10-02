package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kontrolplane/tsui/pkg/tui/styles"
)

func TestWidthsMatchAnsi(t *testing.T) {
	plain := []string{"", "GET /orders 200", "naïve café", "日本語のログ", "emoji 🚀 ok", `\x1b escaped`, "tab\tin",
		"e\u0301 combined", "╭─ box ─╮ … ▌ ● ✓", "flag 🇳🇱", "♥\ufe0f"}
	for _, s := range plain {
		if got, want := textWidth(s), ansi.StringWidth(s); got != want {
			t.Errorf("textWidth(%q) = %d, want %d", s, got, want)
		}
		for w := range ansi.StringWidth(s) + 2 {
			if got, want := truncateText(s, w), ansi.Truncate(s, w, ""); got != want {
				t.Errorf("truncateText(%q, %d) = %q, want %q", s, w, got, want)
			}
		}
	}
	styled := append(plain,
		styles.Render(styles.B("bold", styles.ToneAccent), styles.S(" and plain", styles.ToneFaint)),
		pad(4, styles.P.Selection)+styles.S("日本", styles.ToneText).On(styles.P.Selection),
		"\x1b]8;;https://example.com\x07link\x1b]8;;\x07",
		"\x1b[38;2;1;2;3mtruecolor\x1b[m",
		"\x1b[",
	)
	for _, s := range styled {
		if got, want := styledWidth(s), ansi.StringWidth(s); got != want {
			t.Errorf("styledWidth(%q) = %d, want %d", s, got, want)
		}
	}
}

func TestNarrowRunesAreOneColumn(t *testing.T) {
	for _, n := range narrowRanges {
		for r := n[0]; r <= n[1]; r++ {
			if w := ansi.StringWidth(string(r)); w != 1 {
				t.Errorf("%U %q is %d wide", r, r, w)
			}
		}
	}
}

func TestPlaceMatchesLipgloss(t *testing.T) {
	block := "short\n" + styles.Render(styles.B("a longer line │ with ▌ symbols", styles.ToneAccent)) + "\n\nend"
	for _, size := range [][2]int{{40, 10}, {41, 9}, {33, 4}} {
		got := place(size[0], size[1], block)
		want := lipgloss.Place(size[0], size[1], lipgloss.Center, lipgloss.Center, block)
		if got != want {
			t.Errorf("%v:\n%q\nwant\n%q", size, got, want)
		}
	}
}

func TestWrapLineKeepsWordsWhole(t *testing.T) {
	line := `{"customer":"cus_4419","duration_ms":113,"msg":"request served"} and a verylongwordthatdoesnotfitanywhere`
	pieces := wrapLine(line, 24)
	if strings.Join(pieces, "") != line {
		t.Fatalf("pieces %q lost text", pieces)
	}
	want := []string{`{"customer":"cus_4419",`, `"duration_ms":113,`, `"msg":"request served"} `,
		// No break in the second half: cut where the piece is full rather than leave it short.
		`and a verylongwordthatdo`, `esnotfitanywhere`}
	if strings.Join(pieces, "|") != strings.Join(want, "|") {
		t.Errorf("pieces\n%q\nwant\n%q", pieces, want)
	}
	for _, p := range wrapLine("日本語のログ日本語のログ", 5) {
		if w := textWidth(p); w > 5 {
			t.Errorf("piece %q is %d wide", p, w)
		}
	}
}

func TestPayloadWrapsBetweenWords(t *testing.T) {
	p := newPayloadText([]byte(`{"msg":"payment provider unavailable, retrying in a while","id":7}`))
	got := strings.Split(ansi.Strip(p.render(30)), "\n")
	want := []string{
		`{`,
		`  "msg": "payment provider `,
		`unavailable, retrying in a `,
		`while",`,
		`  "id": 7`,
		`}`,
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("payload\n%q\nwant\n%q", got, want)
	}
}
