package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// Log session plumbing messages, tagged with the session generation.
type (
	logStartedMsg struct {
		screen *logsScreen
		gen    int
		ch     <-chan ports.LogBatch
		err    error
	}
	logBatchMsg struct {
		screen *logsScreen
		gen    int
		batch  ports.LogBatch
		ch     <-chan ports.LogBatch
		closed bool
	}
)

// podIDMode is how pods are identified at the start of each line.
type podIDMode int

const (
	podIDShort podIDMode = iota
	podIDFull
	podIDNone
)

// logsScreen is the logs of one repository (mockup board 3).
type logsScreen struct {
	repo   string
	window domain.TimeWindow
	follow bool
	// previous shows the previous instance of the restarted containers
	// (what explains a crash) instead of the current ones.
	previous bool

	gen     int
	cancel  context.CancelFunc
	err     error
	loading bool

	buf      *domain.LogBuffer
	rows     []viewRow // displayed entries (pod scope and filters applied), oldest first
	pods     []ports.PodState
	podColor map[string]int
	notice   string
	scope    map[string]bool // selected pods; nil means all

	// viewport
	cursor     int  // position in display order
	offset     int  // first displayed position
	tail       bool // cursor sticks to the newest line
	paused     bool
	frozen     int // len(view) when paused
	newestTop  bool
	wrap       bool
	pan        int
	timestamps ports.TimestampMode
	podID      podIDMode
	fullscreen bool
	height     int

	// columns (columns.go)
	hide          ports.ColumnSet // names of columns hidden by the user
	manualColumns bool            // the user chose columns: no automatic narrowing
	focus         bool
	beforeFocus   columnState
	levels        [domain.LevelError + 1]int // view rows by level (context rows excluded)
	live          bool                       // history loaded: new batches are live lines
	rate          rateMeter                  // live lines per second
	idxBuf        []int                      // rebuild scratch buffers, reused
	selBuf        []domain.Row               // across the ~30 rebuilds a second
	cycling       bool                       // c is hiding columns one by one
	beforeCycle   columnState

	// filters (logfilter.go)
	filter    domain.LogFilter
	committed []domain.TextFilter // stacked filters before the one being edited
	input     lineEdit
	regex     bool
	inputErr  string
	editing   bool
	before    string // input before editing, restored by esc
	dirty     bool   // rows must be recomputed (debounced while typing)
	pending   bool   // a debounce tick is scheduled
}

// viewRow is one displayed entry.
type viewRow struct {
	seq     uint64
	level   domain.Level // for the level counts of the status bar
	match   bool         // matches the text filters
	context bool         // shown as context around a match
	gap     bool         // a separator precedes it
}

func newLogsScreen(m *Model, repo string) *logsScreen {
	return (&logsScreen{
		repo: repo, window: m.opts.Window, follow: true, tail: true,
		buf: domain.NewLogBuffer(m.opts.BufferLines), podColor: map[string]int{},
		filter: domain.NewLogFilter(), hide: initialHide(m.opts.Columns),
	}).withColumns(m.opts.LogColumns, m.opts.Columns)
}

func (l *logsScreen) crumbs() []string {
	if l.previous {
		return []string{"services", l.repo, "logs", "previous instance"}
	}
	return []string{"services", l.repo, "logs"}
}

// init opens the session.
func (l *logsScreen) init(m *Model) tea.Cmd { return l.open(m) }

func (l *logsScreen) open(m *Model) tea.Cmd {
	l.close()
	l.gen++
	l.buf.Reset()
	l.rows, l.cursor, l.tail, l.paused, l.err, l.loading, l.notice = nil, 0, true, false, nil, true, ""
	l.levels, l.live, l.rate = [domain.LevelError + 1]int{}, false, rateMeter{}
	if m.opts.Sessions == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(m.opts.Context)
	l.cancel = cancel
	gen, sessions := l.gen, m.opts.Sessions
	q := ports.LogQuery{Env: domain.Env(m.env.Name), Repo: l.repo, Window: l.window, Follow: l.follow, Previous: l.previous}
	return func() tea.Msg {
		ch, err := sessions.Open(ctx, q)
		return logStartedMsg{screen: l, gen: gen, ch: ch, err: err}
	}
}

func (l *logsScreen) close() {
	if l.cancel != nil {
		l.cancel()
		l.cancel = nil
	}
}

