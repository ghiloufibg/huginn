package tui

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

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
	// lateTickMsg merges the late entries received since the last merge.
	lateTickMsg struct {
		screen *logsScreen
		gen    int
	}
)

// lateEvery is the most often late entries are merged: a merge re-sorts
// the buffer and rebuilds the view, and a stream recovering from an outage
// delivers what it missed over many batches.
const lateEvery = 250 * time.Millisecond

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
	// containerMode is the containers streamed (A switches: application
	// containers, or all); containerScope the containers shown (nil: all
	// streamed ones), chosen in the selector (S).
	containerMode  domain.ContainerMode
	containerScope map[string]bool
	// showMuted streams the lines of the loggers muted by the log formats
	// (M switches); muted counts the lines hidden in this session, and
	// mutedBy per pattern of mute.loggers (help shows them).
	showMuted bool
	muted     uint64
	mutedBy   map[string]uint64
	// skipped counts entries the session left out because more arrived
	// than the buffer holds (counted with the evicted ones as dropped).
	skipped uint64
	roles   map[string]map[string]domain.ContainerRole // pod → container → role
	// multiContainer: some pod streams several containers, so the pod
	// column names the container too.
	multiContainer bool
	containerWidth int
	formats        map[string]bool // formats of the entries received (their layouts' columns are offered)

	// viewport
	cursor   int  // position in display order
	offset   int  // first displayed position
	tail     bool // cursor sticks to the newest line
	paused   bool
	frozen   int               // len(view) when paused
	held     []domain.LogEntry // arrived while paused, not yet in the buffer
	heldLate []domain.LogEntry // same, for late entries
	heldLost int               // held entries dropped (more than the buffer holds)
	// lateWaiting are late entries not merged yet (lateEvery); a merge
	// is scheduled when lateTick is set.
	lateWaiting []domain.LogEntry
	lateTick    bool
	newestTop   bool
	wrap        bool
	pan         int
	timestamps  ports.TimestampMode
	podID       podIDMode
	fullscreen  bool
	height      int

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
	before    string      // input before editing, restored by esc
	ctx       contextTail // context selection as entries are appended
	dirty     bool        // rows must be recomputed (debounced while typing)
	pending   bool        // a debounce tick is scheduled

	trace *traceState // the trace view (trace.go), nil outside it
	sel   selection   // lines selected for copying (selection.go)
	// mouse (mouse.go): the display position of the line on each screen
	// row of the last frame, the screen row of the first one, and a drag.
	screenRows []int
	linesTop   int
	dragging   bool
	dragFrom   uint64
	lastForm   copyForm // the form of the last copy, which ctrl+s saves in
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
		containerMode: m.opts.ContainerMode,
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
	l.held, l.heldLate, l.heldLost = nil, nil, 0
	l.lateWaiting, l.lateTick = nil, false
	l.ctx = contextTail{last: -1}
	l.formats = map[string]bool{}
	l.levels, l.live, l.rate, l.muted, l.mutedBy, l.skipped = [domain.LevelError + 1]int{}, false, rateMeter{}, 0, nil, 0
	if m.opts.Sessions == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(m.opts.Context)
	l.cancel = cancel
	gen, sessions := l.gen, m.opts.Sessions
	if l.window.IsHead() {
		l.tail = false // a head is read from its first line, set once loaded
	}
	q := ports.LogQuery{Env: domain.Env(m.env.Name), Repo: l.repo, Window: l.window, Follow: l.follow && !l.window.IsHead(), Previous: l.previous, Containers: l.containerMode, NoMute: l.showMuted}
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
		cmd := l.wait(msg.gen, msg.ch)
		if len(l.lateWaiting) > 0 && !l.lateTick {
			l.lateTick = true
			gen := l.gen
			cmd = tea.Batch(cmd, tea.Tick(lateEvery, func(time.Time) tea.Msg { return lateTickMsg{screen: l, gen: gen} }))
		}
		return true, cmd
	case lateTickMsg:
		if msg.screen != l || msg.gen != l.gen {
			return false, nil
		}
		l.lateTick = false
		l.mergeWaiting()
		return true, nil
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
	case tea.MouseClickMsg, tea.MouseMotionMsg, tea.MouseReleaseMsg:
		l.mouse(m, msg.(tea.MouseMsg))
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
		l.rate.add(now, len(b.Entries)+len(b.Late)+int(b.Skipped))
	}
	l.muted += b.Muted
	l.skipped += b.Skipped
	if b.MutedBy != nil {
		l.mutedBy = b.MutedBy
	}
	if l.paused {
		// The paused view keeps its lines: new ones wait outside the
		// buffer, which would otherwise evict what is on screen.
		l.hold(b.Entries, b.Late)
	} else {
		l.ingest(b.Entries)
		l.waitLate(b.Late)
	}
	if b.Pods != nil {
		l.pods = b.Pods
		l.multiContainer, l.containerWidth = false, 0
		l.roles = make(map[string]map[string]domain.ContainerRole, len(b.Pods))
		for _, p := range b.Pods {
			l.roles[p.Pod.Name] = p.Roles
			l.multiContainer = l.multiContainer || len(p.Containers) > 1
			for _, c := range p.Containers {
				l.containerWidth = max(l.containerWidth, len(c))
			}
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
	if b.HistoryDone && l.window.IsHead() {
		l.toOldest()
	}
}

// toOldest puts the cursor on the oldest line, unpinned: where a head is
// read from.
func (l *logsScreen) toOldest() {
	l.tail, l.cursor = false, 0
	if l.newestTop {
		l.cursor = max(l.shown()-1, 0)
	}
}

// hold keeps entries arriving while paused, at most a buffer's worth: the
// oldest are dropped beyond, and counted.
func (l *logsScreen) hold(entries, late []domain.LogEntry) {
	l.held = append(l.held, entries...)
	l.heldLate = append(l.heldLate, late...)
	if over := len(l.held) + len(l.heldLate) - l.buf.Cap(); over > 0 {
		n := min(over, len(l.held))
		l.held = append(l.held[:0], l.held[n:]...)
		l.heldLate = l.heldLate[min(over-n, len(l.heldLate)):]
		l.heldLost += over
	}
}

// release adds the entries held while paused.
func (l *logsScreen) release() {
	entries, late := l.held, l.heldLate
	l.held, l.heldLate = nil, nil
	l.ingest(entries)
	l.mergeLate(late)
	l.evict()
}

func (l *logsScreen) noteFormat(f string) {
	if !l.formats[f] {
		if l.formats == nil {
			l.formats = map[string]bool{}
		}
		l.formats[f] = true
	}
}

// waitLate keeps late entries until the next merge (lateEvery), at most a
// buffer's worth: older ones would be evicted by the merge.
func (l *logsScreen) waitLate(late []domain.LogEntry) {
	l.lateWaiting = append(l.lateWaiting, late...)
	if over := len(l.lateWaiting) - l.buf.Cap(); over > 0 {
		slices.SortStableFunc(l.lateWaiting, func(a, b domain.LogEntry) int { return a.OrderTime().Compare(b.OrderTime()) })
		clear(l.lateWaiting[:over])
		l.lateWaiting = l.lateWaiting[over:]
		l.skipped += uint64(over)
	}
}

// mergeWaiting merges the late entries waiting, in one pass over the
// buffer.
func (l *logsScreen) mergeWaiting() {
	late := l.lateWaiting
	l.lateWaiting = nil
	if l.paused { // the pause holds them until it ends
		l.hold(nil, late)
		return
	}
	l.mergeLate(late)
	l.evict()
}

// mergeLate places entries older than some shown ones at their place.
func (l *logsScreen) mergeLate(late []domain.LogEntry) {
	if len(late) == 0 {
		return
	}
	var keep uint64
	if !l.tail {
		if e, ok := l.entryAt(l.displayCursor()); ok {
			keep = e.Seq
		}
	}
	for _, e := range late {
		l.noteFormat(e.Format)
	}
	renumber := l.buf.InsertLate(late)
	if keep != 0 {
		keep, _ = renumber(keep)
	}
	if l.trace != nil {
		l.trace.seq, _ = renumber(l.trace.seq)
	}
	l.rebuildFrom(keep)
}

// ingest appends entries to the buffer and the view.
func (l *logsScreen) ingest(entries []domain.LogEntry) {
	added := 0
	for _, e := range entries {
		l.noteFormat(e.Format)
		seq := l.buf.Append(e)
		if !l.entryInScope(&e) || l.dirty {
			continue
		}
		i, _ := l.buf.Index(seq)
		if l.needsContext() {
			added += l.appendContext(seq, l.buf.At(i))
			continue
		}
		if r, ok := l.rowFor(seq, l.buf.At(i)); ok {
			l.rows = append(l.rows, r)
			l.count(r, 1)
			added++
		}
	}
	if l.dirty && !l.editing {
		l.rebuild()
	} else if l.trace != nil && added > 0 {
		l.keepCursor(l.orderTrace)
	}
	if l.newestTop && !l.tail && !l.paused {
		// New entries are inserted above: keep the same entries on screen.
		l.cursor += added
		l.offset += added
	}
}

// evict drops view entries that left the buffer, keeping the cursor on
// the same entry.
func (l *logsScreen) evict() {
	first := l.buf.FirstSeq()
	if l.sel.active() {
		l.pruneSelection()
	}
	if l.trace != nil && len(l.rows) > 0 && !slices.ContainsFunc(l.rows, func(r viewRow) bool { return r.seq < first }) {
		return
	}
	if l.trace != nil {
		// Trace rows are ordered by entry time, not by sequence: drop the
		// evicted ones wherever they are.
		kept := l.rows[:0]
		for _, r := range l.rows {
			if r.seq < first {
				l.count(r, -1)
				continue
			}
			kept = append(kept, r)
		}
		l.rows = kept
		l.orderTrace()
		return
	}
	n := 0
	for n < len(l.rows) && l.rows[n].seq < first {
		n++
	}
	if n == 0 {
		return
	}
	n += l.orphanContext(n)
	for _, r := range l.rows[:n] {
		l.count(r, -1)
	}
	l.rows = l.rows[n:]
	if len(l.rows) > 0 {
		l.rows[0].gap = false // as a full selection would draw it
	}
	l.frozen = max(l.frozen-n, 0)
	if !l.newestTop {
		l.cursor, l.offset = max(l.cursor-n, 0), max(l.offset-n, 0)
	}
}

func (l *logsScreen) inScope(pod string) bool { return l.scope == nil || l.scope[pod] }

// entryInScope applies the pod and container selections.
func (l *logsScreen) entryInScope(e *domain.LogEntry) bool {
	return l.inScope(e.Pod) && (l.containerScope == nil || l.containerScope[e.Container])
}

// isSidecar tells whether a container of a pod is not an application
// container (its lines are drawn dimmer).
func (l *logsScreen) isSidecar(pod, container string) bool {
	return l.roles[pod][container] != domain.RoleApp
}

// setContainerMode switches the streamed containers and reopens.
func (l *logsScreen) setContainerMode(m *Model, mode domain.ContainerMode) tea.Cmd {
	l.containerMode = mode
	if mode == domain.ContainersApp {
		l.containerScope = nil // sidecars are no longer streamed
	}
	m.flash(map[domain.ContainerMode]string{
		domain.ContainersApp: "application containers",
		domain.ContainersAll: "all containers (sidecars and init)",
	}[mode])
	return l.open(m)
}

// containersLabel describes the container selection for the status bar,
// "" for the default.
func (l *logsScreen) containersLabel(m *Model) string {
	switch {
	case len(l.containerScope) > 0:
		names := slices.Sorted(maps.Keys(l.containerScope))
		return "containers " + strings.Join(names, ",")
	case l.containerMode != m.opts.ContainerMode:
		return "containers " + l.containerMode.String()
	}
	return ""
}

// rebuild recomputes the rows after the scope or the filters changed,
// keeping the cursor on the same entry when it is still shown.
func (l *logsScreen) rebuild() {
	var keep uint64
	if !l.tail {
		if e, ok := l.entryAt(l.displayCursor()); ok {
			keep = e.Seq
		}
	}
	l.rebuildFrom(keep)
}

// rebuildFrom recomputes the rows, putting the cursor on the entry with
// sequence keep (0: the tail).
func (l *logsScreen) rebuildFrom(keep uint64) {
	if l.paused { // a rebuild ends the pause: take what waited
		l.paused = false
		if l.heldLost > 0 {
			l.notice = fmt.Sprintf("%d lines dropped while paused (more than the buffer holds)", l.heldLost)
			l.heldLost = 0
		}
		entries, late := l.held, l.heldLate
		l.held, l.heldLate = nil, nil
		for _, e := range entries {
			l.buf.Append(e)
		}
		if len(late) > 0 {
			renumber := l.buf.InsertLate(late)
			if keep != 0 {
				keep, _ = renumber(keep)
			}
		}
	}
	idx := l.idxBuf[:0]
	all := l.scope == nil && l.containerScope == nil // no pod or container chosen
	for i := range l.buf.Len() {
		if all || l.entryInScope(l.buf.At(i)) {
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
	l.resetContext(idx, sel)
	l.dirty, l.paused = false, false
	if l.trace != nil {
		l.orderTrace() // rows by entry time: the cursor goes on keep itself
		if i := slices.IndexFunc(l.rows, func(r viewRow) bool { return r.seq == keep }); keep != 0 && i >= 0 {
			l.cursor = i
			if l.newestTop {
				l.cursor = len(l.rows) - 1 - i
			}
			return
		}
	}
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
	case keys.Is(key, ActFilterMode) && l.trace != nil:
		m.flash("the trace view keeps only the lines of the trace")
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
	case keys.Is(key, ActFollow) && l.window.IsHead():
		// A head does not follow: following goes back to the default
		// window, so no gap is left between the head and now.
		l.window, l.follow = l.followWindow(m), true
		m.flash("follow: back to " + l.window.Label())
		return true, l.open(m)
	case keys.Is(key, ActFollow):
		l.follow = !l.follow
		m.flash(onOff("follow", l.follow))
		return true, l.open(m)
	case keys.Is(key, ActAllContainers):
		mode := domain.ContainersAll
		if l.containerMode == domain.ContainersAll {
			mode = domain.ContainersApp
		}
		return true, l.setContainerMode(m, mode)
	case keys.Is(key, ActShowMuted):
		l.showMuted = !l.showMuted
		m.flash(map[bool]string{true: "muted loggers shown", false: "muted loggers hidden"}[l.showMuted])
		return true, l.open(m)
	case keys.Is(key, ActPreviousLogs):
		l.previous = !l.previous
		m.flash(map[bool]string{true: "previous instance of the restarted containers", false: "current logs"}[l.previous])
		return true, l.open(m)
	case keys.Is(key, ActPause):
		l.paused = !l.paused
		l.frozen = len(l.rows)
		msg := "paused (new lines wait)"
		if !l.paused {
			lost := l.heldLost
			l.heldLost = 0
			l.release()
			l.scroll(l.shown())
			msg = "resumed"
			if lost > 0 {
				msg = fmt.Sprintf("resumed · %d lines dropped while paused (more than the buffer holds)", lost)
			}
		}
		m.flash(msg)
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
	case keys.Is(key, ActBack) && l.sel.active():
		l.clearSelection(m)
	case keys.Is(key, ActBack) && l.fullscreen:
		l.fullscreen = false
	case keys.Is(key, ActBack) && l.trace != nil && !l.traceHasExtraFilters():
		l.exitTrace(m)
	case keys.Is(key, ActBack) && l.clearLastFilter(m):
	case keys.Is(key, ActSelect):
		l.toggleRange(m)
	case keys.Is(key, ActMark):
		l.toggleMark(m)
	case keys.Is(key, ActCopy):
		return true, l.copyLines(m, l.selected(), copyShown)
	case keys.Is(key, ActCopyRaw):
		return true, l.copyLines(m, l.selected(), copyRaw)
	case keys.Is(key, ActSave):
		return true, l.save(m)
	case keys.Is(key, ActViewTrace):
		if e, ok := l.entryAt(l.displayCursor()); ok {
			l.enterTrace(m, e.Seq)
		}
	case keys.Is(key, ActPodScope):
		l.cycleScope()
		m.flash("scope " + l.scopeLabel())
	case keys.Is(key, ActPodSelector):
		return true, m.push(newPodSelector(m, l))
	case keys.Is(key, ActOpen):
		if e, ok := l.entryAt(l.displayCursor()); ok {
			return true, m.push(newZoomScreen(l, e.Seq))
		}
	default:
		return false, nil
	}
	return true, nil
}

// windowKey maps the preset keys (1…7, 0 for tail, 9 for head and their
// AZERTY aliases).
func (l *logsScreen) windowKey(m *Model, key string) (domain.TimeWindow, bool) {
	keys := m.opts.Keys
	for i, a := range []Action{ActWindow1, ActWindow2, ActWindow3, ActWindow4, ActWindow5, ActWindow6, ActWindow7} {
		if keys.Is(key, a) {
			return durationPreset(m.opts.Windows, i)
		}
	}
	switch {
	case keys.Is(key, ActWindowTail):
		return findWindow(m.opts.Windows, domain.TimeWindow.IsTail)
	case keys.Is(key, ActWindowHead):
		return findWindow(m.opts.Windows, domain.TimeWindow.IsHead)
	}
	return domain.TimeWindow{}, false
}

// durationPreset returns the i-th duration window of the presets.
func durationPreset(ws []domain.TimeWindow, i int) (domain.TimeWindow, bool) {
	for _, w := range ws {
		if w.IsTail() || w.IsHead() {
			continue
		}
		if i == 0 {
			return w, true
		}
		i--
	}
	return domain.TimeWindow{}, false
}

func findWindow(ws []domain.TimeWindow, is func(domain.TimeWindow) bool) (domain.TimeWindow, bool) {
	for _, w := range ws {
		if is(w) {
			return w, true
		}
	}
	return domain.TimeWindow{}, false
}

// followWindow is the window f goes back to from a head: the default one,
// or the tail when the default is a head.
func (l *logsScreen) followWindow(m *Model) domain.TimeWindow {
	if !m.opts.Window.IsHead() {
		return m.opts.Window
	}
	if w, ok := findWindow(m.opts.Windows, domain.TimeWindow.IsTail); ok {
		return w
	}
	return domain.TimeWindow{Tail: domain.DefaultTailLines}
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
	t := &m.opts.Theme
	var parts []string
	l.linesTop, l.screenRows = 1, l.screenRows[:0] // below the header; rows filled by lines
	if !l.fullscreen {
		parts = append(parts, l.podStrip(m, w))
		h--
		l.linesTop++
	}
	l.height = h
	switch {
	case l.err != nil:
		parts = append(parts, errorPanel(t, "Cannot read the logs of "+l.repo+": "+errKind(l.err), l.err, errFix(t, l.err),
			l.keyHint(m, ActRefresh, "retry", ActBack, "back"), w, h))
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
	t := &m.opts.Theme
	switch {
	case l.buf.Len() > 0 && (l.filter.Active() || levelsLabel(l.filter.Levels) != "all"):
		return t.Dim.Render(fmt.Sprintf("no line out of %d matches the filters", l.buf.Len())) + "\n\n" +
			l.keyHint(m, ActBack, "clear the last filter", ActAllLevels, "all levels")
	case l.buf.Len() > 0:
		return t.Dim.Render("no line from the selected pods") + "\n\n" + l.keyHint(m, ActPodScope, "pods")
	case l.muted > 0:
		return t.Dim.Render(fmt.Sprintf("all %s are from muted loggers (mute in formats/)", plural(int(l.muted), "line"))) + "\n\n" +
			l.keyHint(m, ActShowMuted, "show them", ActWindowNext, "longer window", ActBack, "back")
	case l.window.IsHead():
		return t.Dim.Render("no log line in "+l.repo+"'s containers yet") + "\n\n" + l.keyHint(m, ActWindowTail, "last lines", ActBack, "back")
	case l.window.Tail > 0:
		return t.Dim.Render("no log line yet") + "\n\n" + l.keyHint(m, ActFollow, "follow", ActBack, "back")
	}
	return t.Dim.Render("no log line in the last "+l.window.Label()) + "\n\n" +
		l.keyHint(m, ActWindowNext, "longer window", ActWindowTail, "last lines", ActBack, "back")
}

// keyHint formats key/description pairs: "t longer window  ·  0 last lines".
func (l *logsScreen) keyHint(m *Model, pairs ...any) string {
	t := &m.opts.Theme
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
	// With a selection, a one-column gutter marks the range (▌) and the
	// marked lines (*); the range is located once per frame.
	selecting := l.sel.active()
	lo, hi, inRange := l.rangeBounds()
	render := func(i int) []string {
		if rows, ok := cache[i]; ok {
			return rows
		}
		var rows []string
		if e, ok := l.entryAt(i); ok {
			r, _ := l.rowAt(i)
			if selecting {
				rows = l.renderRows(m, e, r, max(w-1, 1), f)
				mark := m.ink("key", func() lipgloss.Style { return m.opts.Theme.Key }).paint(l.gutter(i, e.Seq, lo, hi, inRange))
				for j := range rows {
					rows[j] = mark + rows[j]
				}
			} else {
				rows = l.renderRows(m, e, r, w, f)
			}
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
	l.screenRows = l.screenRows[:0]
	for i := start; i < l.shown() && len(rows) < h; i++ {
		for _, r := range render(i) {
			rows = append(rows, row{r, i == cur})
			l.screenRows = append(l.screenRows, i)
		}
	}
	l.screenRows = l.screenRows[:min(len(l.screenRows), h)]
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
	if l.trace != nil {
		b.WriteString(m.segmentInk(e, ports.RoleTimestamp).paint(l.traceDelta(e)))
	}
	if id := l.podLabel(e.Pod); id != "" {
		b.WriteString(m.podInk(l.podColor[e.Pod]).paint(id))
		if l.multiContainer && e.Container != "" {
			// Several containers in one pod: say which wrote it, sidecars
			// dimmer.
			name := "/" + padRight(e.Container, l.containerWidth)
			if l.isSidecar(e.Pod, e.Container) {
				b.WriteString(m.dim().paint(name))
			} else {
				b.WriteString(m.podInk(l.podColor[e.Pod]).paint(name))
			}
		}
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
		text := safeText(visiblePart(s.Text, l.visibleBytes(w)))
		if filtering && (s.Role == ports.RoleMessage || s.Role == ports.RoleLogger || s.Role == ports.RoleThread) {
			b.WriteString(l.highlight(m, text, k))
		} else {
			b.WriteString(k.paint(text))
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
		fold := "    " + stack.paint(safeText(first)) + m.dim().paint(fmt.Sprintf("   [+%d lines, enter to open]", more))
		rows = append(rows, ansi.Truncate(fold, w, "…"))
	}
	return rows
}

func (l *logsScreen) segmentStyle(t *Theme, e *domain.LogEntry, r ports.Role) lipgloss.Style {
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
func (l *logsScreen) podLabel(pod string) string { return podLabelFor(pod, l.podID) }

// podLabelFor is the pod column of pod in a pod-id mode.
func podLabelFor(pod string, mode podIDMode) string {
	switch mode {
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
	t := &m.opts.Theme
	scope := "all " + fmt.Sprint(len(l.pods))
	if l.scope != nil {
		scope = fmt.Sprintf("%d of %d", len(l.scope), len(l.pods))
	}
	parts := []string{t.Dim.Render(" pods ") + t.Chip.Render(scope)}
	for _, p := range l.pods {
		desc, st := podLabel(p.Pod)
		style := t.statusStyle(st)
		switch {
		case p.Terminated:
			desc, style = "terminated", t.Dim
		case p.Pod.Phase == domain.PodSucceeded || p.Pod.Deleted:
			style = t.Dim
		case errors.Is(p.Err, domain.ErrNoPrevious):
			desc, style = "no previous instance", t.Dim
		case errors.Is(p.Err, domain.ErrNotStarted): // its logs come back when it runs
			desc = "waiting: " + waitingReason(m, p.Pod, st)
		case p.Err != nil:
			desc, style = "no logs: "+errKind(p.Err), t.Warn
		}
		extra := ""
		if n := domain.PodRestarts(p.Pod, m.opts.Filter); n > 0 {
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

// repoGone says why no live line can come: "REMOVED" when the repository
// left the catalog, "NO PODS" when all its pods are gone; "" otherwise.
func (l *logsScreen) repoGone(m *Model) string {
	if !l.live || l.previous {
		return ""
	}
	if s := m.snap; s != nil && s.Synced && s.Err == nil && !slices.ContainsFunc(s.Services, func(r domain.ServiceSummary) bool { return r.Repo == l.repo }) {
		return "REMOVED"
	}
	for _, p := range l.pods {
		if !p.Terminated {
			return ""
		}
	}
	return "NO PODS"
}

// visibleBytes bounds the bytes of one segment that can reach the
// screen: a row, or maxWrapRows rows when wrapping, from the pan offset
// (4 bytes per cell covers any UTF-8). Longer text is cut before it is
// highlighted and painted: a megabyte line costs no more than a row.
func (l *logsScreen) visibleBytes(w int) int {
	if l.wrap {
		return maxWrapRows*w*4 + 64
	}
	return (l.pan+w)*4 + 64
}

func visiblePart(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// waitingReason is why the app containers of a pod do not run, in the
// runtime's words (CrashLoopBackOff, ImagePullBackOff, ContainerCreating).
func waitingReason(m *Model, p domain.Pod, st domain.ServiceStatus) string {
	for _, c := range p.Containers { // an init container that fails blocks the rest
		if c.Init && !domain.InitDone(c) && c.State != domain.ContainerRunning && c.Restarts > 0 {
			reason := c.Reason
			if c.State == domain.ContainerTerminated || reason == "" {
				reason = "failing"
			}
			return "init " + c.Name + " " + reason
		}
	}
	for _, c := range m.opts.Filter.AppContainers(p) {
		if c.State != domain.ContainerRunning && c.Reason != "" {
			return c.Reason
		}
	}
	return st.String()
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
	t := &m.opts.Theme
	bar := l.bar(m)
	live := "LIVE"
	if r := l.rate.label(m.opts.Now()); r != "" {
		live += " " + r
	}
	var chip string
	gone := l.repoGone(m)
	switch {
	case l.trace != nil:
		chip = t.Chip.Render("TRACE")
	case l.sel.active():
		chip = t.Chip.Render("SELECT")
	case l.err != nil:
		chip = t.Chip.Render("NOT LOADED")
	case gone != "":
		chip = t.Chip.Render(gone)
	case l.previous:
		chip = t.ChipPaused.Render("PREVIOUS INSTANCE")
	case l.paused:
		waiting := fmt.Sprintf("PAUSED +%d", len(l.held)+len(l.heldLate)+l.heldLost)
		if l.heldLost > 0 {
			waiting += fmt.Sprintf(" (%d dropped)", l.heldLost)
		}
		chip = t.ChipPaused.Render(waiting)
	case l.window.IsHead():
		chip = t.Chip.Render("HEAD")
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
	switch {
	case l.previous && l.window.IsHead():
		window = l.window.String() + " of the instance"
	case l.previous:
		window = "whole instance" // the window does not apply
	case l.window.IsHead():
		window = l.window.String()
	}
	fields := []string{window, scope, "levels " + levelsLabel(l.filter.Levels)}
	if c := l.containersLabel(m); c != "" {
		fields = append(fields[:2], append([]string{c}, fields[2:]...)...)
	}
	if l.trace != nil {
		fields = append(l.traceSummary(), window)
	} else if f := l.filterSummary(); f != "" {
		fields = append(fields, f)
	}
	switch {
	case l.showMuted:
		fields = append(fields, "muted loggers shown")
	case l.muted > 0:
		fields = append(fields, fmt.Sprintf("muted %d", l.muted))
	}
	if l.sel.active() {
		fields = append([]string{l.selectionSummary()}, fields...)
	}
	// Most useful first: a narrow terminal truncates the end.
	if gone == "REMOVED" {
		fields = append(fields, l.repo+" no longer exists in "+m.env.Name)
	}
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
		fmt.Sprintf("dropped %d", l.buf.Dropped()+l.skipped),
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
	case l.sel.active():
		return []hint{
			m.h(ActCopy, "copy"), m.h(ActCopyRaw, "copy raw"), m.h(ActSave, "save"), m.h(ActSelect, "range"), m.h(ActMark, "mark"),
			m.pair(ActDown, ActUp, "move"), m.h(ActBack, "clear"), m.h(ActHelp, "help"),
		}
	case l.trace != nil:
		return []hint{
			m.h(ActBack, "back to the logs"), m.h(ActOpen, "zoom"), m.h(ActFilter, "filter"), m.pair(ActNextError, ActPrevError, "error"),
			m.h(ActViewTrace, "trace of this line"), m.h(ActWindowNext, "window"), m.h(ActHelp, "help"),
		}
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
		m.h(ActPodScope, "pods"), m.h(ActPodSelector, "select pods/containers"), m.h(ActAllContainers, "all containers"), m.h(ActShowMuted, "muted loggers"), m.h(ActCycleColumns, "hide next column"), m.h(ActColumns, "columns"),
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
