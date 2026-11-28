package tui

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// The COMMIT section of the services preview: which commit runs, proven
// by the release history (docs/plan/M16-deployed-commit.md). It is loaded
// in the background once the cursor rests on a service, like the pod
// events, and kept a minute.
const (
	releasesTTL     = time.Minute
	releasesTimeout = 2 * time.Minute
)

// relKey identifies the images of a service a lookup was made for.
type relKey struct {
	env     domain.Env
	repo    string
	digests string
}

type relEntry struct {
	at      time.Time
	res     map[string]domain.RunningRelease
	err     error
	loading bool
}

// releasesRestMsg fires once the cursor rested on a service.
type releasesRestMsg struct {
	key relKey
	seq int
}

// releasesMsg carries the releases found for a service's images.
type releasesMsg struct {
	key relKey
	res map[string]domain.RunningRelease
	err error
}

// releases is the preview's state of the COMMIT section.
type releases struct {
	want    relKey
	images  []domain.WorkloadImage // of want
	seq     int
	pending bool // a rest is scheduled for target
	cache   map[relKey]relEntry
	memo    relMemo
	drawn   drawnLines
}

// drawnLines keeps the COMMIT lines last drawn: they change only with the
// snapshot, the answer or the theme, while the preview is drawn on every
// frame.
type drawnLines struct {
	snap    *ports.CatalogSnapshot
	key     relKey
	at      time.Time
	loading bool
	theme   string
	env     string
	lines   []string
}

// relMemo keeps the images of the selected service for the snapshot they
// were read from: they change only with the cluster, while the preview is
// drawn on every frame (2 000 pods take about 0.4 ms per workload).
type relMemo struct {
	snap      *ports.CatalogSnapshot
	env       string
	repo      string
	key       relKey
	imgs      []domain.WorkloadImage
	cur, prev []domain.WorkloadImage
}

// target returns the lookup the selected service needs, its images, and
// the current and previous image of each workload, in workload order.
// They are computed once per snapshot.
func (r *releases) target(m *Model, svc *domain.ServiceSummary) (key relKey, imgs, cur, prev []domain.WorkloadImage) {
	if svc == nil || svc.Unassigned || m.opts.Releases == nil || m.env.NoCommit {
		return relKey{}, nil, nil, nil
	}
	mm := &r.memo
	if mm.snap != nil && mm.snap == m.snap && mm.env == m.env.Name && mm.repo == svc.Repo {
		return mm.key, mm.imgs, mm.cur, mm.prev
	}
	for _, w := range svc.WorkloadStates {
		c, p := domain.RunningImages(w, svc.Pods, m.opts.Filter)
		cur, prev = append(cur, c), append(prev, p)
	}
	key, imgs = releaseKey(m, svc.Repo, cur, prev)
	*mm = relMemo{snap: m.snap, env: m.env.Name, repo: svc.Repo, key: key, imgs: imgs, cur: cur, prev: prev}
	return key, imgs, cur, prev
}

// releaseKey returns the lookup of a service's images: each digest once.
func releaseKey(m *Model, repo string, cur, prev []domain.WorkloadImage) (relKey, []domain.WorkloadImage) {
	var imgs []domain.WorkloadImage
	var digests []string
	for _, img := range append(cur, prev...) {
		if img.Digest != "" && !slices.Contains(digests, img.Digest) {
			imgs, digests = append(imgs, img), append(digests, img.Digest)
		}
	}
	if len(imgs) == 0 {
		return relKey{}, nil
	}
	slices.Sort(digests)
	return relKey{domain.Env(m.env.Name), repo, strings.Join(digests, ",")}, imgs
}

// watch schedules a lookup once the cursor rests on a service whose images
// were not looked up within the TTL (never while scrolling).
func (r *releases) watch(m *Model, svc *domain.ServiceSummary) tea.Cmd {
	key, imgs, _, _ := r.target(m, svc)
	if key != r.want {
		r.want, r.images, r.pending = key, imgs, false
		r.seq++
	}
	if key.repo == "" || r.pending || r.fresh(m, key) {
		return nil
	}
	r.pending = true
	msg := releasesRestMsg{key, r.seq}
	return tea.Tick(eventsRest, func(time.Time) tea.Msg { return msg })
}

func (r *releases) fresh(m *Model, key relKey) bool {
	e, ok := r.cache[key]
	return ok && (e.loading || m.opts.Now().Sub(e.at) < releasesTTL)
}

// rest looks up the releases of the service the cursor rested on.
func (r *releases) rest(m *Model, msg releasesRestMsg) tea.Cmd {
	if msg.seq != r.seq || msg.key != r.want {
		return nil
	}
	r.pending = false
	if r.fresh(m, msg.key) {
		return nil
	}
	if r.cache == nil {
		r.cache = map[relKey]relEntry{}
	}
	e := r.cache[msg.key]
	e.loading = true
	r.cache[msg.key] = e
	src, parent := m.opts.Releases, m.opts.Context
	q := ports.ReleaseQuery{Env: msg.key.env, Repo: msg.key.repo, Images: slices.Clone(r.images)}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, releasesTimeout)
		defer cancel()
		res, err := src.Running(ctx, q)
		return releasesMsg{key: msg.key, res: res, err: err}
	}
}