func (l *logsScreen) wait(gen int, ch <-chan ports.LogBatch) tea.Cmd {
	return func() tea.Msg {
		b, ok := <-ch
		return logBatchMsg{screen: l, gen: gen, batch: b, ch: ch, closed: !ok}
	}
}

func (l *logsScreen) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	switch msg := msg.(type) {
	case logStartedMsg:
		if msg.screen != l || msg.gen != l.gen {
			return false, nil
		}
		if msg.err != nil {
			l.err, l.loading = msg.err, false
			return true, nil
		}
		return true, l.wait(msg.gen, msg.ch)
	case logBatchMsg:
		if msg.screen != l || msg.gen != l.gen || msg.closed {
			return false, nil
		}
		l.apply(msg.batch, m.opts.Now())
		return true, l.wait(msg.gen, msg.ch)
	case filterTickMsg:
		if msg.screen != l {
			return false, nil
		}
		l.pending = false
		if l.dirty {
			l.rebuild()
		}
		return true, nil
	case tea.MouseWheelMsg:
		switch msg.Button {
		case tea.MouseWheelDown:
			l.scroll(3)
		case tea.MouseWheelUp:
			l.scroll(-3)
		}
		return true, nil
	case tea.KeyPressMsg:
		return l.key(m, msg)
	}
	return false, nil
}

// apply stores a batch: entries go to the buffer, those in scope to the
// view; evicted entries leave the view. now dates the batch for the live
// rate.
func (l *logsScreen) apply(b ports.LogBatch, now time.Time) {
	if l.live {
		l.rate.add(now, len(b.Entries))
	}
	added := 0
	for _, e := range b.Entries {
		seq := l.buf.Append(e)
		if !l.inScope(e.Pod) || l.dirty {
			continue
		}
		if l.needsFullSelect() {
			l.dirty = true // context rows depend on neighbours
			continue
		}
		i, _ := l.buf.Index(seq)
		if r, ok := l.rowFor(seq, l.buf.At(i)); ok {
			l.rows = append(l.rows, r)
			l.count(r, 1)
			added++
		}
	}
	if l.dirty && !l.editing {
		l.rebuild()
	}
	if l.newestTop && !l.tail && !l.paused {
		// New entries are inserted above: keep the same entries on screen.
		l.cursor += added
		l.offset += added
	}
	if b.Pods != nil {
		l.pods = b.Pods
		for _, p := range b.Pods {
			if _, ok := l.podColor[p.Pod.Name]; !ok {
				l.podColor[p.Pod.Name] = len(l.podColor)
			}
		}
	}
	if len(b.Notices) > 0 {
		l.notice = b.Notices[len(b.Notices)-1].Text
	}
	if b.HistoryDone {
		l.loading, l.live = false, true
		releaseMemory()
	}
	l.evict()
}

// evict drops view entries that left the buffer, keeping the cursor on
// the same entry.
func (l *logsScreen) evict() {
	first := l.buf.FirstSeq()
	n := 0
	for n < len(l.rows) && l.rows[n].seq < first {
		n++
	}
	if n == 0 {
		return
	}
	for _, r := range l.rows[:n] {
		l.count(r, -1)
	}
	l.rows = l.rows[n:]
	l.frozen = max(l.frozen-n, 0)
	if !l.newestTop {
		l.cursor, l.offset = max(l.cursor-n, 0), max(l.offset-n, 0)
	}
}

func (l *logsScreen) inScope(pod string) bool { return l.scope == nil || l.scope[pod] }

// rebuild recomputes the rows after the scope or the filters changed,
// keeping the cursor on the same entry when it is still shown.
func (l *logsScreen) rebuild() {
	var keep uint64
	if !l.tail {
		if e, ok := l.entryAt(l.displayCursor()); ok {
			keep = e.Seq
		}
	}
	idx := l.idxBuf[:0]
	for i := range l.buf.Len() {
		if l.inScope(l.buf.At(i).Pod) {
			idx = append(idx, i)
		}
	}
	l.idxBuf = idx
	sel := l.filter.SelectAppend(l.selBuf, len(idx), func(i int) *domain.LogEntry { return l.buf.At(idx[i]) })
	l.selBuf = sel
	l.rows = l.rows[:0]
	l.levels = [domain.LevelError + 1]int{}
	for _, r := range sel {
		e := l.buf.At(idx[r.Index])
		row := viewRow{seq: e.Seq, level: e.Level, match: r.Match, context: r.Context, gap: r.Gap}
		l.rows = append(l.rows, row)
		l.count(row, 1)
	}
	l.dirty, l.paused = false, false
	if keep != 0 {
		for i, r := range l.rows {
			if r.seq >= keep {
				l.cursor = i
				if l.newestTop {
					l.cursor = len(l.rows) - 1 - i
				}
				return
			}
		}
	}
	l.tail = true
}

