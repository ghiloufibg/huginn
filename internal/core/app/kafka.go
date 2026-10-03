package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// KafkaService implements ports.Kafka: it finds the profile of a
// repository, reads its sources, resolves each topic's connection and
// reads topics through TopicSources, strictly read only (D-040).
type KafkaService struct {
	Profiles []KafkaProfile
	Files    ports.LocalFiles
	Sources  ports.TopicSourceFactory
	// ConfigDir resolves relative paths; ReposRoot gives {repo_dir}; Home
	// expands ~/.
	ConfigDir, ReposRoot, Home string
	// Getenv reads env:VAR values.
	Getenv func(string) string
	// MaxRecords and MaxBufferBytes bound the history kept per read (the
	// view keeps no more); MaxValueBytes truncates keys and values.
	MaxRecords, MaxBufferBytes, MaxValueBytes int
	Log                                       *slog.Logger

	mu    sync.Mutex
	known map[kafkaRepoKey]bool
}

type kafkaRepoKey struct {
	env  domain.Env
	repo string
}

var _ ports.Kafka = (*KafkaService)(nil)

// applied is a profile applied to a repository.
type applied struct {
	p    KafkaProfile
	repo KafkaRepoSpec
	base map[string]string // env, repo, repo_dir
}

// Repos implements ports.Kafka.
func (s *KafkaService) Repos(ctx context.Context, env domain.Env, repos []string) map[string]bool {
	out := map[string]bool{}
	for _, r := range repos {
		k := kafkaRepoKey{env, r}
		s.mu.Lock()
		has, ok := s.known[k]
		s.mu.Unlock()
		if !ok {
			_, has = s.profileFor(ctx, env, r)
			s.mu.Lock()
			if s.known == nil {
				s.known = map[kafkaRepoKey]bool{}
			}
			s.known[k] = has
			s.mu.Unlock()
		}
		if has {
			out[r] = true
		}
	}
	return out
}

// profileFor returns the first profile that applies to repo in env.
func (s *KafkaService) profileFor(ctx context.Context, env domain.Env, repo string) (applied, bool) {
	for _, p := range s.Profiles {
		spec, listed := p.Repos[repo]
		if listed && !spec.Enabled {
			return applied{}, false
		}
		if !listed && !matchesRepo(p.MatchRepos, repo) {
			continue
		}
		a := applied{p: p, repo: spec, base: s.baseVars(env, repo, spec)}
		if s.filesExist(ctx, a) {
			return a, true
		}
	}
	return applied{}, false
}

func matchesRepo(globs []string, repo string) bool {
	if len(globs) == 0 {
		return true
	}
	return slices.ContainsFunc(globs, func(g string) bool { ok, _ := path.Match(g, repo); return ok })
}

// baseVars are the reserved placeholders of a repository.
func (s *KafkaService) baseVars(env domain.Env, repo string, spec KafkaRepoSpec) map[string]string {
	vars := map[string]string{"env": env.String(), "repo": repo}
	switch {
	case spec.Path != "":
		vars["repo_dir"] = s.abs(spec.Path)
	case s.ReposRoot != "":
		vars["repo_dir"] = filepath.Join(s.abs(s.ReposRoot), repo)
	}
	return vars
}

// vars returns the placeholders for a topic: the profile's variables,
// the repository's, the topic's, then the reserved ones (never
// overridden).
func (a applied) vars(topic map[string]string) map[string]string {
	out := map[string]string{}
	for _, vs := range []map[string]string{a.p.Vars, a.repo.Vars, topic, a.base} {
		maps.Copy(out, vs)
	}
	return out
}

func (s *KafkaService) filesExist(ctx context.Context, a applied) bool {
	vars := a.vars(nil)
	for _, pattern := range a.p.MatchFiles {
		p, err := s.path(pattern, vars, nil)
		if err != nil {
			s.debug("kafka profile does not apply", "profile", a.p.Name, "file", pattern, "err", err)
			return false
		}
		found, err := s.Files.Glob(ctx, p)
		if err != nil || len(found) == 0 {
			return false
		}
	}
	return true
}

// abs makes a path absolute: ~/ is the home folder, a relative path is
// relative to the config folder.
func (s *KafkaService) abs(p string) string {
	switch {
	case p == "~" || strings.HasPrefix(p, "~/"):
		return filepath.Join(s.Home, strings.TrimPrefix(p, "~"))
	case filepath.IsAbs(p):
		return filepath.Clean(p)
	}
	return filepath.Join(s.ConfigDir, filepath.FromSlash(p))
}

