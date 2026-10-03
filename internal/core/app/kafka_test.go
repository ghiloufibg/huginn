package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
	"github.com/ghiloufibg/huginn/internal/core/ports/portstest"
)

// osRoot makes a test root absolute on this system: "/repos" is not
// absolute on Windows.
func osRoot(p string) string {
	if runtime.GOOS == "windows" {
		return "C:" + filepath.FromSlash(p)
	}
	return p
}

var kafkaNow = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

// kafkaFixture is a profile in the shape of a repository debug script:
// dotenv overlays, the secret one encrypted, one SASL account per topic.
func kafkaFixture(t *testing.T) (*KafkaService, *portstest.FakeLocalFiles, *portstest.FakeKafka) {
	t.Helper()
	files := portstest.NewFakeLocalFiles()
	files.Env["/repos/orders/deploy/base/kafka.env"] = map[string]string{"BROKERS": "b1:9093, b2:9093", "PROTOCOL": "SASL_SSL", "TOPIC_IN": "orders.in"}
	files.Env["/repos/orders/deploy/rec/secrets/kafka.env"] = map[string]string{
		"ORDERS_USERNAME": "orders-user", "ORDERS_PASSWORD": "pw1", "OUT_USERNAME": "out-user", "OUT_PASSWORD": "pw2",
		"TOPIC_IN": "orders.requested", "TOPIC_OUT": "orders.confirmed", "TOPIC_AUDIT": "orders.audit, orders.confirmed",
		"ORDERS_TRUSTSTORE_PASSWORD": "tspw",
	}
	files.Encrypted["/repos/orders/deploy/rec/secrets/kafka.env"] = true
	files.Stores["/repos/orders/src/main/resources/truststore.p12"] = portstest.FakeTrustStore{Certs: [][]byte{[]byte("der")}, Password: "tspw"}
	files.Env["/repos/billing/deploy/base/kafka.env"] = map[string]string{"BROKERS": "b1:9093"}

	k := portstest.NewFakeKafka(portstest.NewFakeClock(kafkaNow))
	k.AddTopic("orders.requested", 2)
	k.AddTopic("orders.confirmed", 1)
	k.AddTopic("orders.audit", 1)
	s := &KafkaService{
		Files: files, Sources: k, ConfigDir: osRoot("/cfg"), ReposRoot: osRoot("/repos"), Home: osRoot("/home/me"),
		Getenv:     func(k string) string { return map[string]string{"MY_PASS": "mine"}[k] },
		MaxRecords: 1000, MaxBufferBytes: 1 << 20, MaxValueBytes: 64,
		Profiles: []KafkaProfile{{
			Name:       "10-repo",
			MatchFiles: []string{"{repo_dir}/deploy/{env}/secrets/kafka.env"},
			Sources: []KafkaSourceSpec{
				{File: "{repo_dir}/deploy/base/kafka.env"},
				{File: "{repo_dir}/deploy/{env}/kafka.env", Optional: true},
				{File: "{repo_dir}/deploy/{env}/secrets/kafka.env", Sops: true},
			},
			Vars: map[string]string{"account": "ORDERS"},
			Conn: KafkaConnSpec{
				Bootstrap: "${BROKERS}", Security: "${PROTOCOL:-plaintext}", Mechanism: "SCRAM-SHA-512",
				Username: "${{account}_USERNAME}", Password: "${{account}_PASSWORD}",
				CA: "{repo_dir}/**/truststore.p12", CAPassword: "${ORDERS_TRUSTSTORE_PASSWORD}",
			},
			Topics: KafkaTopicsSpec{Discover: []string{"TOPIC_AUDIT"}},
			Repos: map[string]KafkaRepoSpec{"orders": {
				Enabled: true,
				Topics: KafkaTopicsSpec{
					Consume: []KafkaTopicSpec{{Name: "${TOPIC_IN}"}},
					Produce: []KafkaTopicSpec{{Name: "${TOPIC_OUT}", Vars: map[string]string{"account": "OUT"}}, {Name: "${TOPIC_MISSING}"}},
				},
			}},
		}},
	}
	return s, files, k
}

func TestKafkaReposChecksFilesOnly(t *testing.T) {
	s, files, _ := kafkaFixture(t)
	s.Profiles[0].Repos["legacy"] = KafkaRepoSpec{Enabled: false}
	files.Env["/repos/legacy/deploy/rec/secrets/kafka.env"] = map[string]string{}
	got := s.Repos(context.Background(), "rec", []string{"orders", "billing", "legacy", "unknown"})
	if len(got) != 1 || !got["orders"] {
		t.Fatalf("repos: %v", got)
	}
	if got := s.Repos(context.Background(), "prd", []string{"orders"}); len(got) != 0 {
		t.Fatalf("no secrets file for prd: %v", got)
	}
	if len(files.Reads) != 0 {
		t.Fatalf("Repos read files: %v", files.Reads)
	}
	delete(files.Env, "/repos/orders/deploy/rec/secrets/kafka.env")
	if got := s.Repos(context.Background(), "rec", []string{"orders"}); !got["orders"] {
		t.Fatal("answers are remembered")
	}
}