// shown is the number of view entries displayed (frozen while paused).
func (l *logsScreen) shown() int {
	if l.paused {
		return min(l.frozen, len(l.rows))
	}
	return len(l.rows)
}

// entryAt returns the entry at display position i (newest first when
// ordered so).
func (l *logsScreen) entryAt(i int) (*domain.LogEntry, bool) {
	n := l.shown()
	if i < 0 || i >= n {
		return nil, false
	}
	if l.newestTop {
		i = n - 1 - i
	}
	idx, ok := l.buf.Index(l.rows[i].seq)
	if !ok {
		return nil, false
	}
	return l.buf.At(idx), true
}

// displayCursor is the cursor in display order.
func (l *logsScreen) displayCursor() int {
	n := l.shown()
	if l.tail {
		if l.newestTop {
			return 0
		}
		return max(n-1, 0)
	}
	return min(max(l.cursor, 0), max(n-1, 0))
}

func (l *logsScreen) scroll(delta int) {
	n := l.shown()
	c := l.displayCursor() + delta
	c = min(max(c, 0), max(n-1, 0))
	l.cursor = c
	newest := n - 1
	if l.newestTop {
		newest = 0
	}
	l.tail = c == newest
}

func (l *logsScreen) key(m *Model, k tea.KeyPressMsg) (bool, tea.Cmd) {
	if l.editing {
		return true, l.filterKey(m, k)
	}
	keys, key := m.opts.Keys, k.String()
	page := max(l.height-2, 1)
	if w, ok := l.windowKey(m, key); ok {
		return true, l.setWindow(m, w)
	}
	switch {
	case keys.Is(key, ActDown):
		l.scroll(1)
	case keys.Is(key, ActUp):
		l.scroll(-1)
	case keys.Is(key, ActPageDown):
		l.scroll(page)
	case keys.Is(key, ActPageUp):
		l.scroll(-page)
	case keys.Is(key, ActTop):
		l.scroll(-l.shown())
	case keys.Is(key, ActBottom):
		l.scroll(l.shown())
	case keys.Is(key, ActNextError):
		l.jumpError(1)
	case keys.Is(key, ActPrevError):
		l.jumpError(-1)
	case keys.Is(key, ActFilter):
		l.startEditing()
	case keys.Is(key, ActFilterMode):
		l.toggleMode(m)
		l.rebuild()
	case keys.Is(key, ActRegex):
		l.regex = !l.regex
		l.setTexts()
		l.rebuild()
		m.flash(onOff("regex", l.regex))
	case keys.Is(key, ActContext):
		l.cycleContext(m)
	case keys.Is(key, ActNextMatch):
		l.jumpMatch(m, 1)
	case keys.Is(key, ActPrevMatch):
		l.jumpMatch(m, -1)
	case keys.Is(key, ActLevels):
		m.popup = newLevelPicker(l)
	case keys.Is(key, ActErrorsOnly):
		l.setLevels(m, errorsOnly())
	case keys.Is(key, ActWarnAndError):
		l.setLevels(m, warnAndError())
	case keys.Is(key, ActAllLevels):
		l.setLevels(m, domain.AllLevels())
	case keys.Is(key, ActFollow):
		l.follow = !l.follow
		m.flash(onOff("follow", l.follow))
		return true, l.open(m)
	case keys.Is(key, ActPreviousLogs):
		l.previous = !l.previous
		m.flash(map[bool]string{true: "previous instance of the restarted containers", false: "current logs"}[l.previous])
		return true, l.open(m)
	case keys.Is(key, ActPause):
		l.paused = !l.paused
		l.frozen = len(l.rows)
		if !l.paused {
			l.scroll(l.shown())
		}
		m.flash(map[bool]string{true: "paused (lines keep buffering)", false: "resumed"}[l.paused])
	case keys.Is(key, ActRefresh) && l.err != nil:
		m.flash("reloading " + l.repo)
		return true, l.open(m)
	case keys.Is(key, ActWindowNext):
		return true, l.setWindow(m, domain.NextWindow(m.opts.Windows, l.window))
	case keys.Is(key, ActWindowPick):
		m.popup = newWindowPicker(m, l)
	case keys.Is(key, ActOrder):
		l.newestTop = !l.newestTop
		l.cursor = l.shown() - 1 - l.displayCursor()
		m.flash(map[bool]string{true: "newest first", false: "oldest first"}[l.newestTop])
	case keys.Is(key, ActTimestamps):
		l.cycleTime(m)
	case keys.Is(key, ActCycleColumns):
		l.cycleColumns(m)
	case keys.Is(key, ActResetDisplay):
		l.resetDisplay(m)
	case keys.Is(key, ActPodID):
		l.podID = (l.podID + 1) % (podIDNone + 1)
		l.manualColumns, l.cycling = true, false
		m.flash("pod id " + [...]string{"short", "full", "hidden"}[l.podID])
	case keys.Is(key, ActColumns):
		m.popup = newColumnsPicker(l)
	case keys.Is(key, ActFocus):
		l.toggleFocus(m)
	case keys.Is(key, ActWrap):
		l.wrap, l.pan = !l.wrap, 0
		m.flash(onOff("wrap", l.wrap))
	case keys.Is(key, ActPanRight):
		l.pan += 8
	case keys.Is(key, ActPanLeft):
		l.pan = max(l.pan-8, 0)
	case keys.Is(key, ActPanRightHalf):
		l.pan += max(m.width/2, 8)
	case keys.Is(key, ActPanLeftHalf):
		l.pan = max(l.pan-max(m.width/2, 8), 0)
	case keys.Is(key, ActFullscreen):
		l.fullscreen = !l.fullscreen
	case keys.Is(key, ActBack) && l.fullscreen:
		l.fullscreen = false
	case keys.Is(key, ActBack) && l.clearLastFilter(m):
	case keys.Is(key, ActPodScope):
		l.cycleScope()
		m.flash("scope " + l.scopeLabel())
	case keys.Is(key, ActPodSelector):
		return true, m.push(newPodSelector(l))
	case keys.Is(key, ActOpen):
		if e, ok := l.entryAt(l.displayCursor()); ok {
			return true, m.push(newZoomScreen(l, e.Seq))
		}
	default:
		return false, nil
	}
	return true, nil
}