// path expands a path: placeholders, references to keys read so far, then
// made absolute.
func (s *KafkaService) path(p string, vars map[string]string, keys map[string]domain.Secret) (string, error) {
	v, err := s.value(p, vars, keys)
	if err != nil {
		return "", err
	}
	return s.abs(v.Reveal()), nil
}

// value resolves a profile string: placeholders first, then a whole
// env:VAR value or ${KEY} references to the sources.
func (s *KafkaService) value(raw string, vars map[string]string, keys map[string]domain.Secret) (domain.Secret, error) {
	v, err := domain.ExpandVars(raw, func(k string) (string, bool) { x, ok := vars[k]; return x, ok })
	if err != nil {
		return domain.Secret{}, err
	}
	if name, ok := strings.CutPrefix(v, "env:"); ok {
		if s.Getenv == nil || s.Getenv(name) == "" {
			return domain.Secret{}, fmt.Errorf("environment variable %s is not set", name)
		}
		return domain.NewSecret(s.Getenv(name)), nil
	}
	v, err = domain.ResolveKeys(v, func(k string) (string, bool) { x, ok := keys[k]; return x.Reveal(), ok })
	if err != nil {
		return domain.Secret{}, err
	}
	return domain.NewSecret(v), nil
}

// one returns the single file matching a path pattern.
func (s *KafkaService) one(ctx context.Context, pattern string) (string, error) {
	found, err := s.Files.Glob(ctx, pattern)
	switch {
	case err != nil:
		return "", err
	case len(found) == 0:
		return "", fmt.Errorf("%s: no file: %w", pattern, domain.ErrNotFound)
	case len(found) > 1:
		return "", fmt.Errorf("%s: %d files (%s), expected one: %w", pattern, len(found), strings.Join(found, ", "), domain.ErrConfig)
	}
	return found[0], nil
}

func (s *KafkaService) debug(msg string, args ...any) {
	if s.Log != nil {
		s.Log.Debug(msg, args...)
	}
}

// Open implements ports.Kafka.
func (s *KafkaService) Open(ctx context.Context, env domain.Env, repo string) (ports.KafkaSession, error) {
	a, ok := s.profileFor(ctx, env, repo)
	if !ok {
		return nil, fmt.Errorf("no Kafka profile applies to %s in %s: %w", repo, env, domain.ErrNotFound)
	}
	keys, err := s.readSources(ctx, a)
	if err != nil {
		return nil, fmt.Errorf("%s (kafka/%s): %w", repo, a.p.Name, err)
	}
	sess := &kafkaSession{s: s, profile: a.p.Name, sources: map[string]ports.TopicSource{}, conns: map[string]string{}}
	plans := s.topicPlans(a, keys)
	groups := map[string][]int{}
	conns := map[string]domain.KafkaConnection{}
	cas := map[string][][]byte{}
	for i, t := range plans {
		st := ports.KafkaTopicState{Name: t.name, Direction: t.dir, Err: t.err}
		if t.err == nil {
			conn, key, err := s.connection(ctx, a, t.vars, keys, cas)
			if err != nil {
				st.Err = err
			} else {
				groups[key] = append(groups[key], i)
				conns[key] = conn
				sess.conns[t.name] = key
			}
		}
		sess.topics = append(sess.topics, st)
	}
	for _, key := range slices.Sorted(maps.Keys(groups)) {
		s.connect(ctx, sess, key, conns[key], groups[key])
	}
	s.debug("kafka session opened", "repo", repo, "env", env, "profile", a.p.Name, "topics", len(sess.topics), "connections", len(groups))
	return sess, nil
}

// readSources reads the profile's sources, then the repository's, and
// merges them in order.
func (s *KafkaService) readSources(ctx context.Context, a applied) (map[string]domain.Secret, error) {
	keys := map[string]domain.Secret{}
	vars := a.vars(nil)
	for _, src := range slices.Concat(a.p.Sources, a.repo.Sources) {
		pattern, err := s.path(src.File, vars, keys)
		if err != nil {
			return nil, fmt.Errorf("source %s: %w", src.File, err)
		}
		file, err := s.one(ctx, pattern)
		if errors.Is(err, domain.ErrNotFound) && src.Optional {
			continue
		}
		if err != nil {
			return nil, err
		}
		values, err := s.Files.ReadEnv(ctx, file, src.Sops)
		if errors.Is(err, domain.ErrNotFound) && src.Optional {
			continue
		}
		if err != nil {
			return nil, err
		}
		maps.Copy(keys, values)
	}
	return keys, nil
}

