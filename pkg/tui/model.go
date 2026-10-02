package tui

import (
	"context"
	"time"

	"charm.land/bubbles/v2/spinner"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/kontrolplane/tsui/pkg/client"
	keys "github.com/kontrolplane/tsui/pkg/keys"
	"github.com/kontrolplane/tsui/pkg/tsui"
	"github.com/kontrolplane/tsui/pkg/tui/styles"
)

// Layout constants for consistent sizing across all views
const (
	attributesHeight = 6   // Attributes plus spacing in the details view
	tabBarHeight     = 2   // Tab bar plus spacing above the details table
	tableHeaderRows  = 2   // Column titles and the rule under them
	minTableHeight   = 5   // Minimum body rows for any table
	minContentWidth  = 100 // Below this, or minContentHeight, a notice asks for a larger terminal
	minContentHeight = 16
	chromeWidth      = 2 // Frame borders
	chromeHeight     = 8 // Header, spacing, frame edges and padding, footer
)

// The content area fills the terminal. setLayout resizes it and everything derived from it.
var (
	contentWidth  = 140
	contentHeight = 25
	frameWidth    = contentWidth + chromeWidth
)

func setLayout(width, height int) {
	contentWidth = max(minContentWidth, width-chromeWidth)
	contentHeight = max(minContentHeight, height-chromeHeight)
	frameWidth = contentWidth + chromeWidth
	setPanelLayout()
}

// resize fits the tables, viewports and inputs of every page to the current layout.
func (m model) resize() model {
	o := &m.state.streamOverview
	o.table.setSize(contentWidth-4, contentHeight-tableHeaderRows)

	d := &m.state.streamDetails
	for _, t := range []*dataTable{&d.messagesTable, &d.consumersTable, &d.subjectsTable} {
		t.setSize(contentWidth-4, m.getDetailsTableHeight())
	}
	if d.attributesTable != "" {
		d.attributesTable = renderAttributesTable(d.stream)
	}

	// The viewport and inputs are built when their page opens, so only the open one needs fitting.
	switch m.page {
	case messageDetails:
		m.resizeMessageDetails()
	case messagePublish:
		p := &m.state.messagePublish
		p.subject.SetWidth(leftContentWidth - 4)
		p.headers.SetWidth(leftContentWidth)
		p.body.SetWidth(rightContentWidth)
		p.body.SetHeight(publishBodyHeight())
	case streamCreate:
		if f := m.state.streamCreate.form; f != nil {
			f.WithWidth(formWidth()).WithHeight(formHeight())
		}
	case streamDelete:
		c := &m.state.streamDelete.confirm
		c.input.SetWidth(typedConfirmWidth(c.want))
	case streamPurge:
		c := &m.state.streamPurge.confirm
		c.input.SetWidth(typedConfirmWidth(c.want))
	}
	return m
}

type model struct {
	projectName string
	programName string
	page        page
	state       state
	conn        *nats.Conn
	js          jetstream.JetStream
	info        client.Info
	server      tsui.Server
	serverOK    bool
	rttOK       bool
	context     context.Context
	width       int
	height      int
	keys        keys.KeyMap
	showHelp    bool
	error       string
	loading     bool
	loadingMsg  string
	spinner     spinner.Model
	spinning    bool
	statusMsg   string
	statusTone  styles.Tone
	statusGen   int
	refreshGen  int
	busy        bool   // a create, delete, purge or publish is in flight
	armed       bool   // esc was pressed once on a form with input, the next esc discards it
	msgsGen     uint64 // last message load issued
	msgsApplied uint64 // last message load shown
	olderGen    uint64 // older page load in flight
	consGen     uint64 // last consumer list load issued
	consApplied uint64 // last consumer list load shown
	paused      bool   // the refresh ticks are stopped
	refreshedAt time.Time
	clocking    bool // a tick for the time since the last refresh is pending
}

// getDetailsTableHeight returns the body rows of the tables in the details view.
func (m model) getDetailsTableHeight() int {
	available := contentHeight - attributesHeight - tabBarHeight - tableHeaderRows
	if available < minTableHeight {
		return minTableHeight
	}
	return available
}

type state struct {
	streamOverview  streamOverviewState
	streamDetails   streamDetailsState
	streamCreate    streamCreateState
	streamDelete    streamDeleteState
	streamPurge     streamPurgeState
	messageDetails  messageDetailsState
	messagePublish  messagePublishState
	messageDelete   messageDeleteState
	consumerDetails consumerDetailsState
	consumerDelete  consumerDeleteState
}
