package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

type (
	kafkaStartedMsg struct {
		screen *kafkaRecordsScreen
		gen    int
		ch     <-chan ports.KafkaBatch
		err    error
	}
	kafkaBatchMsg struct {
		screen *kafkaRecordsScreen
		gen    int
		batch  ports.KafkaBatch
		ch     <-chan ports.KafkaBatch
		closed bool
	}
)

// kafkaRecordsScreen shows the records of one topic, oldest first. It
// reads only when asked: the tail when it opens, a window (1…7), or live
// records while following (f).
type kafkaRecordsScreen struct {
	topics        *kafkaTopicsScreen
	topic         ports.KafkaTopicState
	window        domain.TimeWindow
	follow        bool
	readCommitted bool

	gen     int
	cancel  context.CancelFunc
	err     error
	loading bool
	live    bool // the history is loaded
	ended   bool // the read ended (not following)
	notice  string

	buf      *domain.RecordBuffer
	rows     []uint64 // sequence numbers of the records shown (filter applied)
	previews map[uint64]string

	cursor    int  // index in rows
	offset    int  // first displayed position
	tail      bool // the cursor follows the newest record
	newestTop bool // newest record first (o)
	height    int
	paused    bool
	held      []domain.KafkaRecord
	heldBytes int
	maxBytes  int // the buffer's byte bound, also for the records held while paused
	lost      int // records received while paused that did not fit

	filter    domain.RecordFilter
	input     lineEdit
	editing   bool
	filterErr string
}

func newKafkaRecordsScreen(m *Model, topics *kafkaTopicsScreen, t ports.KafkaTopicState) *kafkaRecordsScreen {
	return &kafkaRecordsScreen{
		topics: topics, topic: t, window: domain.TimeWindow{Tail: max(m.opts.KafkaTail, 1)},
		readCommitted: m.opts.KafkaReadCommitted, tail: true,
		buf:      domain.NewRecordBuffer(max(m.opts.KafkaMaxRecords, 1000), max(m.opts.KafkaMaxBytes, 1<<20)),
		maxBytes: max(m.opts.KafkaMaxBytes, 1<<20),
	}
}

func (r *kafkaRecordsScreen) crumbs() []string {
	return []string{"services", r.topics.repo, "kafka", r.topic.Name}
}

func (r *kafkaRecordsScreen) init(m *Model) tea.Cmd { return r.open(m) }

// open (re)starts the read with the current window, follow and
// isolation; the view starts over.
func (r *kafkaRecordsScreen) open(m *Model) tea.Cmd {
	r.close()
	r.gen++
	r.buf.Reset()
	r.rows, r.previews, r.cursor, r.offset, r.tail = nil, map[uint64]string{}, 0, 0, true
	r.err, r.loading, r.live, r.ended, r.notice, r.paused, r.held, r.heldBytes, r.lost = nil, true, false, false, "", false, nil, 0, 0
	sess := r.topics.session
	if sess == nil {
		r.err, r.loading = fmt.Errorf("the Kafka session is closed: %w", domain.ErrUnreachable), false
		return nil
	}
	ctx, cancel := context.WithCancel(m.opts.Context)
	r.cancel = cancel
	gen := r.gen
	q := ports.KafkaQuery{Topic: r.topic.Name, Window: r.window, Follow: r.follow, ReadCommitted: r.readCommitted}
	return func() tea.Msg {
		ch, err := sess.Read(ctx, q)
		return kafkaStartedMsg{screen: r, gen: gen, ch: ch, err: err}
	}
}

func (r *kafkaRecordsScreen) close() {
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
}

func (r *kafkaRecordsScreen) wait(gen int, ch <-chan ports.KafkaBatch) tea.Cmd {
	return func() tea.Msg {
		b, ok := <-ch
		return kafkaBatchMsg{screen: r, gen: gen, batch: b, ch: ch, closed: !ok}
	}
}

func (r *kafkaRecordsScreen) busy(*Model) bool { return r.loading && r.err == nil }