func (r *releases) store(m *Model, msg releasesMsg) {
	if r.cache == nil {
		r.cache = map[relKey]relEntry{}
	}
	now := m.opts.Now()
	for k, e := range r.cache { // a long session visits many services: keep the fresh ones
		if !e.loading && now.Sub(e.at) >= 10*releasesTTL {
			delete(r.cache, k)
		}
	}
	r.cache[msg.key] = relEntry{at: now, res: msg.res, err: msg.err}
}

// lines returns the COMMIT lines of svc: one when every workload runs the
// same commit, else one per workload, prefixed with its name.
func (r *releases) lines(m *Model, svc domain.ServiceSummary) []string {
	key, _, cur, prev := r.target(m, &svc)
	if key.repo == "" {
		return nil
	}
	e := r.cache[key]
	d := &r.drawn
	if d.lines != nil && d.snap == m.snap && d.key == key && d.at.Equal(e.at) && d.loading == e.loading &&
		d.theme == m.opts.Theme.Name && d.env == m.env.Name {
		return d.lines
	}
	lines := r.draw(m, svc, key, cur, prev)
	*d = drawnLines{snap: m.snap, key: key, at: e.at, loading: e.loading, theme: m.opts.Theme.Name, env: m.env.Name, lines: lines}
	return lines
}

// draw builds the COMMIT lines of svc from the last answer.
func (r *releases) draw(m *Model, svc domain.ServiceSummary, key relKey, cur, prev []domain.WorkloadImage) []string {
	t := &m.opts.Theme
	e, ok := r.cache[key]
	if !ok || (e.loading && e.res == nil) {
		return []string{t.Dim.Render("searching GitLab releases…")}
	}
	if e.err != nil {
		return []string{t.Dim.Render(safeText(e.err.Error()))}
	}
	var names, texts []string
	width := 0
	for i, w := range svc.WorkloadStates {
		if cur[i] == (domain.WorkloadImage{}) {
			continue // no pod (a CronJob between runs)
		}
		names = append(names, w.Ref.Name)
		texts = append(texts, commitText(t, m.env.Name, cur[i], prev[i], e.res))
		width = max(width, ansi.StringWidth(w.Ref.Name))
	}
	if len(texts) == 0 {
		return nil
	}
	if same := slices.IndexFunc(texts, func(s string) bool { return s != texts[0] }) < 0; same {
		return texts[:1]
	}
	out := make([]string, len(texts))
	for i := range texts {
		out[i] = padRight(names[i], width) + "  " + texts[i]
	}
	return out
}

// commitText is what the COMMIT line says about one workload's images.
func commitText(t *Theme, env string, cur, prev domain.WorkloadImage, res map[string]domain.RunningRelease) string {
	if cur.Digest == "" {
		return t.Dim.Render("waiting for the image")
	}
	rel, ok := res[cur.Digest]
	if !ok {
		return t.Dim.Render("searching GitLab releases…")
	}
	var parts []string
	if rel.NotRecent {
		parts = append(parts, t.Warn.Render("not a recent "+env+" release"))
	}
	if rel.Release.SHA == "" {
		if rel.Problem != nil {
			parts = append(parts, t.Dim.Render(safeText(rel.Problem.Error())))
		}
		if rel.Problem == nil || rel.NotRecent {
			parts = append(parts, t.Dim.Render("commit unknown (tag "+safeText(cur.Tag)+")"))
		}
		return strings.Join(parts, t.Dim.Render("  · "))
	}
	sha := rel.Short()
	if p, ok := res[prev.Digest]; ok && prev.Digest != "" && p.Release.SHA != "" && p.Release.SHA != rel.Release.SHA {
		sha = t.Warn.Render(p.Short() + "→" + sha)
	} else if rel.State == domain.ReleaseGuessed {
		sha = "≈ " + sha
	}
	head := t.Bold.Render(sha) + "  " + safeText(rel.Release.Subject)
	if len(parts) > 0 {
		head = parts[0] + t.Dim.Render("  · ") + head
	}
	var tail []string
	switch {
	case rel.State == domain.ReleaseGuessed:
		tail = append(tail, "from the "+rel.Guess)
	case rel.Release.Pipeline > 0 && rel.Release.DeployedBy != "":
		tail = append(tail, fmt.Sprintf("%s, pipeline #%d", safeText(rel.Release.DeployedBy), rel.Release.Pipeline))
	case rel.Release.Pipeline > 0:
		tail = append(tail, fmt.Sprintf("pipeline #%d", rel.Release.Pipeline))
	}
	if c := countText(t, rel.Count); c != "" {
		tail = append(tail, c)
	}
	if rel.Problem != nil {
		tail = append(tail, t.Dim.Render(safeText(rel.Problem.Error())))
	}
	for _, s := range tail {
		head += t.Dim.Render("  · ") + s
	}
	return head
}

// countText compares the running commit with the trunk. A capped count is
// a lower bound: "100+".
func countText(t *Theme, c domain.TrunkCount) string {
	if !c.Known {
		return ""
	}
	n := strconv.Itoa(c.Behind)
	if c.Capped {
		n += "+"
	}
	switch {
	case !c.OnTrunk:
		return t.Warn.Render("not on " + c.Ref)
	case c.Behind == 0:
		return t.Dim.Render("up to date with " + c.Ref)
	case c.WholeProject:
		return n + " commits on " + c.Ref + " since"
	}
	return n + " not deployed on " + c.Ref
}