// topicPlan is a topic to show, before its connection is resolved.
type topicPlan struct {
	name string
	dir  domain.TopicDirection
	vars map[string]string
	err  error
}

// topicPlans lists the topics in order (consume, produce, list,
// discovered), one entry per name, directions combined.
func (s *KafkaService) topicPlans(a applied, keys map[string]domain.Secret) []topicPlan {
	var out []topicPlan
	index := map[string]int{}
	add := func(t topicPlan) {
		if i, ok := index[t.name]; ok && t.err == nil {
			out[i].dir = out[i].dir.With(t.dir)
			return
		}
		index[t.name] = len(out)
		out = append(out, t)
	}
	listed := func(specs []KafkaTopicSpec, dir domain.TopicDirection) {
		for _, spec := range specs {
			vars := a.vars(spec.Vars)
			name, err := s.value(spec.Name, vars, keys)
			t := topicPlan{name: strings.TrimSpace(name.Reveal()), dir: dir, vars: vars, err: err}
			if err != nil {
				t.name = spec.Name // the reference as written says what is missing
			}
			add(t)
		}
	}
	for _, ts := range []KafkaTopicsSpec{a.p.Topics, a.repo.Topics} {
		listed(ts.Consume, domain.TopicConsume)
		listed(ts.Produce, domain.TopicProduce)
		listed(ts.List, domain.TopicNone)
	}
	globs := slices.Concat(a.p.Topics.Discover, a.repo.Topics.Discover)
	for _, k := range slices.Sorted(maps.Keys(keys)) {
		if !slices.ContainsFunc(globs, func(g string) bool { ok, _ := path.Match(g, k); return ok }) {
			continue
		}
		for _, name := range strings.Split(keys[k].Reveal(), ",") {
			if name = strings.TrimSpace(name); name != "" {
				add(topicPlan{name: name, dir: domain.TopicNone, vars: a.vars(nil)})
			}
		}
	}
	return out
}

// connection resolves the connection of a topic, and a key identifying
// it so topics sharing one share a client. Truststores are read once per
// file and password.
func (s *KafkaService) connection(ctx context.Context, a applied, vars map[string]string, keys map[string]domain.Secret, cas map[string][][]byte) (domain.KafkaConnection, string, error) {
	spec := a.p.Conn.over(a.repo.Conn)
	get := func(field, raw string) (string, error) {
		if raw == "" {
			return "", nil
		}
		v, err := s.value(raw, vars, keys)
		if err != nil {
			return "", fmt.Errorf("connection.%s: %w", field, err)
		}
		return v.Reveal(), nil
	}
	var c domain.KafkaConnection
	var key strings.Builder
	bootstrap, err := get("bootstrap", spec.Bootstrap)
	if err != nil {
		return c, "", err
	}
	for _, b := range strings.Split(bootstrap, ",") {
		if b = strings.TrimSpace(b); b != "" {
			c.Bootstrap = append(c.Bootstrap, b)
		}
	}
	if len(c.Bootstrap) == 0 {
		return c, "", fmt.Errorf("connection.bootstrap is empty: %w", domain.ErrConfig)
	}
	sec, err := get("security", spec.Security)
	if err != nil {
		return c, "", err
	}
	c.Security = domain.NormalizeKafkaSecurity(sec)
	if !slices.Contains(domain.KafkaSecurities, c.Security) {
		return c, "", fmt.Errorf("connection.security %s is not one of: %s: %w", shown(spec.Security, sec), strings.Join(domain.KafkaSecurities, ", "), domain.ErrConfig)
	}
	if c.SASL() {
		mech, err := get("sasl.mechanism", spec.Mechanism)
		if err != nil {
			return c, "", err
		}
		c.Mechanism = domain.NormalizeKafkaMechanism(mech)
		if !slices.Contains(domain.KafkaMechanisms, c.Mechanism) {
			return c, "", fmt.Errorf("connection.sasl.mechanism %s is not one of: %s: %w", shown(spec.Mechanism, mech), strings.Join(domain.KafkaMechanisms, ", "), domain.ErrConfig)
		}
		user, err := get("sasl.username", spec.Username)
		if err != nil {
			return c, "", err
		}
		pass, err := get("sasl.password", spec.Password)
		if err != nil {
			return c, "", err
		}
		if user == "" || pass == "" {
			return c, "", fmt.Errorf("%s needs sasl.username and sasl.password: %w", c.Security, domain.ErrConfig)
		}
		c.Username, c.Password = domain.NewSecret(user), domain.NewSecret(pass)
	}
	if c.TLS() && spec.CA != "" {
		pattern, err := s.path(spec.CA, vars, keys)
		if err != nil {
			return c, "", fmt.Errorf("connection.tls.ca: %w", err)
		}
		file, err := s.one(ctx, pattern)
		if err != nil {
			return c, "", fmt.Errorf("connection.tls.ca: %w", err)
		}
		pw, err := get("tls.ca_password", spec.CAPassword)
		if err != nil {
			return c, "", err
		}
		ck := file + "\x00" + pw
		if c.CACerts = cas[ck]; c.CACerts == nil {
			if c.CACerts, err = s.Files.ReadTrustStore(ctx, file, domain.NewSecret(pw)); err != nil {
				return c, "", err
			}
			cas[ck] = c.CACerts
		}
		key.WriteString(ck)
	}
	fmt.Fprintf(&key, "\x00%s\x00%s\x00%s\x00%s\x00%s", strings.Join(c.Bootstrap, ","), c.Security, c.Mechanism, c.Username.Reveal(), c.Password.Reveal())
	return c, key.String(), nil
}

