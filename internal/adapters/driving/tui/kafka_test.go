package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// fakeKafka is a ports.Kafka with fixed topics.
type fakeKafka struct {
	mu      sync.Mutex
	repos   map[string]bool
	asked   int
	openErr error
	session *fakeKafkaSession
}

func (f *fakeKafka) Repos(_ context.Context, _ domain.Env, repos []string) map[string]bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked++
	out := map[string]bool{}
	for _, r := range repos {
		if f.repos[r] {
			out[r] = true
		}
	}
	return out
}

func (f *fakeKafka) Open(context.Context, domain.Env, string) (ports.KafkaSession, error) {
	if f.openErr != nil {
		return nil, f.openErr
	}
	return f.session, nil
}

type fakeKafkaSession struct {
	mu      sync.Mutex
	topics  []ports.KafkaTopicState
	queries []ports.KafkaQuery
	closed  bool
	decodes bool
}

func (s *fakeKafkaSession) Profile() string                 { return "demo" }
func (s *fakeKafkaSession) Decodes() bool                   { return s.decodes }
func (s *fakeKafkaSession) Topics() []ports.KafkaTopicState { return s.topics }
func (s *fakeKafkaSession) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
}

func (s *fakeKafkaSession) Read(_ context.Context, q ports.KafkaQuery) (<-chan ports.KafkaBatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queries = append(s.queries, q)
	return make(chan ports.KafkaBatch), nil
}

func newKafkaModel(t testing.TB) (*Model, *fakeKafka) {
	t.Helper()
	m, _ := newTestModel(t, 1, "")
	fk := &fakeKafka{repos: map[string]bool{"payment-service": true}, session: &fakeKafkaSession{topics: []ports.KafkaTopicState{
		{Name: "payments.dlq", Direction: domain.TopicProduce, Partitions: 1},
		{Name: "payments.requested", Direction: domain.TopicConsume, Partitions: 3},
		{Name: "payments.completed", Direction: domain.TopicBoth, Partitions: 6},
		{Name: "${TOPIC_AUDIT}", Direction: domain.TopicNone, Err: fmt.Errorf("%w", &domain.MissingKeyError{Key: "TOPIC_AUDIT"})},
		{Name: "ledger.snapshots", Direction: domain.TopicNone, Partitions: 2, Err: fmt.Errorf("topic ledger.snapshots: %w", domain.ErrForbidden)},
	}}}
	m.opts.Kafka, m.opts.KafkaTail, m.opts.KafkaMaxRecords, m.opts.KafkaMaxBytes = fk, 100, 1000, 1<<20
	snapshotKafka(m)
	return m, fk
}

// snapshotKafka sends a snapshot and runs the Kafka question it asks
// (snapshot drops the commands of the update).
func snapshotKafka(m *Model) {
	asked := m.kafkaAsked
	snapshot(m, mockupSnapshot("rec"))
	if m.kafkaAsked != asked {
		m.kafkaAsked = asked
		run(m, m.askKafka())
	}
}

// selectRepo moves the services cursor to repo (sorted by name).
func selectRepo(t testing.TB, m *Model, repo string) {
	t.Helper()
	press(m, "s", "g")
	for range 20 {
		if selectedRepo(m) == repo {
			m.flashText = "" // "sort name" would stay in every golden
			return
		}
		press(m, "j")
	}
	t.Fatalf("%s not found", repo)
}

func kafkaRecords(n int) []domain.KafkaRecord {
	var out []domain.KafkaRecord
	for i := range n {
		v := fmt.Appendf(nil, `{"id":"PAY-%05d","status":"PAID","amount":%d.50}`, i, 10+i)
		r := domain.KafkaRecord{
			Topic: "payments.requested", Partition: int32(i % 3), Offset: int64(1000 + i), Time: t0.Add(time.Duration(i) * time.Second), Key: fmt.Appendf(nil, "PAY-%05d", i), Value: v,
			Headers: []domain.KafkaHeader{{Key: "traceId", Value: []byte("a1b2c3d4e5f60718")}},
		}
		switch i {
		case 2:
			r.Value = nil
		case 3:
			r.Value = []byte{0, 0, 0, 1, 0x9c, 2, 'x'}
		case 4:
			r.Value, r.Key = []byte("plain text event\nsecond line"), nil
		}
		r.KeySize, r.ValueSize = len(r.Key), len(r.Value)
		out = append(out, r)
	}
	return out
}

