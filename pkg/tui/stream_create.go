package tui

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"

	"github.com/kontrolplane/tsui/pkg/tsui"
	"github.com/kontrolplane/tsui/pkg/tui/commands"
	"github.com/kontrolplane/tsui/pkg/tui/styles"
)

// Stream names may not contain whitespace, `.`, `*`, `>`, path separators or non-printable characters.
var streamNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

const (
	flagAllowDirect = "allow_direct"
	flagDenyDelete  = "deny_delete"
	flagDenyPurge   = "deny_purge"
)

// streamCreateInput holds form field values during stream creation.
type streamCreateInput struct {
	name              string
	subjects          string
	description       string
	storage           string
	retention         string
	discard           string
	maxAge            string
	maxMsgs           string
	maxBytes          string
	maxMsgsPerSubject string
	maxMsgSize        string
	replicas          int
	duplicates        string
	flags             []string
}

type streamCreateState struct {
	input       *streamCreateInput
	form        *huh.Form
	stepOf      map[huh.Field]int
	currentStep int
}

// dirty reports whether anything was typed that leaving the form would lose.
func (in *streamCreateInput) dirty() bool {
	for _, v := range []string{in.name, in.subjects, in.description, in.maxAge, in.maxMsgs, in.maxBytes, in.maxMsgsPerSubject, in.maxMsgSize, in.duplicates} {
		if strings.TrimSpace(v) != "" {
			return true
		}
	}
	return false
}

// formWidth leaves the form room on both sides, like the other pages, up to 100 columns.
func formWidth() int { return min(100, contentWidth-8) }

// formHeight is the height the form gets below its step header.
func formHeight() int { return contentHeight - 4 }

var formSteps = []string{"basic", "retention", "limits", "advanced"}

// replicaOptions offers replication only when the server runs in a cluster.
func replicaOptions(clustered bool) (string, []huh.Option[int]) {
	if !clustered {
		return "a single server keeps one copy", huh.NewOptions(1)
	}
	return "number of copies kept in the cluster", huh.NewOptions(1, 3, 5)
}