func (r *kafkaRecordsScreen) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	switch msg := msg.(type) {
	case kafkaStartedMsg:
		if msg.screen != r || msg.gen != r.gen {
			return false, nil
		}
		if msg.err != nil {
			r.err, r.loading = msg.err, false
			return true, nil
		}
		return true, r.wait(msg.gen, msg.ch)
	case kafkaBatchMsg:
		if msg.screen != r || msg.gen != r.gen {
			return false, nil
		}
		if msg.closed {
			r.loading, r.ended = false, true
			return true, nil
		}
		r.apply(msg.batch)
		return true, r.wait(msg.gen, msg.ch)
	case tea.MouseWheelMsg:
		switch msg.Button {
		case tea.MouseWheelDown:
			r.move(3)
		case tea.MouseWheelUp:
			r.move(-3)
		}
		return true, nil
	case tea.KeyPressMsg:
		if r.editing {
			return true, r.edit(msg)
		}
		return r.key(m, msg)
	}
	return false, nil
}

// apply stores a batch. While paused, records wait outside the buffer so
// the records on screen stay; at most a buffer's worth is held.
func (r *kafkaRecordsScreen) apply(b ports.KafkaBatch) {
	if len(b.Notices) > 0 {
		r.notice = b.Notices[len(b.Notices)-1]
	}
	if b.Err != nil {
		r.err = b.Err
	}
	if b.HistoryDone {
		r.loading, r.live = false, true
		releaseMemory()
	}
	if r.paused {
		for _, rec := range b.Records {
			r.held = append(r.held, rec)
			r.heldBytes += rec.Bytes()
		}
		// At most a buffer's worth, by count and by bytes: the oldest go.
		drop := 0
		for drop < len(r.held)-1 && (len(r.held)-drop > max(r.buf.Len(), 1000) || r.heldBytes > r.maxBytes) {
			r.heldBytes -= r.held[drop].Bytes()
			drop++
		}
		if drop > 0 {
			clear(r.held[:drop])
			r.held, r.lost = r.held[drop:], r.lost+drop
		}
		return
	}
	r.ingest(b.Records)
}

func (r *kafkaRecordsScreen) ingest(recs []domain.KafkaRecord) {
	before := len(r.rows)
	for _, rec := range recs {
		r.buf.Append(rec)
		last := r.buf.At(r.buf.Len() - 1)
		if r.filter.Match(last) {
			r.rows = append(r.rows, last.Seq)
		}
	}
	if r.newestTop && !r.tail {
		// New records come in above: keep the records being read in place.
		r.offset += len(r.rows) - before
	}
	r.evict()
}

// evict drops rows whose record left the buffer, keeping the cursor on
// the same record.
func (r *kafkaRecordsScreen) evict() {
	first := r.buf.FirstSeq()
	n := 0
	for n < len(r.rows) && r.rows[n] < first {
		delete(r.previews, r.rows[n])
		n++
	}
	if n > 0 {
		r.rows = append(r.rows[:0], r.rows[n:]...)
		r.cursor = max(r.cursor-n, 0)
		if !r.newestTop { // newest first: the rows left keep their screen positions
			r.offset = max(r.offset-n, 0)
		}
	}
	if len(r.previews) > 2*max(r.buf.Len(), 1) { // previews of records hidden by a filter, then evicted
		for seq := range r.previews {
			if seq < first {
				delete(r.previews, seq)
			}
		}
	}
	if r.tail {
		r.cursor = max(len(r.rows)-1, 0)
	}
}

// rebuild applies the filter again, keeping the cursor on the same record
// when it is still shown.
func (r *kafkaRecordsScreen) rebuild() {
	var keep uint64
	if rec, ok := r.at(r.cursor); ok {
		keep = rec.Seq
	}
	r.rows = r.rows[:0]
	r.cursor = 0
	for i := range r.buf.Len() {
		rec := r.buf.At(i)
		if r.filter.Match(rec) {
			if rec.Seq == keep {
				r.cursor = len(r.rows)
			}
			r.rows = append(r.rows, rec.Seq)
		}
	}
	if r.tail || keep == 0 {
		r.cursor = max(len(r.rows)-1, 0)
	}
}