func feedKafka(m *Model, r *kafkaRecordsScreen, b ports.KafkaBatch) {
	m.Update(kafkaBatchMsg{screen: r, gen: r.gen, batch: b, ch: make(chan ports.KafkaBatch)})
}

func TestKafkaGolden(t *testing.T) {
	m, fk := newKafkaModel(t)
	if fk.asked != 1 {
		t.Fatalf("Repos asked %d times", fk.asked)
	}
	snapshotKafka(m)
	if fk.asked != 1 {
		t.Fatal("same repositories: not asked again")
	}
	selectRepo(t, m, "payment-service")
	golden(t, "kafka_services_120x20", render(m, 120, 20))

	press(m, "M")
	topics, ok := m.top().(*kafkaTopicsScreen)
	if !ok {
		t.Fatalf("M must open the Kafka topics, top is %T", m.top())
	}
	golden(t, "kafka_topics_120x16", render(m, 120, 16))

	press(m, "enter") // payments.requested: the first consumed topic
	r, ok := m.top().(*kafkaRecordsScreen)
	if !ok || r.topic.Name != "payments.requested" {
		t.Fatalf("enter must open the records, top is %T", m.top())
	}
	if q := fk.session.queries[0]; q.Window.Tail != 100 || q.Follow || q.ReadCommitted {
		t.Fatalf("first read: %+v", q)
	}
	golden(t, "kafka_records_loading_120x10", render(m, 120, 10))
	feedKafka(m, r, ports.KafkaBatch{Records: kafkaRecords(8), HistoryDone: true})
	golden(t, "kafka_records_120x14", render(m, 120, 14))

	press(m, "k", "k", "k", "enter")
	golden(t, "kafka_zoom_100x24", render(m, 100, 24))
	press(m, "esc", "esc", "esc")
	if m.top() != m.stack[0] || !fk.session.closed {
		t.Fatalf("esc closes the screens and the session (top %T, closed %v)", m.top(), fk.session.closed)
	}
	_ = topics
}

func TestKafkaRecordsKeys(t *testing.T) {
	m, fk := newKafkaModel(t)
	selectRepo(t, m, "payment-service")
	press(m, "M", "enter")
	r := m.top().(*kafkaRecordsScreen)
	feedKafka(m, r, ports.KafkaBatch{Records: kafkaRecords(8), HistoryDone: true})

	press(m, "/", "p", "a", "y", "-", "0", "0", "0", "0", "6", "enter")
	if len(r.rows) != 1 {
		t.Fatalf("filter: %d rows", len(r.rows))
	}
	press(m, "esc")
	if len(r.rows) != 8 {
		t.Fatalf("esc clears the filter: %d rows", len(r.rows))
	}

	press(m, "space")
	feedKafka(m, r, ports.KafkaBatch{Records: kafkaRecords(2)})
	if len(r.rows) != 8 || len(r.held) != 2 {
		t.Fatalf("paused: rows %d held %d", len(r.rows), len(r.held))
	}
	press(m, "space")
	if len(r.rows) != 10 {
		t.Fatalf("resumed: %d", len(r.rows))
	}

	press(m, "f")
	if q := fk.session.queries[len(fk.session.queries)-1]; !q.Follow || len(r.rows) != 0 {
		t.Fatalf("f reopens following: %+v", q)
	}
	press(m, "i")
	if q := fk.session.queries[len(fk.session.queries)-1]; !q.ReadCommitted {
		t.Fatalf("i switches isolation: %+v", q)
	}
	press(m, "5")
	if q := fk.session.queries[len(fk.session.queries)-1]; q.Window.Since != time.Hour {
		t.Fatalf("5 is the 1h window: %+v", q)
	}
	press(m, "0")
	if q := fk.session.queries[len(fk.session.queries)-1]; q.Window.Tail != 100 {
		t.Fatalf("0 is the tail: %+v", q)
	}
	feedKafka(m, r, ports.KafkaBatch{Err: fmt.Errorf("fetch: %w", domain.ErrUnreachable)})
	if !strings.Contains(render(m, 120, 12), "Cannot read payments.requested") {
		t.Fatal("read error shown")
	}
}