func TestKafkaOpenResolvesTopicsAndConnections(t *testing.T) {
	s, files, k := kafkaFixture(t)
	sess, err := s.Open(context.Background(), "rec", "orders")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if sess.Profile() != "10-repo" {
		t.Errorf("profile %q", sess.Profile())
	}
	type row struct {
		name string
		dir  domain.TopicDirection
		n    int
		err  string
	}
	var got []row
	for _, ts := range sess.Topics() {
		r := row{name: ts.Name, dir: ts.Direction, n: ts.Partitions}
		if ts.Err != nil {
			r.err = ts.Err.Error()
		}
		got = append(got, r)
	}
	want := []row{
		{"orders.requested", domain.TopicConsume, 2, ""}, // the encrypted overlay overrides the base
		{"orders.confirmed", domain.TopicProduce, 1, ""}, // discovered too: no direction added, listed once
		{"${TOPIC_MISSING}", domain.TopicProduce, 0, "TOPIC_MISSING not found in the sources"},
		{"orders.audit", domain.TopicNone, 1, ""},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("topics:\n got %+v\nwant %+v", got, want)
	}
	if len(k.Conns) != 2 {
		t.Fatalf("one connection per account, got %d", len(k.Conns))
	}
	users := []string{k.Conns[0].Username.Reveal(), k.Conns[1].Username.Reveal()}
	slices.Sort(users)
	c := k.Conns[0]
	if !slices.Equal(users, []string{"orders-user", "out-user"}) || c.Security != "sasl_ssl" || c.Mechanism != "scram-sha-512" ||
		!slices.Equal(c.Bootstrap, []string{"b1:9093", "b2:9093"}) || len(c.CACerts) != 1 {
		t.Fatalf("connection: users %v %+v", users, c)
	}
	if files.Reads["/repos/orders/deploy/rec/kafka.env"] != 0 || files.Reads["/repos/orders/deploy/rec/secrets/kafka.env"] != 1 {
		t.Errorf("reads: %v", files.Reads)
	}
}

func TestKafkaOpenFailures(t *testing.T) {
	ctx := context.Background()
	s, files, k := kafkaFixture(t)
	if _, err := s.Open(ctx, "rec", "billing"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("no profile: %v", err)
	}
	files.Encrypted["/repos/orders/deploy/rec/secrets/kafka.env"] = false // sops fails
	if _, err := s.Open(ctx, "rec", "orders"); !errors.Is(err, domain.ErrSecretsAccess) || !strings.Contains(err.Error(), "kafka/10-repo") {
		t.Errorf("source failure: %v", err)
	}
	files.Encrypted["/repos/orders/deploy/rec/secrets/kafka.env"] = true

	files.Stores["/repos/orders/src/main/resources/truststore.p12"] = portstest.FakeTrustStore{Password: "other"}
	sess, err := s.Open(ctx, "rec", "orders")
	if err != nil {
		t.Fatal(err)
	}
	for _, ts := range sess.Topics() {
		if ts.Err == nil || !strings.Contains(ts.Err.Error(), "wrong password") && !strings.Contains(ts.Err.Error(), "TOPIC_MISSING") {
			t.Errorf("%s: %v", ts.Name, ts.Err)
		}
	}
	if len(k.Conns) != 0 {
		t.Error("connected without a truststore")
	}
	if _, err := sess.Read(ctx, ports.KafkaQuery{Topic: "orders.requested", Window: domain.TimeWindow{Tail: 1}}); err == nil {
		t.Error("read of a failed topic")
	}

	s2, _, k2 := kafkaFixture(t)
	k2.OpenErr = fmt.Errorf("dial: %w", domain.ErrUnreachable)
	sess, err = s2.Open(ctx, "rec", "orders")
	if err != nil {
		t.Fatal(err)
	}
	if ts := sess.Topics()[0]; !errors.Is(ts.Err, domain.ErrUnreachable) {
		t.Errorf("unreachable: %+v", ts)
	}
}