// newStreamCreateForm builds the form, and maps each field to the index of its step in formSteps.
func newStreamCreateForm(input *streamCreateInput, clustered bool) (*huh.Form, map[huh.Field]int) {
	replicasDescription, replicas := replicaOptions(clustered)
	steps := [][]huh.Field{
		{
			huh.NewInput().
				Title("stream name").
				Description("letters, digits, hyphens and underscores").
				Placeholder("ORDERS").
				Value(&input.name).
				Validate(validateStreamName),

			huh.NewInput().
				Title("subjects").
				Description("comma separated, wildcards allowed. defaults to <name lowercased>.> when empty").
				Placeholder("orders.>").
				Value(&input.subjects).
				Validate(validateSubjects),

			huh.NewInput().
				Title("description").
				Description("optional").
				Value(&input.description),

			huh.NewSelect[string]().
				Title("storage").
				Options(
					huh.NewOption("file", "file"),
					huh.NewOption("memory", "memory"),
				).
				Value(&input.storage),
		},
		{
			huh.NewSelect[string]().
				Title("retention policy").
				Description("when messages are removed from the stream").
				Options(
					huh.NewOption("limits: keep messages until a limit is reached", "limits"),
					huh.NewOption("interest: keep messages until all consumers acknowledged them", "interest"),
					huh.NewOption("work queue: remove messages once acknowledged by a consumer", "workqueue"),
				).
				Value(&input.retention),

			huh.NewSelect[string]().
				Title("discard policy").
				Description("what happens when a limit is reached").
				Options(
					huh.NewOption("old: remove the oldest messages", "old"),
					huh.NewOption("new: reject new messages", "new"),
				).
				Value(&input.discard),

			huh.NewInput().
				Title("maximum age").
				Description("e.g. 1h, 24h, 7d. empty for unlimited").
				Placeholder("unlimited").
				Value(&input.maxAge).
				Validate(validateOptional(parseDuration)),
		},
		{
			huh.NewInput().
				Title("maximum messages").
				Description("empty for unlimited").
				Placeholder("unlimited").
				Value(&input.maxMsgs).
				Validate(validateOptional(parsePositiveInt)),

			huh.NewInput().
				Title("maximum bytes").
				Description("e.g. 512MB, 1GB. empty for unlimited").
				Placeholder("unlimited").
				Value(&input.maxBytes).
				Validate(validateOptional(parseBytes)),

			huh.NewInput().
				Title("maximum messages per subject").
				Description("empty for unlimited").
				Placeholder("unlimited").
				Value(&input.maxMsgsPerSubject).
				Validate(validateOptional(parsePositiveInt)),

			huh.NewInput().
				Title("maximum message size").
				Description("e.g. 1MB. empty for unlimited").
				Placeholder("unlimited").
				Value(&input.maxMsgSize).
				Validate(validateOptional(parseBytes)),
		},
		{
			huh.NewSelect[int]().
				Title("replicas").
				Description(replicasDescription).
				Options(replicas...).
				Value(&input.replicas),

			huh.NewInput().
				Title("duplicate window").
				Description("window for Nats-Msg-Id based deduplication, e.g. 2m. at most the maximum age").
				Placeholder("2m").
				Value(&input.duplicates).
				Validate(func(s string) error { return validateDuplicates(s, input.maxAge) }),

			huh.NewMultiSelect[string]().
				Title("options").
				Description("deny delete and deny purge cannot be turned off once the stream exists").
				Options(
					huh.NewOption("allow direct get", flagAllowDirect),
					huh.NewOption("deny message deletion", flagDenyDelete),
					huh.NewOption("deny purge", flagDenyPurge),
				).
				Height(5).
				Value(&input.flags),
		},
	}

	stepOf := map[huh.Field]int{}
	groups := make([]*huh.Group, len(steps))
	for i, fields := range steps {
		for _, f := range fields {
			stepOf[f] = i
		}
		groups[i] = huh.NewGroup(fields...).Title(formSteps[i])
	}
	form := huh.NewForm(groups...).
		WithTheme(styles.FormTheme()).
		WithShowHelp(false).
		WithWidth(formWidth()).
		WithHeight(formHeight()).
		WithShowErrors(false)
	return form, stepOf
}

func validateStreamName(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return fmt.Errorf("stream name is required")
	}
	if len(s) > 255 {
		return fmt.Errorf("stream name must be 255 characters or less")
	}
	if !streamNameRegex.MatchString(s) {
		return fmt.Errorf("only alphanumeric characters, hyphens, and underscores allowed")
	}
	return nil
}

func splitSubjects(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' })
}

func validateSubjects(s string) error {
	for _, subject := range splitSubjects(s) {
		for i, token := range strings.Split(subject, ".") {
			if token == "" {
				return fmt.Errorf("subject %q contains an empty token", subject)
			}
			if token == ">" && i != len(strings.Split(subject, "."))-1 {
				return fmt.Errorf("'>' must be the last token in %q", subject)
			}
		}
	}
	return nil
}

func validateOptional[T any](parse func(string) (T, error)) func(string) error {
	return func(s string) error {
		var v T
		return parseOptional(s, parse, &v)
	}
}

// parseOptional parses s into dst, leaving dst as is when s is empty.
func parseOptional[T any](s string, parse func(string) (T, error), dst *T) error {
	if s = strings.TrimSpace(s); s == "" {
		return nil
	}
	v, err := parse(s)
	if err != nil {
		return err
	}
	*dst = v
	return nil
}

// validateDuplicates checks the duplicate window, which the server refuses to be longer than the maximum age.
func validateDuplicates(duplicates, maxAge string) error {
	var window, age time.Duration
	if err := parseOptional(duplicates, parseDuration, &window); err != nil {
		return err
	}
	if err := parseOptional(maxAge, parseDuration, &age); err != nil {
		return nil
	}
	if window > 0 && age > 0 && window > age {
		return fmt.Errorf("the duplicate window must not be longer than the maximum age (%s)", formatDuration(age))
	}
	return nil
}