func TestKafkaBufferBounds(t *testing.T) {
	m, _ := newKafkaModel(t)
	m.opts.KafkaMaxRecords = 1000
	selectRepo(t, m, "payment-service")
	press(m, "M", "enter")
	r := m.top().(*kafkaRecordsScreen)
	for range 3 {
		feedKafka(m, r, ports.KafkaBatch{Records: kafkaRecords(500)})
	}
	if r.buf.Len() != 1000 || len(r.rows) != 1000 || r.buf.Dropped() != 500 || len(r.previews) > 1000 {
		t.Fatalf("len %d rows %d dropped %d previews %d", r.buf.Len(), len(r.rows), r.buf.Dropped(), len(r.previews))
	}
	if !strings.Contains(render(m, 160, 10), "500 older records dropped") {
		t.Fatal("drops are reported")
	}
	// Previews of records hidden by a filter are released once evicted.
	for i := range r.rows {
		r.previews[r.rows[i]] = "x"
	}
	press(m, "/", "z", "z", "z", "enter")
	for range 3 {
		feedKafka(m, r, ports.KafkaBatch{Records: kafkaRecords(1000)})
	}
	if len(r.previews) > 2*r.buf.Len() {
		t.Fatalf("previews kept: %d", len(r.previews))
	}
}

func TestKafkaAbsentAndErrors(t *testing.T) {
	m, _ := newTestModel(t, 1, "")
	snapshot(m, mockupSnapshot("rec"))
	selectRepo(t, m, "payment-service")
	press(m, "M")
	if _, ok := m.top().(*servicesScreen); !ok {
		t.Fatal("without Kafka, M does nothing")
	}
	if strings.Contains(render(m, 160, 30), "payment-service K") {
		t.Fatal("no marker without Kafka")
	}

	m, fk := newKafkaModel(t)
	selectRepo(t, m, "user-api")
	press(m, "M")
	if _, ok := m.top().(*servicesScreen); !ok {
		t.Fatal("M on a repository without Kafka does nothing")
	}
	fk.openErr = fmt.Errorf("sops cannot decrypt x.env: no key: %w", domain.ErrSecretsAccess)
	selectRepo(t, m, "payment-service")
	press(m, "M")
	out := render(m, 120, 14)
	if !strings.Contains(out, "Cannot open the Kafka topics of payment-service") || !strings.Contains(out, "sops cannot decrypt") {
		t.Fatalf("open error:\n%s", out)
	}
	if !errors.Is(fk.openErr, domain.ErrSecretsAccess) {
		t.Fatal("unreachable")
	}
}

func TestKafkaHelp(t *testing.T) {
	m, _ := newKafkaModel(t)
	selectRepo(t, m, "payment-service")
	press(m, "M", "enter", "?")
	out := render(m, 100, 60)
	for _, want := range []string{"KAFKA RECORDS", "isolation", "partition=<n>"} {
		if !strings.Contains(out, want) {
			t.Errorf("help misses %q:\n%s", want, out)
		}
	}
}