// shown quotes a resolved value in a message only when the profile wrote
// it literally: a value read from a source or the environment may come
// from an encrypted file, so the message names where it came from.
func shown(raw, value string) string {
	if strings.Contains(raw, "${") || strings.HasPrefix(raw, "env:") {
		return "(from " + raw + ")"
	}
	return fmt.Sprintf("%q", value)
}

// connect opens one client for a group of topics and describes them.
func (s *KafkaService) connect(ctx context.Context, sess *kafkaSession, key string, conn domain.KafkaConnection, topics []int) {
	fail := func(err error) {
		for _, i := range topics {
			sess.topics[i].Err = err
			delete(sess.conns, sess.topics[i].Name)
		}
	}
	src, err := s.Sources.Open(ctx, conn)
	if err != nil {
		fail(err)
		return
	}
	names := make([]string, len(topics))
	for j, i := range topics {
		names[j] = sess.topics[i].Name
	}
	infos, err := src.Describe(ctx, names)
	if err != nil {
		src.Close()
		fail(err)
		return
	}
	sess.sources[key] = src
	for j, i := range topics {
		if j < len(infos) {
			sess.topics[i].Partitions, sess.topics[i].Err = infos[j].Partitions, infos[j].Err
		}
	}
}

// kafkaSession implements ports.KafkaSession.
type kafkaSession struct {
	s       *KafkaService
	profile string
	topics  []ports.KafkaTopicState
	sources map[string]ports.TopicSource // by connection key
	conns   map[string]string            // topic → connection key

	mu      sync.Mutex
	cancels []context.CancelFunc
	closed  bool
}

func (k *kafkaSession) Profile() string                 { return k.profile }
func (k *kafkaSession) Topics() []ports.KafkaTopicState { return slices.Clone(k.topics) }

// Close implements ports.KafkaSession.
func (k *kafkaSession) Close() {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.closed {
		return
	}
	k.closed = true
	for _, cancel := range k.cancels {
		cancel()
	}
	for _, src := range k.sources {
		src.Close()
	}
	clear(k.sources)
}