func TestKafkaEnvValuesAndLiteralProfile(t *testing.T) {
	s, _, k := kafkaFixture(t)
	s.Profiles = []KafkaProfile{{
		Name:   "zz-local",
		Conn:   KafkaConnSpec{Bootstrap: "localhost:9092", Security: "sasl_plaintext", Mechanism: "plain", Username: "me", Password: "env:MY_PASS"},
		Topics: KafkaTopicsSpec{List: []KafkaTopicSpec{{Name: "orders.audit"}}},
	}}
	sess, err := s.Open(context.Background(), "rec", "anything")
	if err != nil || sess.Topics()[0].Err != nil {
		t.Fatalf("%v %+v", err, sess.Topics())
	}
	if k.Conns[0].Password.Reveal() != "mine" || k.Conns[0].TLS() {
		t.Fatalf("%+v", k.Conns[0])
	}
	s.Profiles[0].Conn.Password = "env:UNSET"
	sess, _ = s.Open(context.Background(), "rec", "anything")
	if err := sess.Topics()[0].Err; err == nil || !strings.Contains(err.Error(), "UNSET is not set") {
		t.Fatalf("unset variable: %v", err)
	}
}

func readAll(t *testing.T, ch <-chan ports.KafkaBatch, untilDone bool) ([]domain.KafkaRecord, bool) {
	t.Helper()
	var recs []domain.KafkaRecord
	timeout := time.After(5 * time.Second)
	for {
		select {
		case b, ok := <-ch:
			if !ok {
				return recs, false
			}
			if b.Err != nil {
				t.Fatal(b.Err)
			}
			recs = append(recs, b.Records...)
			if b.HistoryDone && untilDone {
				return recs, true
			}
		case <-timeout:
			t.Fatal("timeout")
		}
	}
}

func TestKafkaReadSortsAndBoundsTheHistory(t *testing.T) {
	s, _, k := kafkaFixture(t)
	for i := range 10 {
		k.Produce(domain.KafkaRecord{Topic: "orders.requested", Partition: int32(i % 2), Time: kafkaNow.Add(time.Duration(i) * time.Second), Value: []byte(strings.Repeat("x", 100))})
	}
	sess, err := s.Open(context.Background(), "rec", "orders")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	ch, err := sess.Read(context.Background(), ports.KafkaQuery{Topic: "orders.requested", Window: domain.TimeWindow{Tail: 3}})
	if err != nil {
		t.Fatal(err)
	}
	recs, _ := readAll(t, ch, false)
	if len(recs) != 6 {
		t.Fatalf("3 per partition: %d", len(recs))
	}
	for i := 1; i < len(recs); i++ {
		if recs[i].Time.Before(recs[i-1].Time) {
			t.Fatalf("not sorted: %v then %v", recs[i-1].Time, recs[i].Time)
		}
	}
	if len(recs[0].Value) != 64 || recs[0].ValueSize != 100 || !recs[0].Truncated() {
		t.Fatalf("values truncated to MaxValueBytes: %d %d", len(recs[0].Value), recs[0].ValueSize)
	}
	if r := k.Reads[0]; r.Tail != 3 || r.Limit != 3 || r.Follow {
		t.Errorf("tail read: %+v", r)
	}

	s.MaxRecords = 4
	ch, _ = sess.Read(context.Background(), ports.KafkaQuery{Topic: "orders.requested", Window: domain.TimeWindow{Since: time.Hour}, ReadCommitted: true})
	recs, _ = readAll(t, ch, false)
	if len(recs) != 4 || recs[3].Time != kafkaNow.Add(9*time.Second) {
		t.Fatalf("history bounded to the newest MaxRecords: %d", len(recs))
	}
	if r := k.Reads[1]; r.Since != time.Hour || r.Limit != 2 || !r.ReadCommitted {
		t.Errorf("window read: %+v", r)
	}
}

