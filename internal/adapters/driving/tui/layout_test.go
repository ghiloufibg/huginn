package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// layoutSizes go from a split pane to a wide screen.
var layoutSizes = [][2]int{{20, 5}, {30, 8}, {40, 10}, {60, 12}, {80, 24}, {120, 30}, {220, 60}}

// layoutScreens open every screen and popup, and the error and loading
// states, each on a fresh model.
var layoutScreens = []struct {
	name string
	env  string // the environment the header must always name
	open func(t *testing.T) *Model
}{
	{"connecting", "REC", func(t *testing.T) *Model { m, _ := newTestModel(t, 1, ""); return m }},
	{"services", "REC", services(1, "rec")},
	{"services prd", "PRD", services(3, "prd")},
	{"preview", "REC", services(1, "rec", "p")},
	{"filter", "REC", services(1, "rec", "/", "p", "a")},
	{"env picker", "PRD", services(3, "prd", "ctrl+e")},
	{"help", "REC", services(1, "rec", "?")},
	{"not logged in", "REC", failing(errNotLoggedIn)},
	{"stale", "REC", func(t *testing.T) *Model { m, _ := newTestModel(t, 1, ""); snapshot(m, staleSnapshot()); return m }},
	{"unreachable", "REC", failing(fmt.Errorf("namespace app-rec: %w", domain.KindError(domain.ErrUnreachable,
		`Get "https://10.255.255.1:6443/apis/apps/v1/namespaces/app-rec/deployments?limit=1": dial tcp 10.255.255.1:6443: i/o timeout`)))},
	{"configuration", "REC", failing(domain.KindError(domain.ErrConfig, "kube context gke_acme_europe-west1_main: context was not found for specified context: gke_acme_europe-west1_main"))},
	{"logs", "REC", logs()},
	{"pod selector", "REC", logs("S")},
	{"logs fullscreen", "REC", logs("F")},
	{"zoom", "REC", logs("enter")},
	{"window picker", "REC", logs("T")},
	{"level picker", "REC", logs("l")},
	{"columns picker", "REC", logs("C")},
	{"logs error", "REC", func(t *testing.T) *Model {
		m, l := openLogs(t)
		l.err = fmt.Errorf("logs of payment-service-7d9f8b6c5d-m8q7v/app: %w", domain.ErrForbidden)
		return m
	}},
	{"kafka topics", "REC", kafka(false)},
	{"kafka records", "REC", kafka(true)},
	{"kafka zoom", "REC", kafka(true, "enter")},
	{"kafka records error", "REC", func(t *testing.T) *Model {
		m := kafka(false, "enter")(t)
		r := m.top().(*kafkaRecordsScreen)
		r.err, r.loading = fmt.Errorf("topic payments.requested: %w", domain.ErrUnreachable), false
		return m
	}},
}

// kafka opens the Kafka topics of payment-service, then the records of
// its first topic when records is set, then presses keys.
func kafka(records bool, keys ...string) func(t *testing.T) *Model {
	return func(t *testing.T) *Model {
		m, _ := newKafkaModel(t)
		selectRepo(t, m, "payment-service")
		press(m, "M")
		if records {
			press(m, "enter")
			feedKafka(m, m.top().(*kafkaRecordsScreen), ports.KafkaBatch{Records: kafkaRecords(8), HistoryDone: true})
		}
		press(m, keys...)
		return m
	}
}

func services(env int, name string, keys ...string) func(t *testing.T) *Model {
	return func(t *testing.T) *Model {
		m, _ := newTestModel(t, env, "")
		snapshot(m, mockupSnapshot(name))
		press(m, keys...)
		return m
	}
}

func failing(err error) func(t *testing.T) *Model {
	return func(t *testing.T) *Model {
		m, _ := newTestModel(t, 1, "")
		snapshot(m, ports.CatalogSnapshot{Env: "rec", UpdatedAt: t0, Err: err})
		return m
	}
}

func logs(keys ...string) func(t *testing.T) *Model {
	return func(t *testing.T) *Model {
		m, _ := openLogs(t)
		press(m, keys...)
		return m
	}
}

