package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
	"github.com/ghiloufibg/huginn/internal/core/ports/portstest"
)

// history is a project's history as GitLab serves it: commits oldest first
// on each branch, the folders each commit touches, and tags.
type history struct {
	branches map[string][]string // branch → commit ids, oldest first
	touches  map[string][]string // commit id → folders
	tags     map[string]string
	parents  map[string]string   // for commits off every branch: the parent on main
	merges   map[string][]string // merge commit id → its parent ids

	mu    sync.Mutex
	paths []string // requests made
}

func (h *history) all() []string {
	var ids []string
	for _, b := range h.branches {
		ids = append(ids, b...)
	}
	for id := range h.parents {
		ids = append(ids, id)
	}
	return ids
}

// reachable returns the commits reachable from ref (a branch or a commit).
func (h *history) reachable(ref string) []string {
	if b, ok := h.branches[ref]; ok {
		return slices.Clone(b)
	}
	for _, b := range h.branches {
		if i := slices.Index(b, ref); i >= 0 {
			return slices.Clone(b[:i+1]) // never alias the fixture
		}
	}
	if parent, ok := h.parents[ref]; ok {
		return append(h.reachable(parent), ref)
	}
	return nil
}

func (h *history) server(t *testing.T) *httptest.Server {
	t.Helper()
	const prefix = "/api/v4/projects/g%2Fp"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("PRIVATE-TOKEN") != testToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		path, ok := strings.CutPrefix(r.URL.EscapedPath(), prefix)
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		h.mu.Lock()
		h.paths = append(h.paths, path+"?"+r.URL.RawQuery)
		h.mu.Unlock()
		q := r.URL.Query()
		write := func(v any) { _ = json.NewEncoder(w).Encode(v) }
		commit := func(id string) map[string]any {
			return map[string]any{"id": id, "title": "subject of " + id[:7], "author_name": "Jane", "committed_date": time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC), "parent_ids": h.merges[id]}
		}
		switch {
		case path == "":
			write(map[string]any{"default_branch": "main"})
		case strings.HasPrefix(path, "/repository/commits/"):
			id := strings.TrimPrefix(path, "/repository/commits/")
			var match []string
			for _, c := range h.all() {
				if strings.HasPrefix(c, id) && !slices.Contains(match, c) {
					match = append(match, c)
				}
			}
			if len(match) != 1 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			write(commit(match[0]))
		case strings.HasPrefix(path, "/repository/tags/"):
			id, ok := h.tags[strings.TrimPrefix(path, "/repository/tags/")]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			write(map[string]any{"commit": map[string]any{"id": id}})
		case strings.HasPrefix(path, "/repository/branches/"):
			name := strings.ReplaceAll(strings.TrimPrefix(path, "/repository/branches/"), "%2F", "/")
			b, ok := h.branches[name]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			write(map[string]any{"commit": map[string]any{"id": b[len(b)-1]}})
		case path == "/repository/merge_base":
			refs := q["refs[]"]
			a, b := h.reachable(refs[0]), h.reachable(refs[1])
			base := ""
			for _, c := range a {
				if slices.Contains(b, c) {
					base = c
				}
			}
			write(map[string]any{"id": base})
		case path == "/repository/commits":
			from, to, isRange := strings.Cut(q.Get("ref_name"), "..")
			if !isRange {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			var ids []string
			have := h.reachable(from)
			for _, c := range h.reachable(to) {
				if slices.Contains(have, c) {
					continue
				}
				if p := q.Get("path"); p != "" && !slices.ContainsFunc(h.touches[c], func(f string) bool { return strings.HasPrefix(f, p) }) {
					continue
				}
				ids = append(ids, c)
			}
			slices.Reverse(ids) // newest first, as GitLab lists a range
			// Offset pagination as GitLab documents it: X-Next-Page, and no
			// X-Total (left out of large lists, so never relied on).
			perPage, _ := strconv.Atoi(q.Get("per_page"))
			page, _ := strconv.Atoi(q.Get("page"))
			page = max(page, 1)
			from2 := min((page-1)*perPage, len(ids))
			if from2+perPage < len(ids) {
				w.Header().Set("X-Next-Page", strconv.Itoa(page+1))
			}
			ids = ids[from2:min(from2+perPage, len(ids))]
			var out []map[string]any
			for _, id := range ids {
				out = append(out, commit(id))
			}
			write(out)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func id(c string) string { return strings.Repeat(c, 40) }

func newHistory() *history {
	return &history{
		branches: map[string][]string{
			"main": {id("1"), id("2"), id("3")}, "release/2026": {id("1"), id("5")},
			// feature ends with "Merge main into feature"; mergeonly holds nothing else.
			"feature": {id("1"), id("6"), id("7")}, "mergeonly": {id("1"), id("9")},
		},
		merges:  map[string][]string{id("7"): {id("6"), id("3")}, id("9"): {id("1"), id("3")}},
		touches: map[string][]string{id("2"): {"svc/a.go"}, id("3"): {"other/b.go"}},
		tags:    map[string]string{"v1.0": id("1")},
		parents: map[string]string{id("4"): id("1")},
	}
}

func TestCommitsContract(t *testing.T) {
	portstest.RunCommitLogContract(t, func(t *testing.T) (ports.CommitLog, ports.ReleaseProject, portstest.HistoryFixture) {
		srv := newHistory().server(t)
		return &Commits{Client: &Client{Token: token}}, ports.ReleaseProject{BaseURL: srv.URL, Path: "g/p"},
			portstest.HistoryFixture{First: id("1"), Second: id("2"), Third: id("3"), Hotfix: id("4")}
	})
}

// bigRange adds a branch "big" of n commits after the first, each
// touching svc/.
func bigRange(h *history, n int) {
	var many []string
	for i := range n {
		many = append(many, strconv.Itoa(1000000+i)+strings.Repeat("a", 33))
	}
	h.branches["big"] = append([]string{id("1")}, many...)
	for _, m := range many {
		h.touches[m] = []string{"svc/x"}
	}
}

func TestCommitReadsItsParents(t *testing.T) {
	srv := newHistory().server(t)
	c := &Commits{Client: &Client{Token: token}}
	p := ports.ReleaseProject{BaseURL: srv.URL, Path: "g/p"}
	if got, err := c.Commit(context.Background(), p, id("7")); err != nil || !slices.Equal(got.Parents, []string{id("6"), id("3")}) {
		t.Errorf("merge parents = %v, %v", got.Parents, err)
	}
	if got, err := c.Commit(context.Background(), p, id("2")); err != nil || len(got.Parents) > 1 {
		t.Errorf("plain commit parents = %v, %v", got.Parents, err)
	}
}

func TestIntroducedSkipsMerges(t *testing.T) {
	srv := newHistory().server(t)
	c := &Commits{Client: &Client{Token: token}}
	p := ports.ReleaseProject{BaseURL: srv.URL, Path: "g/p"}
	// The branch tip is a merge: the newest real commit is the one before.
	got, err := c.Introduced(context.Background(), p, id("3"), id("7"))
	if err != nil || got.SHA != id("6") || got.Subject != "subject of "+id("6")[:7] || got.Author != "Jane" {
		t.Errorf("Introduced = %+v, %v; want %s", got, err, id("6")[:7])
	}
	// Nothing but merges: not found, the caller keeps the merge's own message.
	if _, err := c.Introduced(context.Background(), p, id("3"), id("9")); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("only merges: %v", err)
	}
	if _, err := c.Introduced(context.Background(), p, "../x", id("7")); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("bad id: %v", err)
	}
}