func TestKafkaFollowAndClose(t *testing.T) {
	s, _, k := kafkaFixture(t)
	sess, err := s.Open(context.Background(), "rec", "orders")
	if err != nil {
		t.Fatal(err)
	}
	ch, err := sess.Read(context.Background(), ports.KafkaQuery{Topic: "orders.audit", Window: domain.TimeWindow{Tail: 10}, Follow: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, done := readAll(t, ch, true); !done {
		t.Fatal("no HistoryDone")
	}
	k.Produce(domain.KafkaRecord{Topic: "orders.audit", Time: kafkaNow, Value: []byte("live")})
	select {
	case b := <-ch:
		if len(b.Records) != 1 || string(b.Records[0].Value) != "live" {
			t.Fatalf("live batch: %+v", b)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("live record not delivered")
	}
	sess.Close()
	sess.Close() // idempotent
	select {
	case _, ok := <-ch:
		for ok {
			_, ok = <-ch
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not end the read")
	}
	if k.Closed != 2 {
		t.Errorf("sources closed: %d", k.Closed)
	}
	if _, err := sess.Read(context.Background(), ports.KafkaQuery{Topic: "orders.audit", Window: domain.TimeWindow{Tail: 1}}); !errors.Is(err, domain.ErrUnreachable) {
		t.Errorf("read after close: %v", err)
	}
}

func TestKafkaSecretsNeverInErrors(t *testing.T) {
	s, files, _ := kafkaFixture(t)
	files.Env["/repos/orders/deploy/rec/secrets/kafka.env"]["PROTOCOL"] = "hunter2"
	sess, err := s.Open(context.Background(), "rec", "orders")
	if err != nil {
		t.Fatal(err)
	}
	for _, ts := range sess.Topics() {
		if ts.Err != nil && (strings.Contains(ts.Err.Error(), "pw1") || strings.Contains(ts.Err.Error(), "hunter2")) {
			t.Errorf("secret value in error: %v", ts.Err)
		}
		if ts.Name == "orders.requested" && (ts.Err == nil || !strings.Contains(ts.Err.Error(), "(from ${PROTOCOL:-plaintext})")) {
			t.Errorf("the error names where the value came from: %v", ts.Err)
		}
	}
}

func TestKafkaSessionsLeaveNoGoroutine(t *testing.T) {
	s, _, k := kafkaFixture(t)
	k.Produce(domain.KafkaRecord{Topic: "orders.audit", Time: kafkaNow, Value: []byte("x")})
	base := runtime.NumGoroutine()
	for range 20 {
		sess, err := s.Open(context.Background(), "rec", "orders")
		if err != nil {
			t.Fatal(err)
		}
		ch, err := sess.Read(context.Background(), ports.KafkaQuery{Topic: "orders.audit", Window: domain.TimeWindow{Tail: 5}, Follow: true})
		if err != nil {
			t.Fatal(err)
		}
		<-ch
		sess.Close()   // must end the following read too
		for range ch { //nolint:revive // drain
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > base && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > base {
		t.Fatalf("%d goroutines left, %d before", n, base)
	}
}

// BenchmarkKafkaHistorySort sorts and trims the history of 12 partitions
// of 10 000 records each to what the view keeps.
func BenchmarkKafkaHistorySort(b *testing.B) {
	k := &kafkaSession{s: &KafkaService{MaxRecords: 20000, MaxBufferBytes: 64 << 20}}
	src := make([]domain.KafkaRecord, 0, 120000)
	for p := range int32(12) {
		for off := range int64(10000) {
			src = append(src, domain.KafkaRecord{Partition: p, Offset: off, Time: kafkaNow.Add(time.Duration(off)*time.Second + time.Duration(p)*time.Millisecond), Value: []byte(`{"id":1}`)})
		}
	}
	recs := make([]domain.KafkaRecord, len(src))
	b.ReportAllocs()
	for b.Loop() {
		copy(recs, src)
		_ = k.trim(recs)
	}
}

func TestFinishedReadsLeaveNothingInTheSession(t *testing.T) {
	s, _, _ := kafkaFixture(t)
	sess, err := s.Open(context.Background(), "rec", "orders")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	for range 10 {
		ch, err := sess.Read(context.Background(), ports.KafkaQuery{Topic: "orders.audit", Window: domain.TimeWindow{Tail: 1}})
		if err != nil {
			t.Fatal(err)
		}
		for range ch { //nolint:revive // drain
		}
	}
	k := sess.(*kafkaSession)
	deadline := time.Now().Add(2 * time.Second)
	for {
		k.mu.Lock()
		n := len(k.cancels)
		k.mu.Unlock()
		if n == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d finished reads still held", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCoalesceMergesWaitingBatchesOnly(t *testing.T) {
	in := make(chan ports.RecordBatch, 4)
	in <- ports.RecordBatch{Records: []domain.KafkaRecord{{Offset: 2}}, Notices: []string{"n"}}
	in <- ports.RecordBatch{Records: []domain.KafkaRecord{{Offset: 3, Value: []byte("long value")}}}
	got := coalesce(ports.RecordBatch{Records: []domain.KafkaRecord{{Offset: 1}}}, in, 4)
	if len(got.Records) != 3 || len(got.Notices) != 1 || string(got.Records[2].Value) != "long" {
		t.Fatalf("%+v", got)
	}
	in <- ports.RecordBatch{Err: errors.New("boom")}
	in <- ports.RecordBatch{Records: []domain.KafkaRecord{{Offset: 9}}}
	got = coalesce(ports.RecordBatch{}, in, 0)
	if got.Err == nil || len(got.Records) != 0 || len(in) != 1 {
		t.Fatalf("stops at an error: %+v, %d left", got, len(in))
	}
}