// windowKey maps the preset keys (1…7, 0 and their AZERTY aliases).
func (l *logsScreen) windowKey(m *Model, key string) (domain.TimeWindow, bool) {
	acts := []Action{ActWindow1, ActWindow2, ActWindow3, ActWindow4, ActWindow5, ActWindow6, ActWindow7}
	w := m.opts.Windows
	for i, a := range acts {
		if m.opts.Keys.Is(key, a) && i < len(w)-1 {
			return w[i], true
		}
	}
	if m.opts.Keys.Is(key, ActWindowTail) {
		return w[len(w)-1], true
	}
	return domain.TimeWindow{}, false
}

func (l *logsScreen) setWindow(m *Model, w domain.TimeWindow) tea.Cmd {
	if w == l.window {
		return nil
	}
	l.window = w
	return l.open(m)
}

// cycleScope goes all pods → first pod → … → last pod → all pods.
func (l *logsScreen) cycleScope() {
	names := make([]string, 0, len(l.pods))
	for _, p := range l.pods {
		names = append(names, p.Pod.Name)
	}
	switch {
	case len(names) == 0:
		l.scope = nil
	case l.scope == nil:
		l.scope = map[string]bool{names[0]: true}
	default:
		cur := -1
		for i, n := range names {
			if l.scope[n] && len(l.scope) == 1 {
				cur = i
			}
		}
		if cur < 0 || cur == len(names)-1 {
			l.scope = nil
		} else {
			l.scope = map[string]bool{names[cur+1]: true}
		}
	}
	l.rebuild()
}