func TestCountPagesThroughTheRange(t *testing.T) {
	h := newHistory()
	bigRange(h, 250)
	srv := h.server(t)
	c := &Commits{Client: &Client{Token: token}}
	p := ports.ReleaseProject{BaseURL: srv.URL, Path: "g/p"}
	behind, capped, on, err := c.Count(context.Background(), p, id("1"), "big", nil)
	if err != nil || behind != 250 || capped || !on {
		t.Errorf("Count = %d, capped %v, on %v, %v; want 250 exactly over 3 pages", behind, capped, on, err)
	}
	if n := countRequests(h, "/repository/commits?"); n != 3 {
		t.Errorf("%d list requests, want 3", n)
	}
}

func TestCountStopsAtTheCap(t *testing.T) {
	h := newHistory()
	bigRange(h, pageSize*maxPages+5)
	srv := h.server(t)
	c := &Commits{Client: &Client{Token: token}}
	behind, capped, _, err := c.Count(context.Background(), ports.ReleaseProject{BaseURL: srv.URL, Path: "g/p"}, id("1"), "big", nil)
	if err != nil || !capped || behind != pageSize*maxPages {
		t.Errorf("Count = %d, capped %v, %v; want a lower bound of %d", behind, capped, err, pageSize*maxPages)
	}
	if n := countRequests(h, "/repository/commits?"); n != maxPages {
		t.Errorf("%d list requests, want %d", n, maxPages)
	}
}