func TestKafkaOrderAndCopy(t *testing.T) {
	m, _ := newKafkaModel(t)
	selectRepo(t, m, "payment-service")
	press(m, "M", "enter")
	r := m.top().(*kafkaRecordsScreen)
	feedKafka(m, r, ports.KafkaBatch{Records: kafkaRecords(8), HistoryDone: true})

	press(m, "o")
	lines := strings.Split(render(m, 120, 14), "\n")
	if !strings.Contains(lines[2], "#1007") || !strings.Contains(lines[9], "#1000") {
		t.Fatalf("newest first:\n%s", strings.Join(lines, "\n"))
	}
	if r.cursor != 7 {
		t.Fatalf("the cursor stays on the newest record: %d", r.cursor)
	}
	press(m, "j") // down the screen: an older record
	if r.cursor != 6 || r.tail {
		t.Fatalf("j in newest-first order: cursor %d tail %v", r.cursor, r.tail)
	}
	press(m, "g")
	if r.cursor != 7 || !r.tail {
		t.Fatalf("g goes to the top, the newest: %d", r.cursor)
	}

	fc := withClipboards(m)
	m.opts.Redactor, _ = domain.NewRedactor([]string{`PAY-\d+`})
	cmd := r.copyCmd(m)
	if cmd == nil || !strings.Contains(m.flashText, "copied p1 #1007 (") {
		t.Fatalf("copy: %q", m.flashText)
	}
	run(m, cmd)
	if got := lastCopy(t, fc); got != `{"id":"[redacted]","status":"PAID","amount":17.50}` {
		t.Fatalf("the value as received, redacted: %q", got)
	}
	r.cursor = 2 // the tombstone
	if cmd := r.copyCmd(m); cmd != nil || !strings.Contains(m.flashText, "tombstone") {
		t.Fatalf("tombstone: %q", m.flashText)
	}
	r.cursor = 3 // framed binary value: a hex dump
	if cmd := r.copyCmd(m); cmd == nil || !strings.Contains(m.flashText, "p0 #1003") {
		t.Fatalf("binary: %q", m.flashText)
	}
}

// copyCmd presses the copy key and returns its command.
func (r *kafkaRecordsScreen) copyCmd(m *Model) tea.Cmd {
	_, cmd := r.key(m, keyMsg("ctrl+y"))
	return cmd
}

func TestKafkaReviewFixes(t *testing.T) {
	m, fk := newKafkaModel(t)

	// An older answer about the repositories never replaces a newer one.
	m.Update(kafkaReposMsg{env: "rec", key: "stale", repos: map[string]bool{}})
	if !m.kafkaRepos["payment-service"] {
		t.Fatal("a stale answer replaced the current one")
	}

	// A session opened for a screen that is gone is closed.
	gone := newKafkaTopicsScreen("payment-service")
	s := &fakeKafkaSession{}
	m.Update(kafkaOpenedMsg{screen: gone, gen: 1, session: s})
	if !s.closed {
		t.Fatal("the session of a closed screen leaks")
	}

	// Records held while paused are bounded by bytes too.
	m.opts.KafkaMaxBytes = 1 << 20
	selectRepo(t, m, "payment-service")
	press(m, "M", "enter")
	r := m.top().(*kafkaRecordsScreen)
	feedKafka(m, r, ports.KafkaBatch{Records: kafkaRecords(5), HistoryDone: true})
	press(m, "space")
	big := kafkaRecords(40)
	for i := range big {
		big[i].Value = make([]byte, 64<<10)
	}
	feedKafka(m, r, ports.KafkaBatch{Records: big})
	if r.heldBytes > 1<<20 || r.lost == 0 {
		t.Fatalf("held %d bytes, lost %d", r.heldBytes, r.lost)
	}
	press(m, "space")
	if r.heldBytes != 0 || len(r.held) != 0 {
		t.Fatal("resume empties the held records")
	}
	_ = fk
}