// jumpError moves the cursor to the next (dir 1) or previous error line
// in display order.
func (l *logsScreen) jumpError(dir int) {
	for i := l.displayCursor() + dir; i >= 0 && i < l.shown(); i += dir {
		if e, ok := l.entryAt(i); ok && e.Level == domain.LevelError {
			l.scroll(i - l.displayCursor())
			return
		}
	}
}

// --- rendering ---

func (l *logsScreen) view(m *Model, w, h int) string {
	t := m.opts.Theme
	var parts []string
	if !l.fullscreen {
		parts = append(parts, l.podStrip(m, w))
		h--
	}
	l.height = h
	switch {
	case l.err != nil:
		parts = append(parts, centered(t.Bad.Render("Cannot read the logs of "+l.repo+": "+errKind(l.err))+"\n\n"+t.Dim.Render(l.err.Error())+
			"\n\n"+l.keyHint(m, ActRefresh, "retry", ActBack, "back"), w, h))
	case l.shown() == 0 && l.loading:
		parts = append(parts, centered(t.Key.Render(m.spinner())+t.Dim.Render(fmt.Sprintf(" loading %s of %s", l.window.Label(), l.repo)), w, h))
	case l.shown() == 0:
		parts = append(parts, centered(l.emptyMessage(m), w, h))
	default:
		parts = append(parts, l.lines(m, w, h))
	}
	return strings.Join(parts, "\n")
}

// emptyMessage explains an empty view and offers the next step.
func (l *logsScreen) emptyMessage(m *Model) string {
	t := m.opts.Theme
	switch {
	case l.buf.Len() > 0 && (l.filter.Active() || levelsLabel(l.filter.Levels) != "all"):
		return t.Dim.Render(fmt.Sprintf("no line out of %d matches the filters", l.buf.Len())) + "\n\n" +
			l.keyHint(m, ActBack, "clear the last filter", ActAllLevels, "all levels")
	case l.buf.Len() > 0:
		return t.Dim.Render("no line from the selected pods") + "\n\n" + l.keyHint(m, ActPodScope, "pods")
	case l.window.Tail > 0:
		return t.Dim.Render("no log line yet") + "\n\n" + l.keyHint(m, ActFollow, "follow", ActBack, "back")
	}
	return t.Dim.Render("no log line in the last "+l.window.Label()) + "\n\n" +
		l.keyHint(m, ActWindowNext, "longer window", ActWindowTail, "last lines", ActBack, "back")
}

// keyHint formats key/description pairs: "t longer window  ·  0 last lines".
func (l *logsScreen) keyHint(m *Model, pairs ...any) string {
	t := m.opts.Theme
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, t.Key.Render(m.label(pairs[i].(Action)))+" "+t.Dim.Render(pairs[i+1].(string)))
	}
	return strings.Join(parts, t.Dim.Render("  ·  "))
}