// at returns the record of row i.
func (r *kafkaRecordsScreen) at(i int) (*domain.KafkaRecord, bool) {
	if i < 0 || i >= len(r.rows) {
		return nil, false
	}
	j, ok := r.buf.Index(r.rows[i])
	if !ok {
		return nil, false
	}
	return r.buf.At(j), true
}

// move moves the cursor d positions down the screen, whatever the order.
func (r *kafkaRecordsScreen) move(d int) {
	if r.newestTop {
		d = -d
	}
	r.cursor = max(min(r.cursor+d, len(r.rows)-1), 0)
	r.tail = r.cursor == len(r.rows)-1
}

// display maps a displayed position to an index in rows, and back.
func (r *kafkaRecordsScreen) display(i int) int {
	if r.newestTop {
		return len(r.rows) - 1 - i
	}
	return i
}

func (r *kafkaRecordsScreen) key(m *Model, msg tea.KeyPressMsg) (bool, tea.Cmd) {
	keys, key := m.opts.Keys, msg.String()
	page := max(r.height-1, 1)
	if w, ok := r.windowKey(m, key); ok {
		r.window = w
		m.flash("window " + r.windowLabel())
		return true, r.open(m)
	}
	switch {
	case keys.Is(key, ActDown):
		r.move(1)
	case keys.Is(key, ActUp):
		r.move(-1)
	case keys.Is(key, ActPageDown):
		r.move(page)
	case keys.Is(key, ActPageUp):
		r.move(-page)
	case keys.Is(key, ActTop):
		r.move(-len(r.rows))
	case keys.Is(key, ActBottom):
		r.move(len(r.rows))
	case keys.Is(key, ActOrder):
		r.newestTop = !r.newestTop
		m.flash(map[bool]string{true: "newest first", false: "oldest first"}[r.newestTop])
	case keys.Is(key, ActCopy):
		if rec, ok := r.at(r.cursor); ok {
			return true, copyRecord(m, rec)
		}
	case keys.Is(key, ActOpen):
		if rec, ok := r.at(r.cursor); ok {
			return true, m.push(newKafkaZoomScreen(r, rec.Seq))
		}
	case keys.Is(key, ActFollow):
		r.follow = !r.follow
		m.flash(map[bool]string{true: "follow: live records", false: "follow off"}[r.follow])
		return true, r.open(m)
	case keys.Is(key, ActPause):
		r.paused = !r.paused
		if !r.paused {
			held := r.held
			r.held, r.heldBytes = nil, 0
			r.ingest(held)
		}
		m.flash(map[bool]string{true: "paused", false: "resumed"}[r.paused])
	case keys.Is(key, ActIsolation):
		r.readCommitted = !r.readCommitted
		m.flash("isolation " + r.isolation())
		return true, r.open(m)
	case keys.Is(key, ActRefresh):
		return true, r.open(m)
	case keys.Is(key, ActFilter):
		r.editing = true
	case keys.Is(key, ActBack) && r.input.String() != "":
		r.input.Clear()
		r.setFilter()
	default:
		return false, nil
	}
	return true, nil
}

// windowKey maps 1…7 to the duration presets and 0 to the tail.
func (r *kafkaRecordsScreen) windowKey(m *Model, key string) (domain.TimeWindow, bool) {
	keys := m.opts.Keys
	for i, a := range []Action{ActWindow1, ActWindow2, ActWindow3, ActWindow4, ActWindow5, ActWindow6, ActWindow7} {
		if keys.Is(key, a) {
			return durationPreset(m.opts.Windows, i)
		}
	}
	if keys.Is(key, ActWindowTail) {
		return domain.TimeWindow{Tail: max(m.opts.KafkaTail, 1)}, true
	}
	return domain.TimeWindow{}, false
}