func parsePositiveInt(s string) (int64, error) {
	n, err := strconv.ParseInt(strings.ReplaceAll(s, ",", ""), 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("must be a positive number")
	}
	return n, nil
}

// parseDuration extends time.ParseDuration with a `d` suffix for days.
func parseDuration(s string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("must be a duration like 1h, 24h or 7d")
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("must be a duration like 1h, 24h or 7d")
	}
	return d, nil
}

var byteUnits = []struct {
	suffix string
	factor int64
}{
	{"TIB", 1 << 40}, {"GIB", 1 << 30}, {"MIB", 1 << 20}, {"KIB", 1 << 10},
	{"TB", 1 << 40}, {"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10},
	{"T", 1 << 40}, {"G", 1 << 30}, {"M", 1 << 20}, {"K", 1 << 10},
	{"B", 1},
}

// parseBytes parses sizes like 512, 512MB or 1GiB. Units are powers of 1024, matching the nats cli.
func parseBytes(s string) (int64, error) {
	upper := strings.ToUpper(strings.ReplaceAll(s, " ", ""))
	factor := int64(1)
	for _, u := range byteUnits {
		if v, ok := strings.CutSuffix(upper, u.suffix); ok {
			upper, factor = v, u.factor
			break
		}
	}
	n, err := strconv.ParseInt(upper, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("must be a size like 1024, 512MB or 1GB")
	}
	if n > math.MaxInt64/factor {
		return 0, fmt.Errorf("size is too large")
	}
	return n * factor, nil
}

func (m model) StreamCreateSwitchPage() (model, tea.Cmd) {
	m.error = ""
	m.armed = false
	m.state.streamCreate.input = &streamCreateInput{
		storage:   "file",
		retention: "limits",
		discard:   "old",
		replicas:  1,
		flags:     []string{flagAllowDirect},
	}
	return m.openStreamCreateForm()
}

// openStreamCreateForm shows a new form on the current input, which keeps what was entered.
func (m model) openStreamCreateForm() (model, tea.Cmd) {
	c := &m.state.streamCreate
	c.form, c.stepOf = newStreamCreateForm(c.input, m.server.Cluster != "")
	c.currentStep = 0
	return m.SwitchPage(streamCreate), c.form.Init()
}

// reopenStreamCreate returns to the form after the stream could not be created, keeping the input.
func (m model) reopenStreamCreate(reason string) (model, tea.Cmd) {
	m, cmd := m.openStreamCreateForm()
	m.error = reason
	return m, cmd
}

func (m model) StreamCreateView() string {
	if m.state.streamCreate.form == nil {
		return "loading…"
	}
	content := lipgloss.JoinVertical(lipgloss.Left,
		m.renderFormHeader(),
		m.state.streamCreate.form.View(),
	)
	return lipgloss.Place(contentWidth, contentHeight, lipgloss.Center, lipgloss.Top, content)
}

// renderFormHeader renders the progress indicator showing the current form step.
func (m model) renderFormHeader() string {
	var steps []string
	for i, step := range formSteps {
		switch {
		case i < m.state.streamCreate.currentStep:
			steps = append(steps, styles.Render(styles.S("✓ ", styles.ToneAccent), styles.S(step, styles.ToneMuted)))
		case i == m.state.streamCreate.currentStep:
			steps = append(steps, styles.Render(styles.B("● ", styles.ToneAccent), styles.B(step, styles.ToneText)))
		default:
			steps = append(steps, styles.Render(styles.S("○ ", styles.ToneFaint), styles.S(step, styles.ToneFaint)))
		}
	}
	line := strings.Join(steps, styles.Faint("   ───   "))
	return lipgloss.JoinVertical(lipgloss.Center,
		lipgloss.PlaceHorizontal(formWidth(), lipgloss.Center, line),
		lipgloss.NewStyle().Foreground(styles.P.Rule).Render(strings.Repeat("─", formWidth())),
		lipgloss.PlaceHorizontal(formWidth(), lipgloss.Left, m.renderFormError()),
	)
}

// renderFormError shows why the form does not move on. huh adds its errors below a group sized
// without them, where they are cut off, so the line the header keeps free shows them instead.
func (m model) renderFormError() string {
	f := m.state.streamCreate.form
	if f == nil {
		return ""
	}
	errs := f.Errors()
	if len(errs) == 0 {
		return ""
	}
	return styles.Render(styles.S("✗ ", styles.ToneDanger), styles.S(truncate(errs[0].Error(), formWidth()-2), styles.ToneDanger))
}

func (m model) StreamCreateUpdate(msg tea.Msg) (model, tea.Cmd) {
	c := &m.state.streamCreate
	if c.form == nil || m.loading {
		return m, nil
	}

	if keyMsg, ok := msg.(tea.KeyPressMsg); ok && key.Matches(keyMsg, m.keys.Back) {
		if c.input.dirty() && !m.armed {
			m.armed = true
			return m.setStatus(discardPrompt, styles.ToneWarning)
		}
		return m.StreamOverviewGoBack()
	}

	form, cmd := c.form.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		c.form = f
		c.currentStep = c.stepOf[f.GetFocusedField()]
	}

	switch c.form.State {
	case huh.StateCompleted:
		return m.submitStreamCreate()
	case huh.StateAborted:
		return m.StreamOverviewGoBack()
	}

	return m, cmd
}

