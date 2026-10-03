package tui

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// The Kafka screens (docs/plan/M5-kafka.md): the topics of a repository,
// the records of a topic, one record. Read only: nothing here can write
// to Kafka.

type (
	// kafkaReposMsg answers which repositories have a Kafka screen.
	kafkaReposMsg struct {
		env   string
		repos map[string]bool
	}
	// kafkaOpenedMsg delivers the session of a topics screen.
	kafkaOpenedMsg struct {
		screen  *kafkaTopicsScreen
		gen     int
		session ports.KafkaSession
		err     error
	}
)

// askKafka asks, once per change of environment or repositories, which
// repositories have a Kafka screen. It only checks files for existence.
func (m *Model) askKafka() tea.Cmd {
	if m.opts.Kafka == nil || m.snap == nil {
		return nil
	}
	var repos []string
	for _, s := range m.snap.Services {
		if !s.Unassigned {
			repos = append(repos, s.Repo)
		}
	}
	slices.Sort(repos)
	key := m.env.Name + "\x00" + strings.Join(repos, "\x00")
	if key == m.kafkaAsked {
		return nil
	}
	m.kafkaAsked = key
	env, k, ctx := m.env.Name, m.opts.Kafka, m.opts.Context
	return func() tea.Msg {
		return kafkaReposMsg{env: env, repos: k.Repos(ctx, domain.Env(env), repos)}
	}
}

// kafkaTopicsScreen lists the topics of a repository.
type kafkaTopicsScreen struct {
	repo    string
	gen     int
	cancel  context.CancelFunc
	session ports.KafkaSession
	err     error
	loading bool
	rows    []ports.KafkaTopicState
	titles  map[int]string
	tbl     table
	height  int
}

func newKafkaTopicsScreen(repo string) *kafkaTopicsScreen {
	return &kafkaTopicsScreen{repo: repo, tbl: table{cols: []column{
		{title: "TOPIC", width: 16, flex: true},
		{title: "DIR", width: 6},
		{title: "PARTS", width: 5, right: true},
		{title: "STATE", width: 20, fill: true},
	}}}
}

func (k *kafkaTopicsScreen) crumbs() []string { return []string{"services", k.repo, "kafka"} }

func (k *kafkaTopicsScreen) init(m *Model) tea.Cmd { return k.open(m) }

// open resolves the profile and connects, off the UI goroutine.
func (k *kafkaTopicsScreen) open(m *Model) tea.Cmd {
	k.close()
	k.gen++
	k.err, k.loading, k.rows, k.titles = nil, true, nil, nil
	if m.opts.Kafka == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(m.opts.Context)
	k.cancel = cancel
	gen, kafka, env := k.gen, m.opts.Kafka, domain.Env(m.env.Name)
	return func() tea.Msg {
		s, err := kafka.Open(ctx, env, k.repo)
		return kafkaOpenedMsg{screen: k, gen: gen, session: s, err: err}
	}
}

func (k *kafkaTopicsScreen) close() {
	if k.cancel != nil {
		k.cancel()
		k.cancel = nil
	}
	if k.session != nil {
		k.session.Close()
		k.session = nil
	}
}

// busy: the profile is being resolved and the brokers contacted.
func (k *kafkaTopicsScreen) busy(*Model) bool { return k.loading }

func (k *kafkaTopicsScreen) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	switch msg := msg.(type) {
	case kafkaOpenedMsg:
		if msg.screen != k {
			return false, nil
		}
		if msg.gen != k.gen || k.cancel == nil { // replaced or closed meanwhile
			if msg.session != nil {
				msg.session.Close()
			}
			return true, nil
		}
		k.loading, k.err, k.session = false, msg.err, msg.session
		if msg.session != nil {
			k.rows, k.titles = groupTopics(msg.session.Topics())
		}
		return true, nil
	case tea.KeyPressMsg:
		return k.key(m, msg)
	}
	return false, nil
}

// groupTopics orders topics by direction (consumed, produced, others) and
// returns the group titles by first row.
func groupTopics(ts []ports.KafkaTopicState) ([]ports.KafkaTopicState, map[int]string) {
	group := func(d domain.TopicDirection) int {
		switch d {
		case domain.TopicConsume, domain.TopicBoth:
			return 0
		case domain.TopicProduce:
			return 1
		}
		return 2
	}
	rows := slices.Clone(ts)
	slices.SortStableFunc(rows, func(a, b ports.KafkaTopicState) int { return group(a.Direction) - group(b.Direction) })
	names := [...]string{"CONSUMES", "PRODUCES", "TOPICS"}
	titles := map[int]string{}
	for i, r := range rows {
		if i == 0 || group(rows[i-1].Direction) != group(r.Direction) {
			titles[i] = names[group(r.Direction)]
		}
	}
	return rows, titles
}

