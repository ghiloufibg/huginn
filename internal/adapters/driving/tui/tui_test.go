package tui

import (
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestMain(m *testing.M) {
	time.Local = time.UTC
	os.Exit(m.Run())
}

var envs = []EnvInfo{
	{Name: "dev", Context: "gke_acme_europe-west1_main", Namespaces: []string{"app-dev"}},
	{Name: "rec", Context: "gke_acme_europe-west1_main", Namespaces: []string{"app-rec"}},
	{Name: "prprd", Context: "gke_acme_europe-west1_main", Namespaces: []string{"app-prprd"}},
	{Name: "prd", Context: "gke_acme_europe-west1_main", Namespaces: []string{"app-prd"}, Production: true},
}

var (
	sessions *fakeSessions
	events   *fakeEvents
)

func newTestModel(t testing.TB, env int, repo string) (*Model, *fakeCatalog) {
	t.Helper()
	theme, err := NewTheme("light", false)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := NewKeymap(nil)
	if err != nil {
		t.Fatal(err)
	}
	fc := &fakeCatalog{}
	sessions, events = &fakeSessions{}, &fakeEvents{}
	m := NewModel(Options{
		Env: envs[env], Envs: envs, Theme: theme, Keys: keys, Source: "demo", Catalog: fc, Repo: repo,
		Sessions: sessions, Events: events, Layout: compactRenderer{}, Columns: testColumns,
		Now:    func() time.Time { return t0 },
		Filter: domain.ContainerFilter{Deny: []string{"istio-proxy", "istio-init", "vault-agent"}},
	})
	run(m, m.Init())
	return m, fc
}

// run executes a command, feeding watch-start messages back. Commands
// that block (snapshot and batch waits, timers) are abandoned: tests send
// snapshots, batches and ticks themselves.
func run(m *Model, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(20 * time.Millisecond):
		return
	}
	switch msg := msg.(type) {
	case watchStartedMsg:
		m.Update(watchStartedMsg{gen: msg.gen, err: msg.err})
	case logStartedMsg:
		if msg.err != nil {
			m.Update(msg)
		}
	case eventsMsg:
		m.Update(msg)
	case tea.BatchMsg:
		for _, c := range msg {
			run(m, c)
		}
	}
}

func snapshot(m *Model, s ports.CatalogSnapshot) {
	m.Update(snapshotMsg{gen: m.gen, snap: s, ch: make(chan ports.CatalogSnapshot)})
}

func press(m *Model, keys ...string) {
	for _, k := range keys {
		run(m, m.handleKey(keyMsg(k)))
	}
}