// buildStreamConfig converts the form input into a stream configuration.
func buildStreamConfig(input *streamCreateInput) (tsui.StreamConfig, error) {
	name := strings.TrimSpace(input.name)
	if err := validateStreamName(name); err != nil {
		return tsui.StreamConfig{}, err
	}

	subjects := splitSubjects(input.subjects)
	if len(subjects) == 0 {
		subjects = []string{strings.ToLower(name) + ".>"}
	}

	cfg := tsui.StreamConfig{
		Name:              name,
		Description:       strings.TrimSpace(input.description),
		Subjects:          subjects,
		Storage:           input.storage,
		Retention:         input.retention,
		Discard:           input.discard,
		Replicas:          input.replicas,
		MaxMsgs:           -1,
		MaxBytes:          -1,
		MaxMsgsPerSubject: -1,
		MaxMsgSize:        -1,
	}

	var maxMsgSize int64
	for _, err := range []error{
		parseOptional(input.maxAge, parseDuration, &cfg.MaxAge),
		parseOptional(input.duplicates, parseDuration, &cfg.Duplicates),
		parseOptional(input.maxMsgs, parsePositiveInt, &cfg.MaxMsgs),
		parseOptional(input.maxMsgsPerSubject, parsePositiveInt, &cfg.MaxMsgsPerSubject),
		parseOptional(input.maxBytes, parseBytes, &cfg.MaxBytes),
		parseOptional(input.maxMsgSize, parseBytes, &maxMsgSize),
		validateDuplicates(input.duplicates, input.maxAge),
	} {
		if err != nil {
			return cfg, err
		}
	}
	if maxMsgSize > math.MaxInt32 {
		return cfg, fmt.Errorf("maximum message size is too large")
	}
	if maxMsgSize > 0 {
		cfg.MaxMsgSize = int32(maxMsgSize)
	}

	for _, f := range input.flags {
		switch f {
		case flagAllowDirect:
			cfg.AllowDirect = true
		case flagDenyDelete:
			cfg.DenyDelete = true
		case flagDenyPurge:
			cfg.DenyPurge = true
		}
	}

	return cfg, nil
}

func (m model) submitStreamCreate() (model, tea.Cmd) {
	cfg, err := buildStreamConfig(m.state.streamCreate.input)
	if err != nil {
		return m.reopenStreamCreate(err.Error())
	}
	if m.disconnected() {
		return m.reopenStreamCreate(notConnected("created"))
	}

	m.busy, m.loading, m.loadingMsg = true, true, "creating stream…"
	return m, commands.CreateStream(m.context, m.js, cfg)
}