// lines renders the viewport: the cursor is kept visible, the tail pins the
// newest line to the bottom (or top when newest first). Each entry is
// rendered at most once per frame.
func (l *logsScreen) lines(m *Model, w, h int) string {
	cur := l.displayCursor()
	f := l.frame(m, w)
	cache := map[int][]string{}
	render := func(i int) []string {
		if rows, ok := cache[i]; ok {
			return rows
		}
		var rows []string
		if e, ok := l.entryAt(i); ok {
			r, _ := l.rowAt(i)
			rows = l.renderRows(m, e, r, w, f)
		}
		cache[i] = rows
		return rows
	}
	heightOf := func(i int) int { return len(render(i)) }
	switch {
	case l.tail && !l.newestTop:
		// Pin the newest line to the bottom.
		used := heightOf(cur)
		l.offset = cur
		for l.offset > 0 && used+heightOf(l.offset-1) <= h {
			l.offset--
			used += heightOf(l.offset)
		}
	case l.tail:
		l.offset = 0
	default:
		// Keep the offset if the cursor is visible from it; otherwise
		// scroll just enough to show the cursor at the bottom. Walking
		// back from the cursor costs at most h entries, however far it
		// jumped.
		l.offset = min(l.offset, cur)
		used, first := 0, cur
		for first >= l.offset {
			used += heightOf(first)
			if used > h {
				break
			}
			first--
		}
		if used > h {
			l.offset = min(first+1, cur)
		}
	}
	start := l.offset
	type row struct {
		text string
		sel  bool
	}
	var rows []row
	for i := start; i < l.shown() && len(rows) < h; i++ {
		for _, r := range render(i) {
			rows = append(rows, row{r, i == cur})
		}
	}
	sel := m.opts.Theme.Selected
	out := make([]string, 0, h)
	for _, r := range rows[:min(len(rows), h)] {
		line := r.text
		if r.sel {
			plain := ansi.Strip(line)
			line = sel.Render(plain + strings.Repeat(" ", max(w-textWidth(plain), 0)))
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// maxWrapRows caps the rows one entry takes in wrap mode.
const maxWrapRows = 12

// frameState is what every line of a frame shares.
type frameState struct {
	opts      ports.RenderOptions
	warnColor bool // no level column: WARN messages carry the color
}

func (l *logsScreen) frame(m *Model, w int) frameState {
	hide := l.effectiveHide(m, w)
	return frameState{
		opts:      ports.RenderOptions{Timestamps: l.timestamps, Now: m.opts.Now(), Hide: hide},
		warnColor: levelHidden(m.opts.Columns, hide),
	}
}

// renderEntry returns the display rows of one entry: the line (wrapped or
// panned) and, for a stack trace, one folded summary row.
func (l *logsScreen) renderEntry(m *Model, e *domain.LogEntry, row viewRow, w int) []string {
	return l.renderRows(m, e, row, w, l.frame(m, w))
}

func (l *logsScreen) renderRows(m *Model, e *domain.LogEntry, row viewRow, w int, f frameState) []string {
	var b strings.Builder
	b.WriteString(" ")
	if id := l.podLabel(e.Pod); id != "" {
		b.WriteString(m.podInk(l.podColor[e.Pod]).paint(id))
		b.WriteByte(' ')
	}
	filtering := l.filter.Active()
	for _, s := range m.layout(e).Render(*e, f.opts) {
		k := m.segmentInk(e, s.Role)
		if s.Role == ports.RoleMessage && f.warnColor && e.Level == domain.LevelWarn {
			k = m.ink("warn", func() lipgloss.Style { return m.opts.Theme.Warn }) // the level column no longer says it
		}
		if row.context {
			k = m.dim()
		}
		if filtering && (s.Role == ports.RoleMessage || s.Role == ports.RoleLogger || s.Role == ports.RoleThread) {
			b.WriteString(l.highlight(m, s.Text, k))
		} else {
			b.WriteString(k.paint(s.Text))
		}
	}
	line := b.String()
	var rows []string
	if row.gap {
		rows = append(rows, m.dim().paint(" --"))
	}
	if l.wrap {
		wrapped := strings.Split(ansi.Hardwrap(line, w, true), "\n")
		if len(wrapped) > maxWrapRows {
			// A huge line (a serialized payload) must not fill the view:
			// zoom shows it all.
			more := len(wrapped) - maxWrapRows + 1
			wrapped = append(wrapped[:maxWrapRows-1], m.dim().paint(fmt.Sprintf("    … %d more rows, enter to open", more)))
		}
		rows = append(rows, wrapped...)
	} else {
		if l.pan > 0 {
			line = ansi.TruncateLeft(line, l.pan, "…")
		}
		rows = append(rows, ansi.Truncate(line, w, "…"))
	}
	if e.Stack != "" {
		first, _, _ := strings.Cut(strings.TrimSpace(e.Stack), "\n")
		more := strings.Count(e.Stack, "\n")
		stack := m.ink("stack", func() lipgloss.Style { return m.opts.Theme.Stack })
		fold := "    " + stack.paint(first) + m.dim().paint(fmt.Sprintf("   [+%d lines, enter to open]", more))
		rows = append(rows, ansi.Truncate(fold, w, "…"))
	}
	return rows
}

func (l *logsScreen) segmentStyle(t Theme, e *domain.LogEntry, r ports.Role) lipgloss.Style {
	switch r {
	case ports.RoleTimestamp:
		return t.Timestamp
	case ports.RoleLevel:
		return t.levelStyle(e.Level)
	case ports.RoleThread:
		return t.Thread
	case ports.RoleLogger:
		return t.Logger
	case ports.RolePID:
		return t.PID
	case ports.RoleDim:
		return t.Dim
	case ports.RoleMessage:
		if e.Level == domain.LevelError {
			return t.ErrorText
		}
	}
	return lipgloss.NewStyle()
}

// podLabel identifies a pod: its generated suffix (the part after the
// last dash), the full name, or nothing.
func (l *logsScreen) podLabel(pod string) string {
	switch l.podID {
	case podIDFull:
		return pod
	case podIDNone:
		return ""
	}
	if i := strings.LastIndex(pod, "-"); i >= 0 && len(pod)-i-1 >= 4 {
		return pod[i+1:]
	}
	return pod
}

func (l *logsScreen) podStrip(m *Model, w int) string {
	t := m.opts.Theme
	scope := "all " + fmt.Sprint(len(l.pods))
	if l.scope != nil {
		scope = fmt.Sprintf("%d of %d", len(l.scope), len(l.pods))
	}
	parts := []string{t.Dim.Render(" pods ") + t.Chip.Render(scope)}
	for _, p := range l.pods {
		st := domain.PodStatus(p.Pod)
		desc := st.String()
		style := t.statusStyle(st)
		switch {
		case p.Terminated:
			desc, style = "terminated", t.Dim
		case errors.Is(p.Err, domain.ErrNoPrevious):
			desc, style = "no previous instance", t.Dim
		case errors.Is(p.Err, domain.ErrNotStarted): // its logs come back when it runs
			desc = "waiting: " + st.String()
		case p.Err != nil:
			desc, style = "no logs: "+errKind(p.Err), t.Warn
		}
		extra := ""
		if n := p.Pod.Restarts(); n > 0 {
			extra += fmt.Sprintf(" %s", plural(n, "restart"))
		}
		if app, ok := m.opts.Filter.PrimaryApp(p.Pod); ok {
			extra += " " + domain.ImageTag(app.Image)
		}
		if p.New {
			extra += " new"
		}
		label := t.podStyle(l.podColor[p.Pod.Name]).Render(l.shortName(p.Pod.Name))
		if l.scope != nil && !l.scope[p.Pod.Name] {
			label = t.Dim.Render(l.shortName(p.Pod.Name))
		}
		parts = append(parts, label+" "+style.Render(desc)+t.Dim.Render(extra))
	}
	return ansi.Truncate(strings.Join(parts, "   "), w, "…")
}

func (l *logsScreen) shortName(pod string) string { return podShortID(pod) }

// bar is the status bar style (red in production).
func (l *logsScreen) bar(m *Model) lipgloss.Style {
	if m.env.Production {
		return m.opts.Theme.StatusProd
	}
	return m.opts.Theme.Status
}

func (l *logsScreen) statusLeft(m *Model) string {
	t := m.opts.Theme
	bar := l.bar(m)
	live := "LIVE"
	if r := l.rate.label(m.opts.Now()); r != "" {
		live += " " + r
	}
	var chip string
	switch {
	case l.previous:
		chip = t.ChipPaused.Render("PREVIOUS INSTANCE")
	case l.paused:
		chip = t.ChipPaused.Render(fmt.Sprintf("PAUSED +%d", len(l.rows)-l.frozen))
	case !l.follow:
		chip = t.Chip.Render("STOPPED")
	case !l.tail && !l.newestTop:
		chip = t.ChipLive.Render(fmt.Sprintf("%s +%d below", live, l.shown()-1-l.displayCursor()))
	default:
		chip = t.ChipLive.Render(live)
	}
	out := chip + bar.Render("  ")
	if counts := l.levelCounts(m); counts != "" {
		out += counts + bar.Render("  ·  ")
	}
	scope := l.scopeLabel()
	order := "oldest first"
	if l.newestTop {
		order = "newest first"
	}
	window := l.window.Label()
	if l.previous {
		window = "whole instance" // the window does not apply
	}
	fields := []string{window, scope, "levels " + levelsLabel(l.filter.Levels)}
	if f := l.filterSummary(); f != "" {
		fields = append(fields, f)
	}
	// Most useful first: a narrow terminal truncates the end.
	if l.notice != "" {
		fields = append(fields, l.notice)
	}
	if cols := l.columnsLabel(m, m.width); cols != "" {
		fields = append(fields, cols)
	}
	if l.wrap {
		fields = append(fields, "wrap")
	}
	fields = append(fields, order,
		fmt.Sprintf("%d/%d lines", l.shown(), l.buf.Len()),
		fmt.Sprintf("buffer %d%%", l.buf.Len()*100/max(l.buf.Cap(), 1)),
		fmt.Sprintf("dropped %d", l.buf.Dropped()),
	)
	return out + bar.Render(strings.Join(fields, "  ·  "))
}

func (l *logsScreen) hints(m *Model) []hint {
	switch {
	case l.editing:
		return []hint{
			{"enter", "keep"},
			{"esc", "cancel"},
			{"ctrl+r", "regex"},
			{"ctrl+x", "filter/highlight"},
			{"!", "invert (first char)"},
			{m.label(ActAddFilter), "add filter (AND)"},
			{"ctrl+u", "erase"},
		}
	case l.paused:
		return []hint{m.h(ActPause, "resume"), m.pair(ActDown, ActUp, "scroll"), m.h(ActOpen, "zoom"), m.h(ActFilter, "filter"), m.h(ActHelp, "help")}
	}
	return []hint{
		m.h(ActFilter, "filter"), m.h(ActLevels, "levels"), m.h(ActFilterMode, "mode"), m.pair(ActNextMatch, ActPrevMatch, "match"),
		m.h(ActFollow, "follow"), m.h(ActPause, "pause"), m.h(ActWindowNext, "window"), m.h(ActPodScope, "pods"),
		m.h(ActCycleColumns, "cols"), m.h(ActColumns, "columns"), m.h(ActFocus, "focus"), m.h(ActOpen, "zoom"), m.h(ActHelp, "help"),
	}
}

func (l *logsScreen) fullHints(m *Model) []hint {
	if l.editing || l.paused {
		return l.hints(m)
	}
	return []hint{
		m.h(ActFilter, "filter"), m.h(ActFilterMode, "filter/highlight"), m.pair(ActNextMatch, ActPrevMatch, "match"),
		m.h(ActContext, "context"), m.h(ActLevels, "levels"), m.h(ActErrorsOnly, "errors"), m.h(ActWarnAndError, "warn+"),
		m.h(ActAllLevels, "all"), m.pair(ActNextError, ActPrevError, "error"), m.h(ActOpen, "zoom"),
		m.h(ActFollow, "follow"), m.h(ActPause, "pause"), m.h(ActWindowNext, "window"), m.h(ActWindowPick, "windows"),
		{m.label(ActWindow1) + "…" + m.label(ActWindow7) + " " + m.label(ActWindowTail), "15m…2d tail"},
		m.h(ActPodScope, "pods"), m.h(ActPodSelector, "select pods"), m.h(ActCycleColumns, "hide next column"), m.h(ActColumns, "columns"),
		m.h(ActPreviousLogs, "previous instance"), m.h(ActFocus, "focus"), m.h(ActResetDisplay, "reset display"), m.h(ActTimestamps, "time format"), m.h(ActOrder, "order"), m.h(ActWrap, "wrap"), m.h(ActFullscreen, "fullscreen"),
		m.h(ActBack, "back"), m.h(ActKeyBar, "keys"), m.h(ActHelp, "help"),
	}
}

func (l *logsScreen) prompt(m *Model) string {
	if !l.editing {
		return ""
	}
	return l.promptLine(m)
}

// scopeLabel describes the pod scope.
func (l *logsScreen) scopeLabel() string {
	switch {
	case l.scope == nil:
		return "all pods"
	case len(l.scope) == 1:
		for p := range l.scope {
			return "pod " + l.shortName(p)
		}
	}
	return fmt.Sprintf("%d pods", len(l.scope))
}

// seqList returns the displayed entries' sequence numbers, oldest first.
func (l *logsScreen) seqList() []uint64 {
	out := make([]uint64, len(l.rows))
	for i, r := range l.rows {
		out[i] = r.seq
	}
	return out
}

// rowAt returns the row at display position i.
func (l *logsScreen) rowAt(i int) (viewRow, bool) {
	n := l.shown()
	if i < 0 || i >= n {
		return viewRow{}, false
	}
	if l.newestTop {
		i = n - 1 - i
	}
	return l.rows[i], true
}

// podNames lists the pods, for the pod selector.
func (l *logsScreen) podNames() []string {
	out := make([]string, 0, len(l.pods))
	for _, p := range l.pods {
		out = append(out, p.Pod.Name)
	}
	slices.Sort(out)
	return out
}