func TestKafkaNewestFirstKeepsTheViewOnEviction(t *testing.T) {
	m, _ := newKafkaModel(t)
	m.opts.KafkaMaxRecords = 1000
	selectRepo(t, m, "payment-service")
	press(m, "M", "enter")
	r := m.top().(*kafkaRecordsScreen)
	feedKafka(m, r, ports.KafkaBatch{Records: kafkaRecords(1000), HistoryDone: true})
	press(m, "o")
	render(m, 120, 14)
	for range 20 {
		press(m, "j")
	}
	render(m, 120, 14)
	line, seq := r.display(r.cursor)-r.offset, r.rows[r.cursor]
	feedKafka(m, r, ports.KafkaBatch{Records: kafkaRecords(10)}) // 10 newer above, 10 oldest evicted
	render(m, 120, 14)
	if r.rows[r.cursor] != seq {
		t.Fatal("the cursor left its record")
	}
	if got := r.display(r.cursor) - r.offset; got != line {
		t.Fatalf("the view drifted: the cursor moved from line %d to %d", line, got)
	}
}

func TestKafkaPauseStopsReading(t *testing.T) {
	m, _ := newKafkaModel(t)
	selectRepo(t, m, "payment-service")
	press(m, "M", "enter")
	r := m.top().(*kafkaRecordsScreen)
	ch := make(chan ports.KafkaBatch)
	if _, cmd := r.update(m, kafkaBatchMsg{screen: r, gen: r.gen, batch: ports.KafkaBatch{Records: kafkaRecords(3), HistoryDone: true}, ch: ch}); cmd == nil {
		t.Fatal("running: the next batch is awaited")
	}
	press(m, "space")
	if _, cmd := r.update(m, kafkaBatchMsg{screen: r, gen: r.gen, batch: ports.KafkaBatch{Records: kafkaRecords(2)}, ch: ch}); cmd != nil || r.stalled == nil {
		t.Fatal("paused: no batch is awaited, so the read blocks up to the brokers")
	}
	if !strings.Contains(m.flashText, "not read meanwhile") {
		t.Fatalf("flash %q", m.flashText)
	}
	_, cmd := r.key(m, keyMsg("space"))
	if cmd == nil || r.stalled != nil || len(r.rows) != 5 {
		t.Fatalf("resume awaits the read again and shows the held batch: cmd %v rows %d", cmd != nil, len(r.rows))
	}
}

func TestKafkaRecordsPolish(t *testing.T) {
	m, _ := newKafkaModel(t)
	selectRepo(t, m, "payment-service")
	press(m, "M", "enter")
	r := m.top().(*kafkaRecordsScreen)
	recs := kafkaRecords(40)
	recs[0].Time = m.opts.Now().Add(-36 * time.Hour)
	feedKafka(m, r, ports.KafkaBatch{Records: recs, HistoryDone: true})

	out := render(m, 120, 14)
	if !strings.Contains(out, "TIME") || !strings.Contains(out, "OFFSET") {
		t.Fatalf("column header:\n%s", out)
	}
	if day := recs[1].Time.In(time.Local).Format("01-02 "); !strings.Contains(out, day) {
		t.Fatalf("a record from another day shows its date %q:\n%s", day, out)
	}

	press(m, "g") // the oldest: the newest are below
	out = render(m, 120, 14)
	if !strings.Contains(out, " newer") {
		t.Fatalf("records below the screen are counted:\n%s", out)
	}

	r.follow = true
	feedKafka(m, r, ports.KafkaBatch{Records: kafkaRecords(10)})
	if !strings.Contains(r.statusLeft(m), "LIVE ") || !strings.Contains(r.statusLeft(m), "/s") {
		t.Fatalf("live rate: %q", r.statusLeft(m))
	}
}

func TestJSONKeyLine(t *testing.T) {
	th := classicTheme()
	for _, tc := range []struct{ in, key string }{
		{`  "id": "PAY-1",`, `"id"`},
		{`  "a\"b": 1`, `"a\"b"`},
		{`  "only a string"`, ""},
		{`  }`, ""},
		{`  "unterminated`, ""},
	} {
		got := jsonKeyLine(&th, tc.in)
		if ansi.Strip(got) != tc.in {
			t.Errorf("%q: text changed to %q", tc.in, ansi.Strip(got))
		}
		if colored := got != tc.in; colored != (tc.key != "") || tc.key != "" && !strings.Contains(got, th.Key.Render(tc.key)) {
			t.Errorf("%q: key colouring %q", tc.in, got)
		}
	}
}