func keyMsg(k string) tea.KeyPressMsg {
	switch k {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "ctrl+e", "ctrl+r", "ctrl+x", "ctrl+a", "ctrl+f", "ctrl+l", "ctrl+u", "ctrl+t":
		return tea.KeyPressMsg{Code: rune(k[5]), Mod: tea.ModCtrl}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "f2":
		return tea.KeyPressMsg{Code: tea.KeyF2}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
	r := []rune(k)[0]
	return tea.KeyPressMsg{Code: r, Text: k}
}

func render(m *Model, w, h int) string {
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return ansi.Strip(m.View().Content)
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/adapters/driving/tui -update)", err)
	}
	if got != string(want) {
		t.Errorf("%s mismatch:\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func selectedRepo(m *Model) string { return m.stack[0].(*servicesScreen).selected }

func TestServicesGolden(t *testing.T) {
	m, _ := newTestModel(t, 1, "")
	golden(t, "connecting_120x20", render(m, 120, 20))
	snapshot(m, mockupSnapshot("rec"))
	golden(t, "services_rec_160x30", render(m, 160, 30))
	golden(t, "services_rec_80x24", render(m, 80, 24))
	press(m, "/", "p", "a", "y")
	golden(t, "services_filter_editing_120x20", render(m, 120, 20))
}

func TestProductionAndEnvPickerGolden(t *testing.T) {
	m, _ := newTestModel(t, 3, "")
	snapshot(m, mockupSnapshot("prd"))
	press(m, "ctrl+e")
	golden(t, "services_prd_env_picker_160x30", render(m, 160, 30))
}

// openLogs opens payment-service's logs and feeds the mockup batch.
func openLogs(t testing.TB) (*Model, *logsScreen) {
	t.Helper()
	m, _ := newTestModel(t, 1, "")
	snapshot(m, mockupSnapshot("rec"))
	press(m, "s", "g") // sort by name, go to top: payment-service is row 10
	for range 10 {
		press(m, "j")
	}
	if got := selectedRepo(m); got != "payment-service" {
		t.Fatalf("selected %q", got)
	}
	press(m, "enter")
	l, ok := m.top().(*logsScreen)
	if !ok {
		t.Fatal("enter must open the logs")
	}
	feed(m, l, paymentBatch())
	return m, l
}

func feed(m *Model, l *logsScreen, b ports.LogBatch) {
	m.Update(logBatchMsg{screen: l, gen: l.gen, batch: b, ch: make(chan ports.LogBatch)})
}

func TestLogsGolden(t *testing.T) {
	m, _ := openLogs(t)
	golden(t, "logs_160x24", render(m, 160, 24))
	golden(t, "logs_80x16", render(m, 80, 16))
	press(m, "F")
	golden(t, "logs_fullscreen_120x12", render(m, 120, 12))
	press(m, "esc")
	press(m, "T")
	golden(t, "logs_window_picker_120x20", render(m, 120, 20))
	press(m, "esc", "S")
	golden(t, "logs_pod_selector_120x12", render(m, 120, 12))
	press(m, "esc")
	if len(m.stack) != 2 {
		t.Fatal("esc must close the pod selector")
	}
	press(m, "esc")
	if len(m.stack) != 1 || sessions.ctxs[0].Err() == nil {
		t.Fatal("leaving the logs must close the session")
	}
}

func TestZoomGolden(t *testing.T) {
	m, l := openLogs(t)
	for range 5 {
		press(m, "k")
	}
	if e, _ := l.entryAt(l.displayCursor()); e.Level != domain.LevelError || e.Stack == "" {
		t.Fatalf("cursor on %+v", e)
	}
	press(m, "enter")
	golden(t, "zoom_140x40", render(m, 140, 40))
	press(m, "enter")
	golden(t, "zoom_metadata_140x40", render(m, 140, 40))
	press(m, "p")
	golden(t, "zoom_raw_140x10", render(m, 140, 10))
	press(m, "p", "J")
	if z := m.top().(*zoomScreen); z.seq != l.rows[4].seq {
		t.Fatalf("J must go to the next entry, got seq %d", z.seq)
	}
	press(m, "esc")
	if m.top() != l {
		t.Fatal("esc must return to the stream")
	}
}

func TestWatchErrorGolden(t *testing.T) {
	m, _ := newTestModel(t, 1, "")
	snapshot(m, ports.CatalogSnapshot{Env: "rec", UpdatedAt: t0, Err: domain.ErrUnauthorized})
	golden(t, "services_unreachable_120x16", render(m, 120, 16))
}

func TestFitsTerminal(t *testing.T) {
	for _, size := range [][2]int{{160, 45}, {120, 30}, {80, 24}, {60, 12}, {40, 10}} {
		m, _ := newTestModel(t, 1, "")
		snapshot(m, mockupSnapshot("rec"))
		for _, screen := range []string{"services", "logs"} {
			if screen == "logs" {
				press(m, "enter")
				feed(m, m.top().(*logsScreen), paymentBatch())
			}
			lines := strings.Split(render(m, size[0], size[1]), "\n")
			if len(lines) != size[1] {
				t.Errorf("%s %dx%d: %d lines", screen, size[0], size[1], len(lines))
			}
			for i, l := range lines {
				if w := ansi.StringWidth(l); w != size[0] {
					t.Errorf("%s %dx%d: line %d is %d cells wide", screen, size[0], size[1], i, w)
				}
			}
		}
	}
}

func TestNavigationAndStickySelection(t *testing.T) {
	m, _ := newTestModel(t, 1, "")
	snapshot(m, mockupSnapshot("rec"))
	render(m, 120, 30)
	press(m, "j", "j")
	if got := selectedRepo(m); got != "document-renderer" {
		t.Fatalf("after j j: %q", got)
	}
	// catalog-indexer recovers: rows reorder, selection stays on the repo.
	s := mockupSnapshot("rec")
	s.Services[0].Status = domain.StatusHealthy
	snapshot(m, s)
	render(m, 120, 30)
	if got := selectedRepo(m); got != "document-renderer" {
		t.Fatalf("selection lost after update: %q", got)
	}
	press(m, "G")
	if got := selectedRepo(m); got != "legacy-cron" {
		t.Fatalf("G: %q (unassigned workloads come last)", got)
	}
	press(m, "g", "k")
	if got := selectedRepo(m); got != "catalog-indexer" && got != "order-orchestrator" {
		t.Fatalf("g k: %q", got)
	}
}

func TestFilterTypingDoesNotTriggerKeys(t *testing.T) {
	m, _ := newTestModel(t, 1, "")
	snapshot(m, mockupSnapshot("rec"))
	press(m, "/", "q", "s")
	if len(m.stack) != 1 || m.stack[0].(*servicesScreen).filter.String() != "qs" {
		t.Fatal("typed keys must go to the filter")
	}
	press(m, "backspace", "backspace", "u", "s", "e", "r", "enter")
	rows := m.stack[0].(*servicesScreen).rows(m)
	if len(rows) != 1 || rows[0].Repo != "user-api" {
		t.Fatalf("filtered rows: %v", rows)
	}
	press(m, "esc")
	if n := len(m.stack[0].(*servicesScreen).rows(m)); n != 15 {
		t.Fatalf("esc must clear the filter: %d rows", n)
	}
}

func TestSortCycle(t *testing.T) {
	m, _ := newTestModel(t, 1, "")
	snapshot(m, mockupSnapshot("rec"))
	want := []string{"catalog-indexer", "audit-stream", "catalog-indexer", "email-dispatcher"}
	for i, repo := range want {
		if got := m.stack[0].(*servicesScreen).rows(m)[0].Repo; got != repo {
			t.Errorf("sort %d: first row %q, want %q", i, got, repo)
		}
		press(m, "s")
	}
}

func TestEnvSwitchRestartsWatch(t *testing.T) {
	m, fc := newTestModel(t, 1, "")
	snapshot(m, mockupSnapshot("rec"))
	press(m, "ctrl+e", "j", "j", "enter")
	if m.env.Name != "prd" || m.snap != nil || m.popup != nil {
		t.Fatalf("env %s snap %v", m.env.Name, m.snap)
	}
	if !slices.Equal(fc.calls, []domain.Env{"rec", "prd"}) {
		t.Fatalf("watch calls %v", fc.calls)
	}
	if fc.ctxs[0].Err() == nil {
		t.Fatal("previous watch must be cancelled")
	}
	if !strings.Contains(m.note, "switched rec -> prd at 19:14:02") {
		t.Fatalf("note %q", m.note)
	}
	stale := mockupSnapshot("rec")
	m.Update(snapshotMsg{gen: m.gen - 1, snap: stale, ch: make(chan ports.CatalogSnapshot)})
	if m.snap != nil {
		t.Fatal("snapshot of the old watch must be ignored")
	}
}

func TestRefreshRestartsWatch(t *testing.T) {
	m, fc := newTestModel(t, 1, "")
	snapshot(m, mockupSnapshot("rec"))
	press(m, "r")
	if len(fc.calls) != 2 || !m.resyncing {
		t.Fatalf("calls %v resyncing %v", fc.calls, m.resyncing)
	}
}

func TestLogsFollowPauseAndTail(t *testing.T) {
	m, l := openLogs(t)
	render(m, 120, 20)
	press(m, "space")
	feed(m, l, ports.LogBatch{Entries: []domain.LogEntry{logEntry(1, podA, domain.LevelInfo, "a.B", "new 1"), logEntry(2, podA, domain.LevelInfo, "a.B", "new 2")}})
	if out := render(m, 160, 20); !strings.Contains(out, "PAUSED +2") || strings.Contains(out, "new 1") {
		t.Fatalf("paused view must not move:\n%s", out)
	}
	press(m, "space")
	if out := render(m, 160, 20); !strings.Contains(out, "new 2") || !strings.Contains(out, " LIVE ") {
		t.Fatalf("resume must jump to live:\n%s", out)
	}
	press(m, "k", "k")
	feed(m, l, ports.LogBatch{Entries: []domain.LogEntry{logEntry(3, podB, domain.LevelInfo, "a.B", "new 3")}})
	if out := render(m, 160, 20); !strings.Contains(out, "+3 below") {
		t.Fatalf("scrolling up must stop auto-scroll:\n%s", out)
	}
	press(m, "G")
	if !l.tail {
		t.Fatal("G must return to the tail")
	}
	press(m, "f")
	if n := len(sessions.queries); n != 2 || sessions.queries[1].Follow || sessions.ctxs[0].Err() == nil {
		t.Fatalf("f must reopen without follow: %+v", sessions.queries)
	}
	if out := render(m, 160, 20); !strings.Contains(out, "STOPPED") {
		t.Fatal("follow off must show STOPPED")
	}
}

func TestLogsWindowKeys(t *testing.T) {
	m, l := openLogs(t)
	press(m, "é") // AZERTY "2": 30m
	press(m, "0") // tail
	press(m, "t") // next after tail wraps to 15m
	want := []string{"15m", "30m", "tail", "15m"}
	for i, q := range sessions.queries {
		if q.Window.Label() != want[i] || q.Repo != "payment-service" || q.Env != "rec" {
			t.Fatalf("query %d: %+v", i, q)
		}
	}
	if l.buf.Len() != 0 {
		t.Fatal("changing the window must reload")
	}
}

func TestLogsScopeOrderAndErrors(t *testing.T) {
	m, l := openLogs(t)
	press(m, "tab")
	if len(l.scope) != 1 || !l.scope[podA] || l.shown() != 3 {
		t.Fatalf("tab: scope %v shown %d", l.scope, l.shown())
	}
	press(m, "tab", "tab", "tab")
	if l.scope != nil || l.shown() != 9 {
		t.Fatalf("tab cycle must return to all pods: %v", l.scope)
	}
	press(m, "<")
	if e, _ := l.entryAt(l.displayCursor()); e.Message != "Card declined by issuer orderId=ord_72bf10 retryable=false" {
		t.Fatalf("< : %q", e.Message)
	}
	press(m, "<")
	if e, _ := l.entryAt(l.displayCursor()); !strings.HasPrefix(e.Message, "Payment authorization failed") {
		t.Fatalf("< < : %q", e.Message)
	}
	press(m, "o")
	if out := render(m, 160, 20); !strings.Contains(out, "newest first") {
		t.Fatal("order must show in the status bar")
	}
	if e, _ := l.entryAt(0); e.Message != "health probe succeeded components=db,redis,gateway" {
		t.Fatalf("newest first: %q", e.Message)
	}
}

func TestRepoFlagOpensRepo(t *testing.T) {
	m, _ := newTestModel(t, 1, "payment-service")
	snapshot(m, mockupSnapshot("rec"))
	if len(m.stack) != 2 {
		t.Fatal("--repo must open the repo screen")
	}
	m2, _ := newTestModel(t, 1, "nope")
	snapshot(m2, mockupSnapshot("rec"))
	if len(m2.stack) != 1 || !strings.Contains(render(m2, 160, 10), `repo "nope" not found in rec`) {
		t.Fatal("missing --repo must be reported")
	}
}

func TestQuitKeys(t *testing.T) {
	for _, k := range []string{"q", "ctrl+c"} {
		m, fc := newTestModel(t, 1, "")
		cmd := m.handleKey(keyMsg(k))
		if cmd == nil {
			t.Fatalf("%s did not quit", k)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatalf("%s did not quit", k)
		}
		if fc.ctxs[0].Err() == nil {
			t.Fatal("quit must cancel the watch")
		}
	}
}

func TestKeymapOverrides(t *testing.T) {
	km, err := NewKeymap(map[string][]string{"follow": {"ctrl+l"}})
	if err != nil {
		t.Fatal(err)
	}
	if !km.Is("ctrl+l", ActFollow) || km.Is("f", ActFollow) {
		t.Fatal("override must replace default keys")
	}
	if !km.Is("&", ActWindow1) || !km.Is("à", ActWindowTail) {
		t.Fatal("AZERTY aliases missing")
	}
	if _, err := NewKeymap(map[string][]string{"folow": {"f"}}); err == nil || !strings.Contains(err.Error(), `unknown action "folow"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestEveryActionHasAKey(t *testing.T) {
	for a, ks := range defaultKeys {
		if len(ks) == 0 {
			t.Errorf("%s has no default key", a)
		}
	}
}

func TestThemes(t *testing.T) {
	for _, n := range ThemeNames {
		if _, err := NewTheme(n, true); err != nil {
			t.Errorf("%s: %v", n, err)
		}
	}
	if _, err := NewTheme("dark", false); err == nil {
		t.Fatal("unknown theme accepted")
	}
}

func TestShortAge(t *testing.T) {
	for d, want := range map[time.Duration]string{30 * time.Second: "30s", 12 * time.Minute: "12m", 3 * time.Hour: "3h", 47 * time.Hour: "47h", 12 * 24 * time.Hour: "12d"} {
		if got := shortAge(d); got != want {
			t.Errorf("%v: %s", d, got)
		}
	}
}

func keyMsgF1() tea.KeyPressMsg { return tea.KeyPressMsg{Code: tea.KeyF1} }