func (r *kafkaRecordsScreen) edit(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "enter":
		r.editing = false
	case "esc":
		r.editing = false
		r.input.Clear()
	default:
		r.input.handle(k)
	}
	r.setFilter()
	return nil
}

func (r *kafkaRecordsScreen) setFilter() {
	f, err := domain.ParseRecordFilter(r.input.String())
	if err != nil {
		r.filterErr = err.Error()
		return
	}
	r.filter, r.filterErr = f, ""
	r.rebuild()
}

func (r *kafkaRecordsScreen) isolation() string {
	if r.readCommitted {
		return "committed"
	}
	return "uncommitted"
}

func (r *kafkaRecordsScreen) windowLabel() string {
	if r.window.IsTail() {
		return fmt.Sprintf("last %d per partition", r.window.Tail)
	}
	return "since " + r.window.Label()
}

// --- rendering ---

func (r *kafkaRecordsScreen) view(m *Model, w, h int) string {
	r.height = h
	t := m.opts.Theme
	switch {
	case r.err != nil && len(r.rows) == 0:
		return centered(t.Bad.Render("Cannot read "+r.topic.Name+": "+errKind(r.err))+"\n\n"+
			t.Dim.Render(wrapErr(r.err, w))+"\n\n"+t.Dim.Render("press ")+t.Key.Render(m.label(ActRefresh))+t.Dim.Render(" to retry"), w, h)
	case r.loading && len(r.rows) == 0:
		return centered(t.Key.Render(m.spinner())+t.Dim.Render(" reading "+r.topic.Name+", "+r.windowLabel()), w, h)
	case len(r.rows) == 0:
		msg := "no record in " + r.topic.Name + ", " + r.windowLabel()
		if !r.filter.Empty() {
			msg = "no record matches the filter"
		}
		return centered(t.Dim.Render(msg)+"\n\n"+t.Dim.Render(r.emptyHint(m)), w, h)
	}
	cur := r.display(r.cursor)
	if cur < r.offset {
		r.offset = cur
	}
	if cur >= r.offset+h {
		r.offset = cur - h + 1
	}
	r.offset = max(min(r.offset, len(r.rows)-h), 0)
	lines := make([]string, 0, h)
	for d := r.offset; d < len(r.rows) && len(lines) < h; d++ {
		rec, ok := r.at(r.display(d))
		if !ok {
			continue
		}
		line := r.line(t, rec, w)
		if d == cur {
			line = t.Selected.Render(ansi.Strip(line) + strings.Repeat(" ", max(w-textWidth(ansi.Strip(line)), 0)))
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (r *kafkaRecordsScreen) emptyHint(m *Model) string {
	return fmt.Sprintf("%s longer window  ·  %s follow  ·  %s back", m.label(ActWindow5), m.label(ActFollow), m.label(ActBack))
}

// line draws one record: time, partition, offset, key, value preview.
func (r *kafkaRecordsScreen) line(t Theme, rec *domain.KafkaRecord, w int) string {
	ts := "-"
	if !rec.Time.IsZero() {
		ts = rec.Time.In(time.Local).Format("15:04:05.000")
	}
	key := domain.PayloadPreview(rec.Key, rec.KeySize, 24)
	head := fmt.Sprintf("%-12s p%-2d #%-8s %-24s ", ts, rec.Partition, strconv.FormatInt(rec.Offset, 10), key)
	room := w - textWidth(head) - 1
	preview, ok := r.previews[rec.Seq]
	if !ok {
		// Computed once per record (the whole value is classified), cut
		// to the widest terminal it may be drawn on.
		preview = domain.PayloadPreview(rec.Value, rec.ValueSize, 400)
		if rec.Value == nil {
			preview = "tombstone"
		}
		r.previews[rec.Seq] = preview
	}
	if textWidth(preview) > room {
		preview = ansi.Truncate(preview, max(room, 1), "…")
	}
	return " " + t.Dim.Render(head) + preview
}

func (r *kafkaRecordsScreen) statusLeft(m *Model) string {
	t := m.opts.Theme
	bar := t.Status
	if m.env.Production {
		bar = t.StatusProd
	}
	chip := "STOPPED"
	switch {
	case r.paused:
		chip = "PAUSED"
	case r.loading:
		chip = "LOADING"
	case r.follow && !r.ended:
		chip = "LIVE"
	}
	parts := []string{r.windowLabel(), fmt.Sprintf("%d records", len(r.rows)), r.isolation()}
	if r.newestTop {
		parts = append(parts, "newest first")
	}
	if n := r.buf.Dropped(); n > 0 {
		parts = append(parts, fmt.Sprintf("%d older records dropped", n))
	}
	if len(r.held) > 0 {
		parts = append(parts, fmt.Sprintf("%d waiting", len(r.held)))
	}
	if r.lost > 0 {
		parts = append(parts, fmt.Sprintf("%d missed while paused", r.lost))
	}
	if f := r.input.String(); f != "" {
		parts = append(parts, "filter "+f)
	}
	if r.err != nil && len(r.rows) > 0 {
		parts = append(parts, "error: "+errKind(r.err))
	}
	if r.notice != "" {
		parts = append(parts, r.notice)
	}
	return t.Chip.Render(chip) + bar.Render("  "+strings.Join(parts, "  ·  "))
}

func (r *kafkaRecordsScreen) hints(m *Model) []hint {
	if r.editing {
		return []hint{{"enter", "keep"}, {"esc", "clear"}, {"ctrl+u", "erase"}}
	}
	return []hint{
		m.h(ActOpen, "zoom"), m.h(ActFilter, "filter"),
		{"1-7", "window"},
		m.h(ActWindowTail, "tail"),
		m.h(ActFollow, "follow"), m.h(ActPause, "pause"), m.h(ActIsolation, "isolation"), m.h(ActCopy, "copy"), m.h(ActBack, "back"),
	}
}

// maxCopyBytes bounds what one copy sends: terminals limit OSC 52.
const maxCopyBytes = 64 << 10

// copyRecord puts a record's value on the clipboard (OSC 52, D-012): the
// bytes as received for text and JSON, a hex dump for binary data. Only
// on this explicit request does a value leave the screen.
func copyRecord(m *Model, rec *domain.KafkaRecord) tea.Cmd {
	where := fmt.Sprintf("p%d #%d", rec.Partition, rec.Offset)
	if rec.Value == nil {
		m.flash("nothing to copy: " + where + " is a tombstone")
		return nil
	}
	var text string
	switch kind, _ := domain.ClassifyPayload(rec.Value); kind {
	case domain.PayloadJSON, domain.PayloadText, domain.PayloadEmpty:
		text = string(rec.Value)
	default:
		text = strings.Join(domain.PayloadLines(rec.Value), "\n")
	}
	note := ""
	if len(text) > maxCopyBytes {
		text, note = text[:maxCopyBytes], ", first "+domain.ByteSize(maxCopyBytes)
	}
	if rec.Truncated() {
		note += ", cut by kafka.max_value_bytes"
	}
	m.flash(fmt.Sprintf("copied the value of %s (%s%s)", where, domain.ByteSize(len(text)), note))
	return tea.SetClipboard(text)
}

func (r *kafkaRecordsScreen) prompt(m *Model) string {
	if !r.editing && r.input.String() == "" {
		return ""
	}
	t := m.opts.Theme
	text := " " + r.input.String()
	if r.editing {
		text += "_"
	}
	if r.filterErr != "" {
		text += "   " + r.filterErr
	}
	return t.Prompt.Render("/") + t.Bold.Inherit(t.Status).Render(text)
}

// ticking: relative information changes while following.
func (r *kafkaRecordsScreen) ticking(*Model) bool { return r.follow && r.live && !r.paused }