// Read implements ports.KafkaSession.
func (k *kafkaSession) Read(ctx context.Context, q ports.KafkaQuery) (<-chan ports.KafkaBatch, error) {
	i := slices.IndexFunc(k.topics, func(t ports.KafkaTopicState) bool { return t.Name == q.Topic })
	if i < 0 {
		return nil, fmt.Errorf("topic %s is not in this profile: %w", q.Topic, domain.ErrNotFound)
	}
	t := k.topics[i]
	if t.Err != nil {
		return nil, t.Err
	}
	k.mu.Lock()
	src, ok := k.sources[k.conns[q.Topic]]
	if k.closed || !ok {
		k.mu.Unlock()
		return nil, fmt.Errorf("topic %s: no connection: %w", q.Topic, domain.ErrUnreachable)
	}
	ctx, cancel := context.WithCancel(ctx)
	k.cancels = append(k.cancels, cancel)
	k.mu.Unlock()

	r := ports.TopicRead{Topic: q.Topic, Follow: q.Follow, ReadCommitted: q.ReadCommitted}
	parts := max(t.Partitions, 1)
	maxRecords := max(k.s.MaxRecords, 1)
	if q.Window.Since > 0 {
		r.Since = q.Window.Since
		r.Limit = max((maxRecords+parts-1)/parts, 1)
	} else {
		r.Tail = max(q.Window.Tail, 1)
		r.Limit = r.Tail
	}
	in, err := src.Read(ctx, r)
	if err != nil {
		cancel()
		return nil, err
	}
	out := make(chan ports.KafkaBatch, 16)
	go k.forward(ctx, cancel, in, out)
	return out, nil
}

// historyChunk is the size of the batches the sorted history is sent in.
const historyChunk = 2000

// forward truncates records, sorts the history by timestamp once it is
// complete (bounded like the view), then passes live batches on sorted.
func (k *kafkaSession) forward(ctx context.Context, cancel context.CancelFunc, in <-chan ports.RecordBatch, out chan<- ports.KafkaBatch) {
	defer close(out)
	defer cancel()
	defer func() {
		if r := recover(); r != nil { // never take the UI down
			k.s.debug("kafka read panicked", "panic", r)
			send(ctx, out, ports.KafkaBatch{Err: fmt.Errorf("internal error while reading: %v", r)})
		}
	}()
	var history []domain.KafkaRecord
	live := false
	for b := range in {
		for i := range b.Records {
			domain.TruncateRecord(&b.Records[i], k.s.MaxValueBytes)
		}
		if !live {
			history = append(history, b.Records...)
			if len(history) > 2*max(k.s.MaxRecords, 1) {
				history = k.trim(history)
			}
			if !b.HistoryDone && b.Err == nil {
				if len(b.Notices) > 0 && !send(ctx, out, ports.KafkaBatch{Notices: b.Notices}) {
					return
				}
				continue
			}
			live = true
			history = k.trim(history)
			for len(history) > historyChunk {
				if !send(ctx, out, ports.KafkaBatch{Records: history[:historyChunk:historyChunk]}) {
					return
				}
				history = history[historyChunk:]
			}
			if !send(ctx, out, ports.KafkaBatch{Records: history, Notices: b.Notices, HistoryDone: true, Err: b.Err}) {
				return
			}
			history = nil
			continue
		}
		sortRecords(b.Records)
		if !send(ctx, out, ports.KafkaBatch{Records: b.Records, Notices: b.Notices, Err: b.Err}) {
			return
		}
	}
}

// trim sorts records by timestamp and keeps the newest the view can hold.
func (k *kafkaSession) trim(recs []domain.KafkaRecord) []domain.KafkaRecord {
	sortRecords(recs)
	maxBytes := k.s.MaxBufferBytes
	if maxBytes <= 0 {
		maxBytes = int(^uint(0) >> 1)
	}
	start, bytes := len(recs), 0
	for start > 0 && len(recs)-start < max(k.s.MaxRecords, 1) {
		n := recs[start-1].Bytes()
		if bytes+n > maxBytes && start < len(recs) {
			break
		}
		bytes += n
		start--
	}
	clear(recs[:start]) // let the dropped records' bytes go
	return recs[start:]
}

// sortRecords orders records by timestamp, then partition and offset; a
// record without timestamp sorts first in its partition's offset order.
func sortRecords(recs []domain.KafkaRecord) {
	slices.SortStableFunc(recs, func(a, b domain.KafkaRecord) int {
		if c := a.Time.Compare(b.Time); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Partition, b.Partition); c != 0 {
			return c
		}
		return cmp.Compare(a.Offset, b.Offset)
	})
}

func send(ctx context.Context, out chan<- ports.KafkaBatch, b ports.KafkaBatch) bool {
	select {
	case out <- b:
		return true
	case <-ctx.Done():
		return false
	}
}