func (k *kafkaTopicsScreen) key(m *Model, msg tea.KeyPressMsg) (bool, tea.Cmd) {
	keys, key := m.opts.Keys, msg.String()
	page := max(k.height-2, 1)
	move := func(d int) { k.tbl.cursor = max(min(k.tbl.cursor+d, len(k.rows)-1), 0) }
	switch {
	case keys.Is(key, ActDown):
		move(1)
	case keys.Is(key, ActUp):
		move(-1)
	case keys.Is(key, ActPageDown):
		move(page)
	case keys.Is(key, ActPageUp):
		move(-page)
	case keys.Is(key, ActTop):
		move(-len(k.rows))
	case keys.Is(key, ActBottom):
		move(len(k.rows))
	case keys.Is(key, ActRefresh):
		return true, k.open(m)
	case keys.Is(key, ActOpen):
		if t, ok := k.current(); ok && t.Err == nil && k.session != nil {
			return true, m.push(newKafkaRecordsScreen(m, k, t))
		}
		if t, ok := k.current(); ok && t.Err != nil {
			m.flash(t.Name + ": " + errKind(t.Err))
		}
	default:
		return false, nil
	}
	return true, nil
}

func (k *kafkaTopicsScreen) current() (ports.KafkaTopicState, bool) {
	if len(k.rows) == 0 {
		return ports.KafkaTopicState{}, false
	}
	return k.rows[max(min(k.tbl.cursor, len(k.rows)-1), 0)], true
}

func (k *kafkaTopicsScreen) view(m *Model, w, h int) string {
	k.height = h
	t := m.opts.Theme
	switch {
	case k.loading:
		return centered(t.Key.Render(m.spinner())+t.Dim.Render(" reading the Kafka settings of "+k.repo), w, h)
	case k.err != nil:
		return centered(t.Bad.Render("Cannot open the Kafka topics of "+k.repo+": "+errKind(k.err))+"\n\n"+
			t.Dim.Render(wrapErr(k.err, w))+"\n\n"+t.Dim.Render("press ")+t.Key.Render(m.label(ActRefresh))+t.Dim.Render(" to retry"), w, h)
	case len(k.rows) == 0:
		return centered(t.Dim.Render("no topic for "+k.repo), w, h)
	}
	flex := 0
	for _, r := range k.rows {
		flex = max(flex, len(r.Name))
	}
	k.tbl.titles = k.titles
	return k.tbl.render(len(k.rows), func(i int) []cell { return k.cells(t, k.rows[i]) }, flex, w, h, t)
}

func (k *kafkaTopicsScreen) cells(t Theme, r ports.KafkaTopicState) []cell {
	dir := map[domain.TopicDirection]string{domain.TopicConsume: "in", domain.TopicProduce: "out", domain.TopicBoth: "in+out"}[r.Direction]
	parts := ""
	if r.Partitions > 0 {
		parts = strconv.Itoa(r.Partitions)
	}
	state := cell{text: "ready", style: t.Dim}
	if r.Err != nil {
		state = cell{text: r.Err.Error(), style: t.Bad}
	}
	return []cell{{text: r.Name, style: t.Bold}, {text: dir}, {text: parts}, state}
}

func (k *kafkaTopicsScreen) statusLeft(m *Model) string {
	t := m.opts.Theme
	bar := t.Status
	if m.env.Production {
		bar = t.StatusProd
	}
	left := t.Chip.Render("KAFKA") + bar.Render("  ")
	if k.session == nil {
		return left + bar.Render("read only")
	}
	bad := 0
	for _, r := range k.rows {
		if r.Err != nil {
			bad++
		}
	}
	parts := []string{"read only", "profile " + k.session.Profile(), fmt.Sprintf("%d topics", len(k.rows))}
	if bad > 0 {
		parts = append(parts, fmt.Sprintf("%d unreadable", bad))
	}
	return left + bar.Render(strings.Join(parts, "  ·  "))
}

func (k *kafkaTopicsScreen) hints(m *Model) []hint {
	return []hint{m.h(ActOpen, "records"), m.h(ActRefresh, "reconnect"), m.h(ActBack, "back"), m.h(ActHelp, "help")}
}

func (k *kafkaTopicsScreen) prompt(*Model) string { return "" }
