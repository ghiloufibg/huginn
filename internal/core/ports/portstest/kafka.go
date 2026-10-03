package portstest

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// FakeLocalFiles serves dotenv files and truststores from memory. Glob
// matches whole paths with filepath.Match; "**" matches any folders.
type FakeLocalFiles struct {
	mu sync.Mutex
	// Env holds dotenv files by path; Encrypted marks those sops would
	// decrypt; Stores holds truststores by path (certificates, password).
	Env       map[string]map[string]string
	Encrypted map[string]bool
	Stores    map[string]FakeTrustStore
	// Reads counts ReadEnv calls by path.
	Reads map[string]int
}

// FakeTrustStore is a truststore of FakeLocalFiles.
type FakeTrustStore struct {
	Certs    [][]byte
	Password string
}

// NewFakeLocalFiles returns an empty file set.
func NewFakeLocalFiles() *FakeLocalFiles {
	return &FakeLocalFiles{Env: map[string]map[string]string{}, Encrypted: map[string]bool{}, Stores: map[string]FakeTrustStore{}, Reads: map[string]int{}}
}

var _ ports.LocalFiles = (*FakeLocalFiles)(nil)

// Glob implements ports.LocalFiles.
func (f *FakeLocalFiles) Glob(_ context.Context, pattern string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, p := range slices.Concat(slices.Collect(maps.Keys(f.Env)), slices.Collect(maps.Keys(f.Stores))) {
		if globMatch(pattern, p) {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out, nil
}

// globMatch matches path elements one by one; "**" matches zero or more.
func globMatch(pattern, name string) bool {
	return matchParts(strings.Split(filepath.ToSlash(pattern), "/"), strings.Split(filepath.ToSlash(name), "/"))
}

func matchParts(ps, ns []string) bool {
	if len(ps) == 0 {
		return len(ns) == 0
	}
	if ps[0] == "**" {
		for i := 0; i <= len(ns); i++ {
			if matchParts(ps[1:], ns[i:]) {
				return true
			}
		}
		return false
	}
	if len(ns) == 0 {
		return false
	}
	ok, _ := filepath.Match(ps[0], ns[0])
	return ok && matchParts(ps[1:], ns[1:])
}

// ReadEnv implements ports.LocalFiles. A file read with the wrong
// encrypted flag fails, so tests catch a source missing sops: true.
func (f *FakeLocalFiles) ReadEnv(_ context.Context, path string, encrypted bool) (map[string]domain.Secret, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Reads[path]++
	values, ok := f.Env[path]
	if !ok {
		return nil, fmt.Errorf("%s not found: %w", path, domain.ErrNotFound)
	}
	if f.Encrypted[path] != encrypted {
		return nil, fmt.Errorf("%s: encrypted is %v, read with %v: %w", path, f.Encrypted[path], encrypted, domain.ErrSecretsAccess)
	}
	out := map[string]domain.Secret{}
	for k, v := range values {
		out[k] = domain.NewSecret(v)
	}
	return out, nil
}

// ReadTrustStore implements ports.LocalFiles.
func (f *FakeLocalFiles) ReadTrustStore(_ context.Context, path string, password domain.Secret) ([][]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.Stores[path]
	switch {
	case !ok:
		return nil, fmt.Errorf("%s not found: %w", path, domain.ErrNotFound)
	case s.Password != password.Reveal():
		return nil, fmt.Errorf("truststore %s: wrong password: %w", path, domain.ErrConfig)
	}
	return s.Certs, nil
}

// FakeKafka is an in-memory cluster: a TopicSourceFactory whose sources
// read its topics. It records the connections opened and the reads.
type FakeKafka struct {
	mu     sync.Mutex
	clock  ports.Clock
	topics map[string][][]domain.KafkaRecord // topic → partitions
	live   map[string][]chan domain.KafkaRecord
	// OpenErr fails Open; Conns are the connections opened; Reads the
	// reads made; Closed counts closed sources.
	OpenErr error
	Conns   []domain.KafkaConnection
	Reads   []ports.TopicRead
	Closed  int
}

// NewFakeKafka returns a cluster without topics, using clock for Since.
func NewFakeKafka(clock ports.Clock) *FakeKafka {
	return &FakeKafka{clock: clock, topics: map[string][][]domain.KafkaRecord{}, live: map[string][]chan domain.KafkaRecord{}}
}

// AddTopic creates a topic with n empty partitions.
func (k *FakeKafka) AddTopic(name string, n int) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.topics[name] = make([][]domain.KafkaRecord, n)
}

// Produce appends a record to its partition (offset assigned) and delivers
// it to following reads.
func (k *FakeKafka) Produce(r domain.KafkaRecord) {
	k.mu.Lock()
	defer k.mu.Unlock()
	parts := k.topics[r.Topic]
	r.Offset = int64(len(parts[r.Partition]))
	if r.KeySize == 0 {
		r.KeySize = len(r.Key)
	}
	if r.ValueSize == 0 {
		r.ValueSize = len(r.Value)
	}
	parts[r.Partition] = append(parts[r.Partition], r)
	for _, ch := range k.live[r.Topic] {
		select {
		case ch <- r:
		default: // a slow reader misses records, as a test would notice
		}
	}
}

// Open implements ports.TopicSourceFactory.
func (k *FakeKafka) Open(_ context.Context, conn domain.KafkaConnection) (ports.TopicSource, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.OpenErr != nil {
		return nil, k.OpenErr
	}
	k.Conns = append(k.Conns, conn)
	return &fakeTopicSource{k: k}, nil
}

type fakeTopicSource struct{ k *FakeKafka }

func (s *fakeTopicSource) Close() {
	s.k.mu.Lock()
	defer s.k.mu.Unlock()
	s.k.Closed++
}

func (s *fakeTopicSource) Describe(_ context.Context, topics []string) ([]ports.TopicInfo, error) {
	s.k.mu.Lock()
	defer s.k.mu.Unlock()
	out := make([]ports.TopicInfo, len(topics))
	for i, t := range topics {
		out[i].Name = t
		if parts, ok := s.k.topics[t]; ok {
			out[i].Partitions = len(parts)
		} else {
			out[i].Err = fmt.Errorf("topic %s: %w", t, domain.ErrNotFound)
		}
	}
	return out, nil
}

func (s *fakeTopicSource) Read(ctx context.Context, q ports.TopicRead) (<-chan ports.RecordBatch, error) {
	k := s.k
	k.mu.Lock()
	k.Reads = append(k.Reads, q)
	parts, ok := k.topics[q.Topic]
	if !ok {
		k.mu.Unlock()
		return nil, fmt.Errorf("topic %s: %w", q.Topic, domain.ErrNotFound)
	}
	var hist []domain.KafkaRecord
	for p, recs := range parts {
		if len(q.Partitions) > 0 && !slices.Contains(q.Partitions, int32(p)) {
			continue
		}
		start := max(len(recs)-q.Tail, 0)
		if q.Since > 0 {
			from := k.clock.Now().Add(-q.Since)
			start = slices.IndexFunc(recs, func(r domain.KafkaRecord) bool { return !r.Time.Before(from) })
			if start < 0 {
				start = len(recs)
			}
		}
		if q.Limit > 0 {
			start = max(start, len(recs)-q.Limit)
		}
		hist = append(hist, slices.Clone(recs[start:])...)
	}
	var live chan domain.KafkaRecord
	if q.Follow {
		live = make(chan domain.KafkaRecord, 1024)
		k.live[q.Topic] = append(k.live[q.Topic], live)
	}
	k.mu.Unlock()

	out := make(chan ports.RecordBatch, 4)
	go func() {
		defer close(out)
		out <- ports.RecordBatch{Records: hist, HistoryDone: true}
		if live == nil {
			return
		}
		defer s.unsubscribe(q.Topic, live)
		for {
			select {
			case <-ctx.Done():
				return
			case r := <-live:
				select {
				case out <- ports.RecordBatch{Records: []domain.KafkaRecord{r}}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

func (s *fakeTopicSource) unsubscribe(topic string, ch chan domain.KafkaRecord) {
	s.k.mu.Lock()
	defer s.k.mu.Unlock()
	s.k.live[topic] = slices.DeleteFunc(s.k.live[topic], func(c chan domain.KafkaRecord) bool { return c == ch })
}
