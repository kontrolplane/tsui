package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/nats-io/nats.go"

	"github.com/kontrolplane/tsui/pkg/tui/styles"
)

// spread places left and right on one line of the given width.
func spread(left, right string, width int) string {
	gap := width - styledWidth(left) - styledWidth(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

const (
	headerIndent = 1
	headerGap    = 2 // the least room between the fact columns
	usageBar     = 10
)

// headerFact is one label and value in the header grid.
type headerFact struct {
	label string
	value cell
}

// factsWidth is the width facts need to show in full.
func factsWidth(facts []headerFact) int {
	labelWidth, valueWidth := 0, 0
	for _, f := range facts {
		labelWidth = max(labelWidth, len(f.label))
		w := 0
		for _, s := range f.value {
			w += ansi.StringWidth(s.Text)
		}
		valueWidth = max(valueWidth, w)
	}
	return labelWidth + 2 + valueWidth
}

// factColumn renders facts as rows with their labels aligned, filling width.
func factColumn(width int, facts ...headerFact) string {
	labelWidth := 0
	for _, f := range facts {
		labelWidth = max(labelWidth, len(f.label))
	}
	lines := make([]string, len(facts))
	for i, f := range facts {
		label := styles.Faint(f.label + strings.Repeat(" ", labelWidth-len(f.label)+2))
		lines[i] = label + renderCell(f.value, column{width: max(1, width-labelWidth-2)}, nil)
	}
	return strings.Join(lines, "\n")
}

// renderHeader draws a grid of connection, server, JetStream and storage facts.
func (m model) renderHeader() string {
	width := frameWidth - headerIndent
	connection, server, storage := m.connectionFacts(), m.serverFacts(), m.storageFacts()
	full, compact := m.jetstreamFacts(formatCount), m.jetstreamFacts(compactCount)

	// The facts change every few seconds at most, while the header is drawn on every frame.
	var key strings.Builder
	fmt.Fprint(&key, width)
	for _, facts := range [][]headerFact{connection, server, full, compact, storage} {
		key.WriteString("\x01")
		for _, f := range facts {
			key.WriteString("\x00" + f.label)
			for _, s := range f.value {
				fmt.Fprintf(&key, "\x00%s\x00%d%v", s.Text, s.Tone, s.Bold)
			}
		}
	}
	return headerMemo.get(key.String(), func() string {
		facts := factsGrid(width, connection, server, full, storage)
		if facts == "" {
			facts = factsGrid(width, connection, server, compact, storage)
		}
		if facts == "" {
			facts = factsGrid(width, nil, server, compact, storage)
		}
		if facts == "" {
			facts = factsGrid(-1, nil, server, compact, storage)
		}
		return lipgloss.NewStyle().MarginLeft(headerIndent).Render(facts)
	})
}

var headerMemo memo

// factsGrid lays the fact columns out across width, or returns "" when they do not fit. Spare
// room goes between the columns so the grid spans the header like the line above it. Without
// any, the server and then the connection column give up width, since their names and urls read
// fine cut short while the numbers do not, but never below a floor that keeps them readable. A nil column
// is left out, and a negative width lays the columns out whether they fit or not.
func factsGrid(width int, columns ...[]headerFact) string {
	columns = slices.DeleteFunc(columns, func(f []headerFact) bool { return f == nil })
	natural := make([]int, len(columns))
	floor := make([]int, len(columns))
	total := 0
	for i, facts := range columns {
		natural[i] = factsWidth(facts)
		floor[i] = natural[i]
		total += natural[i]
	}
	// The last two columns hold numbers, the ones before them names that read fine cut short.
	flexible := len(columns) - 2
	for i := range flexible {
		floor[i] = min(natural[i], factsLabelWidth(columns[i])+2+12)
	}

	gaps := len(columns) - 1
	spare := width - total
	if short := gaps*headerGap - spare; short > 0 && width >= 0 {
		room := 0
		for i := range flexible {
			room += natural[i] - floor[i]
		}
		if short > room {
			return ""
		}
		// The server column gives up its width first, the url is what tells servers apart.
		left := short
		for i := flexible - 1; i >= 0 && left > 0; i-- {
			give := min(left, natural[i]-floor[i])
			natural[i] -= give
			left -= give
		}
		spare = gaps * headerGap
	}
	spare = max(spare, gaps)

	rendered := make([]string, 0, 2*len(columns)-1)
	for i, facts := range columns {
		if i > 0 {
			gap := spare / gaps
			if i <= spare%gaps {
				gap++
			}
			rendered = append(rendered, strings.Repeat(" ", gap))
		}
		rendered = append(rendered, factColumn(natural[i], facts...))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, rendered...)
}

func factsLabelWidth(facts []headerFact) int {
	w := 0
	for _, f := range facts {
		w = max(w, len(f.label))
	}
	return w
}

// pendingValue stands in for facts the server has not reported yet.
var pendingValue = text("…", styles.ToneFaint)

func (m model) connectionFacts() []headerFact {
	return []headerFact{
		{"context", text(orDash(m.info.Context), styles.ToneBody)},
		{"url", text(orDash(m.info.URL), styles.ToneBody)},
	}
}

func (m model) serverFacts() []headerFact {
	if !m.serverOK {
		return []headerFact{{"server", pendingValue}, {"cluster", pendingValue}}
	}
	s := m.server
	name := text(serverName(s.Name), styles.ToneBody)
	if s.Version != "" {
		name = append(name, styles.S("  v"+s.Version, styles.ToneFaint))
	}
	cluster := text("-", styles.ToneFaint)
	if s.Cluster != "" {
		cluster = text(s.Cluster, styles.ToneBody)
	}
	if s.Domain != "" {
		cluster = append(cluster, styles.S("  domain ", styles.ToneFaint), styles.S(s.Domain, styles.ToneBody))
	}
	return []headerFact{{"server", name}, {"cluster", cluster}}
}

// serverName shortens a server named after its id, the 56 character public key a server without
// a name goes by, which says little beyond its first characters.
func serverName(name string) string {
	if len(name) == 56 && strings.HasPrefix(name, "N") && strings.ToUpper(name) == name && !strings.ContainsAny(name, " .-_") {
		return name[:8] + "…"
	}
	return name
}

// jetstreamFacts counts the streams and consumers, written out by format.
func (m model) jetstreamFacts(format func(uint64) string) []headerFact {
	if !m.serverOK {
		return []headerFact{{"streams", pendingValue}, {"consumers", pendingValue}}
	}
	n := func(v int) cell {
		if v == 0 {
			return text("0", styles.ToneFaint)
		}
		return text(format(uint64(v)), styles.ToneBody)
	}
	return []headerFact{{"streams", n(m.server.Streams)}, {"consumers", n(m.server.Consumers)}}
}

func (m model) storageFacts() []headerFact {
	if !m.serverOK {
		return []headerFact{{"file", pendingValue}, {"memory", pendingValue}}
	}
	return []headerFact{
		{"file", usage(m.server.Store, m.server.MaxStore)},
		{"memory", usage(m.server.Memory, m.server.MaxMemory)},
	}
}

// usage renders a meter of used against limit, followed by the numbers. Without a limit the
// meter is left blank so the numbers still line up.
func usage(used uint64, limit int64) cell {
	if limit <= 0 {
		return cell{styles.S(strings.Repeat(" ", usageBar+1), styles.ToneFaint), styles.S(formatBytes(used), styles.ToneBody)}
	}
	ratio := float64(used) / float64(limit)
	tone := styles.ToneAccent
	switch {
	case ratio >= 0.95:
		tone = styles.ToneDanger
	case ratio >= 0.8:
		tone = styles.ToneWarning
	}
	return append(cell(styles.Bar(ratio, usageBar, tone)),
		styles.S(" "+formatBytes(used), styles.ToneBody),
		styles.S(" / "+formatBytes(uint64(limit)), styles.ToneFaint),
	)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%s %s", formatCount(uint64(n)), noun)
	}
	return fmt.Sprintf("%s %ss", formatCount(uint64(n)), noun)
}

// formatRTT renders a round trip time. Zero means the round trip was shorter than the clock
// resolution, which happens against a nearby server on platforms with a coarse clock (Windows).
func formatRTT(d time.Duration) string {
	if d <= 0 {
		return "<1ms"
	}
	if d < time.Millisecond {
		return d.Round(time.Microsecond).String()
	}
	return d.Round(100 * time.Microsecond).String()
}

// connectionState renders the live state of the NATS connection with its round trip time, and
// whether refreshing is paused.
func (m model) connectionState() string {
	status := m.connectionStatus()
	if m.rttOK && m.conn != nil && m.conn.IsConnected() {
		status = append(status, styles.S(" · rtt ", styles.ToneFaint), styles.S(formatRTT(m.server.RTT), styles.ToneBody))
	}
	if m.paused {
		status = append([]styles.Span{styles.S("⏸ paused", styles.ToneWarning), styles.S(" · ", styles.ToneFaint)}, status...)
	}
	return styles.Render(status...)
}

// connectionStatus renders the live state of the NATS connection.
func (m model) connectionStatus() []styles.Span {
	if m.conn == nil {
		return []styles.Span{styles.S("○ offline", styles.ToneMuted)}
	}
	switch s := m.conn.Status(); s {
	case nats.CONNECTED:
		return []styles.Span{styles.S("● ", styles.ToneSuccess), styles.S("connected", styles.ToneBody)}
	case nats.RECONNECTING, nats.CONNECTING:
		return []styles.Span{styles.S("● ", styles.ToneWarning), styles.S("reconnecting", styles.ToneWarning)}
	default:
		return []styles.Span{styles.S("● ", styles.ToneDanger), styles.S(strings.ToLower(s.String()), styles.ToneDanger)}
	}
}

// maxCrumbWidth bounds the crumbs between the first and the last, so a long stream name leaves
// room for the page it leads to.
const maxCrumbWidth = 32

// breadcrumb names where the current page sits, e.g. streams › ORDERS › consumers › billing.
func (m model) breadcrumb() []styles.Span {
	stream := m.state.streamDetails.stream.Name
	var trail []string
	switch m.page {
	case streamOverview:
		trail = []string{"streams"}
	case streamCreate:
		trail = []string{"streams", "new stream"}
	case streamDelete:
		trail = []string{"streams", "delete"}
	case streamPurge:
		trail = []string{"streams", m.state.streamPurge.stream.Name, "purge"}
	case streamDetails:
		trail = []string{"streams", stream}
	case messageDetails:
		trail = []string{"streams", stream, "messages", fmt.Sprintf("#%d", m.state.messageDetails.message.Sequence)}
	case messagePublish:
		trail = []string{"streams", stream, "publish"}
	case messageDelete:
		trail = []string{"streams", stream, "messages", "delete"}
	case consumerDetails:
		trail = []string{"streams", stream, "consumers", m.state.consumerDetails.consumer.Name}
	case consumerDelete:
		trail = []string{"streams", stream, "consumers", "delete"}
	}

	var spans []styles.Span
	for i, t := range trail {
		if i > 0 {
			spans = append(spans, styles.S(" › ", styles.ToneFaint))
		}
		if i > 0 && i < len(trail)-1 {
			t = truncate(t, maxCrumbWidth)
		}
		if i == len(trail)-1 {
			spans = append(spans, styles.B(t, styles.ToneText))
		} else {
			spans = append(spans, styles.S(t, styles.ToneMuted))
		}
	}
	if m.page == streamOverview && len(m.state.streamOverview.streams) > 0 {
		spans = append(spans, styles.S(" "+formatCount(uint64(len(m.state.streamOverview.streams))), styles.ToneFaint))
	}
	return spans
}

// clip cuts s to height lines, ending lines wider than width in an ellipsis.
func clip(s string, width, height int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > height {
		lines = lines[:max(height, 0)]
	}
	for i, line := range lines {
		if styledWidth(line) > width {
			lines[i] = ansi.Truncate(line, width, "…")
		}
	}
	return strings.Join(lines, "\n")
}

// frame draws the rounded border around the content area, with a title set into the top edge
// and meta text into the bottom one.
func frame(title, meta, foot, body string) string {
	border := lipgloss.NewStyle().Foreground(styles.P.Rule)
	edge := func(n int) string {
		if n < 1 {
			n = 1
		}
		return border.Render(strings.Repeat("─", n))
	}

	metaWidth := 0
	if meta != "" {
		meta = " " + ansi.Truncate(meta, frameWidth/3, "…") + " "
		metaWidth = lipgloss.Width(meta)
	}
	title = ansi.Truncate(title, frameWidth-7-metaWidth, "…")
	top := border.Render("╭─ ") + title + " " +
		edge(frameWidth-5-lipgloss.Width(title)-1-metaWidth) + meta + border.Render("─╮")

	footWidth := 0
	if foot != "" {
		foot = " " + ansi.Truncate(foot, frameWidth-6, "…") + " "
		footWidth = lipgloss.Width(foot)
	}
	bottom := border.Render("╰") + edge(frameWidth-3-footWidth) + foot + border.Render("─╯")

	// The body fills the content area: cut to it, with every line centred in it as lipgloss.Place
	// would, and a blank line above and below. Each line is measured once, this runs every frame.
	lines := strings.Split(body, "\n")
	lines = lines[:min(len(lines), contentHeight)]
	side := border.Render("│")
	blank := side + strings.Repeat(" ", contentWidth) + side

	var b strings.Builder
	b.Grow(len(body) + (contentHeight+4)*(contentWidth+40))
	b.WriteString(top)
	b.WriteString("\n")
	b.WriteString(blank)
	for i := range contentHeight {
		b.WriteString("\n")
		b.WriteString(side)
		if i >= len(lines) {
			b.WriteString(strings.Repeat(" ", contentWidth))
		} else {
			line := lines[i]
			w := styledWidth(line)
			if w > contentWidth {
				line = ansi.Truncate(line, contentWidth, "…")
				w = styledWidth(line)
			}
			gap := max(0, contentWidth-w)
			b.WriteString(strings.Repeat(" ", gap/2))
			b.WriteString(line)
			b.WriteString(strings.Repeat(" ", gap-gap/2))
		}
		b.WriteString(side)
	}
	b.WriteString("\n")
	b.WriteString(blank)
	b.WriteString("\n")
	b.WriteString(bottom)
	return b.String()
}