func TestCountPathsUnion(t *testing.T) {
	h := newHistory()
	srv := h.server(t)
	c := &Commits{Client: &Client{Token: token}}
	behind, capped, _, err := c.Count(context.Background(), ports.ReleaseProject{BaseURL: srv.URL, Path: "g/p"}, id("1"), "main", []string{"svc/", "other/", "svc/"})
	if err != nil || behind != 2 || capped {
		t.Errorf("Count by paths = %d, %v, %v; want 2 (the union)", behind, capped, err)
	}
}

func countRequests(h *history, prefix string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, p := range h.paths {
		if strings.HasPrefix(p, prefix) {
			n++
		}
	}
	return n
}

func TestBranchWithSlashIsEncoded(t *testing.T) {
	h := newHistory()
	srv := h.server(t)
	c := &Commits{Client: &Client{Token: token}}
	tip, err := c.BranchTip(context.Background(), ports.ReleaseProject{BaseURL: srv.URL, Path: "g/p"}, "release/2026")
	if err != nil || tip != id("5") {
		t.Errorf("BranchTip = %q, %v", tip, err)
	}
	if !slices.ContainsFunc(h.paths, func(p string) bool { return strings.HasPrefix(p, "/repository/branches/release%2F2026") }) {
		t.Errorf("requests %v: the branch must be URL-encoded", h.paths)
	}
}

func TestCommitsRejectBadInput(t *testing.T) {
	c := &Commits{Client: &Client{Token: token}}
	p := ports.ReleaseProject{BaseURL: "http://127.0.0.1:1", Path: "g/p"}
	ctx := context.Background()
	if _, err := c.Commit(ctx, p, "../x"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Commit: %v", err)
	}
	if _, err := c.Tag(ctx, p, "-x"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Tag: %v", err)
	}
	if _, err := c.BranchTip(ctx, p, "a..b"); !errors.Is(err, domain.ErrConfig) {
		t.Errorf("BranchTip: %v", err)
	}
	if _, _, _, err := c.Count(ctx, p, id("1"), "main", []string{"../etc"}); !errors.Is(err, domain.ErrConfig) {
		t.Errorf("Count path: %v", err)
	}
}

func TestCommitsErrorsKeepTheirKind(t *testing.T) {
	srv := newHistory().server(t)
	c := &Commits{Client: &Client{Token: func(context.Context) (domain.Secret, error) { return domain.NewSecret("wrong"), nil }}}
	_, err := c.Commit(context.Background(), ports.ReleaseProject{BaseURL: srv.URL, Path: "g/p"}, id("1"))
	if !errors.Is(err, domain.ErrUnauthorized) || strings.Contains(err.Error(), "wrong") {
		t.Errorf("err = %v", err)
	}
}
