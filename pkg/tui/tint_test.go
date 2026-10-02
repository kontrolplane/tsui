package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/kontrolplane/tsui/pkg/tui/styles"
)

// spans renders a cell as text|tone pairs, with bold spans starred, to compare in tests.
func spans(c cell) string {
	parts := make([]string, len(c))
	for i, s := range c {
		star := ""
		if s.Bold {
			star = "*"
		}
		parts[i] = fmt.Sprintf("%s%q:%d", star, s.Text, s.Tone)
	}
	return strings.Join(parts, " ")
}

func joined(c cell) string {
	var b strings.Builder
	for _, s := range c {
		b.WriteString(s.Text)
	}
	return b.String()
}

func TestTint(t *testing.T) {
	F, B, T := styles.ToneFaint, styles.ToneBody, styles.ToneText
	tests := []struct {
		line string
		want cell
	}{
		{`{"level":"info","msg":"hi \"there\"","n":3,"o":{"msg":"x"}}`, cell{
			{Text: `{"level":`, Tone: F}, {Text: `"info"`, Tone: B}, {Text: `,"msg":`, Tone: F},
			{Text: `"hi \"there\""`, Tone: T}, {Text: `,"n":`, Tone: F}, {Text: `3`, Tone: B},
			{Text: `,"o":{"msg":`, Tone: F}, {Text: `"x"`, Tone: B}, {Text: `}}`, Tone: F},
		}},
		{`level=info msg="request done" path=/api status=200`, cell{
			{Text: `level=`, Tone: F}, {Text: `info`, Tone: B}, {Text: ` `, Tone: B}, {Text: `msg=`, Tone: F},
			{Text: `"request done"`, Tone: T}, {Text: ` `, Tone: B}, {Text: `path=`, Tone: F}, {Text: `/api`, Tone: B},
			{Text: ` `, Tone: B}, {Text: `status=`, Tone: F}, {Text: `200`, Tone: B},
		}},
		{`2026-10-01 21:24:45.123 [DEBUG] job done`, cell{
			{Text: `2026-10-01 21:24:45.123 `, Tone: F}, {Text: `[DEBUG] job done`, Tone: B},
		}},
		{`the answer is x=42 according to the docs`, cell{{Text: `the answer is x=42 according to the docs`, Tone: B}}},
		{`{"broken":`, cell{{Text: `{"broken":`, Tone: F}}},
	}
	for _, tt := range tests {
		got := tint(tt.line)
		if spans(got) != spans(tt.want) {
			t.Errorf("tint(%s)\n got %s\nwant %s", tt.line, spans(got), spans(tt.want))
		}
		if joined(got) != tt.line {
			t.Errorf("tint(%s) changed the text to %s", tt.line, joined(got))
		}
	}
}

func TestPayloadPreviewIsTinted(t *testing.T) {
	c := payloadPreview([]byte("{\n  \"msg\": \"order placed\",\n  \"id\": 3\n}"))
	if got := joined(c); got != `{ "msg": "order placed", "id": 3 }` {
		t.Errorf("preview = %q", got)
	}
	tones := map[string]styles.Tone{}
	for _, s := range c {
		tones[strings.TrimSpace(s.Text)] = s.Tone
	}
	if tones[`"order placed"`] != styles.ToneText || tones["3"] != styles.ToneBody {
		t.Errorf("the message and values should stand out, got %s", spans(c))
	}
	if c := payloadPreview([]byte("plain\x1btext")); strings.ContainsRune(joined(c), 0x1b) {
		t.Errorf("control characters should be escaped, got %q", joined(c))
	}
}