// TestLayoutFitsEverySize: every screen fills the terminal exactly, at
// every size, without a line wider than the screen, and the header always
// names the environment (it matters most in production).
func TestLayoutFitsEverySize(t *testing.T) {
	for _, s := range layoutScreens {
		t.Run(s.name, func(t *testing.T) {
			m := s.open(t)
			for _, size := range layoutSizes {
				w, h := size[0], size[1]
				out := render(m, w, h)
				lines := strings.Split(out, "\n")
				if len(lines) != h {
					t.Errorf("%dx%d: %d lines, want %d\n%s", w, h, len(lines), h, out)
				}
				for i, l := range lines {
					if lw := ansi.StringWidth(l); lw > w {
						t.Errorf("%dx%d: line %d is %d cells wide: %q", w, h, i, lw, l)
					}
				}
				if !strings.Contains(lines[0], s.env) {
					t.Errorf("%dx%d: the header does not name %s: %q", w, h, s.env, lines[0])
				}
			}
		})
	}
}

// TestLayoutScreensOpen guards the table above: a key that no longer opens
// its screen or popup would make the size test check the wrong thing.
func TestLayoutScreensOpen(t *testing.T) {
	want := map[string]string{
		"logs": "*tui.logsScreen", "pod selector": "*tui.podSelector", "zoom": "*tui.zoomScreen", "help": "*tui.helpScreen",
		"env picker": "*tui.envPicker", "window picker": "*tui.windowPicker", "level picker": "*tui.levelPicker", "columns picker": "*tui.columnsPicker",
		"kafka topics": "*tui.kafkaTopicsScreen", "kafka records": "*tui.kafkaRecordsScreen", "kafka zoom": "*tui.kafkaZoomScreen",
	}
	for _, s := range layoutScreens {
		name, ok := want[s.name]
		if !ok {
			continue
		}
		m := s.open(t)
		got := fmt.Sprintf("%T", m.top())
		if m.popup != nil {
			got = fmt.Sprintf("%T", m.popup)
		}
		if got != name {
			t.Errorf("%s: shows %s, want %s", s.name, got, name)
		}
	}
}

// TestConnectingShowsElapsed: a cluster that does not answer takes a while
// to time out; after 2 s the wait is counted, and the timeout says what to
// check.
func TestConnectingShowsElapsed(t *testing.T) {
	m, _ := newTestModel(t, 1, "")
	if out := render(m, 80, 12); !strings.Contains(out, "connecting to rec") || strings.Contains(out, "connecting to rec ·") {
		t.Fatalf("no count at first:\n%s", out)
	}
	m.opts.Now = func() time.Time { return t0.Add(7 * time.Second) }
	if out := render(m, 80, 12); !strings.Contains(out, "connecting to rec · 7s") {
		t.Fatalf("elapsed time:\n%s", out)
	}
	snapshot(m, ports.CatalogSnapshot{Env: "rec", UpdatedAt: t0, Err: domain.ErrUnreachable})
	if out := render(m, 80, 12); !strings.Contains(out, "Check your network or VPN access to the cluster.") {
		t.Fatalf("unreachable advice:\n%s", out)
	}
}

// staleSnapshot is rec after its session expired 20 minutes ago: the
// services are kept as last seen.
func staleSnapshot() ports.CatalogSnapshot {
	s := mockupSnapshot("rec")
	for i := range s.Services {
		for _, w := range s.Services[i].WorkloadStates {
			s.Services[i].Refs = append(s.Services[i].Refs, w.Ref)
		}
	}
	s.NamespaceErrs = map[string]error{"app-rec": errNotLoggedIn}
	s.Err, s.StaleSince = errNotLoggedIn, t0.Add(-20*time.Minute)
	return s
}

// TestStaleServices: when the watch is lost, the rows kept say so, in
// words as well as dimmed, and the status bar says since when, first so a
// narrow terminal keeps it.
func TestStaleServices(t *testing.T) {
	m, _ := newTestModel(t, 1, "")
	snapshot(m, staleSnapshot())
	out := render(m, 140, 22)
	golden(t, "services_stale_140x22", out)
	if !strings.Contains(out, "stale · ") || !strings.Contains(out, "(20m)") {
		t.Fatalf("stale rows not marked:\n%s", out)
	}
	if lines := strings.Split(render(m, 50, 12), "\n"); !strings.Contains(lines[len(lines)-2], "stale since") {
		t.Errorf("narrow status bar: %q", lines[len(lines)-2])
	}
	snapshot(m, mockupSnapshot("rec"))
	if out := render(m, 140, 22); strings.Contains(out, "stale") {
		t.Errorf("back to normal:\n%s", out)
	}
}
