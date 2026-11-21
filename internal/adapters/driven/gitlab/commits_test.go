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
	parents  map[string]string // for commits off every branch: the parent on main
	// noTotal drops X-Total (GitLab does so for large lists); noRanges
	// answers 400 to a revision range in ref_name (an old GitLab).
	noTotal, noRanges bool

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
			return map[string]any{"id": id, "title": "subject of " + id[:7], "author_name": "Jane", "committed_date": time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)}
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
			if !isRange || h.noRanges {
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
			perPage, _ := strconv.Atoi(q.Get("per_page"))
			if !h.noTotal {
				w.Header().Set("X-Total", strconv.Itoa(len(ids)))
			}
			if len(ids) > perPage {
				w.Header().Set("X-Next-Page", "2")
				ids = ids[:perPage]
			}
			var out []map[string]any
			for _, id := range ids {
				out = append(out, map[string]any{"id": id})
			}
			write(out)
		case path == "/repository/compare":
			have := h.reachable(q.Get("from"))
			var out []map[string]any
			for _, c := range h.reachable(q.Get("to")) {
				if !slices.Contains(have, c) {
					out = append(out, map[string]any{"id": c})
				}
			}
			write(map[string]any{"commits": out, "compare_timeout": false, "diffs": []any{}})
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
		branches: map[string][]string{"main": {id("1"), id("2"), id("3")}, "release/2026": {id("1"), id("5")}},
		touches:  map[string][]string{id("2"): {"svc/a.go"}, id("3"): {"other/b.go"}},
		tags:     map[string]string{"v1.0": id("1")},
		parents:  map[string]string{id("4"): id("1")},
	}
}

func TestCommitsContract(t *testing.T) {
	portstest.RunCommitLogContract(t, func(t *testing.T) (ports.CommitLog, ports.ReleaseProject, portstest.HistoryFixture) {
		srv := newHistory().server(t)
		return &Commits{Client: &Client{Token: token}}, ports.ReleaseProject{BaseURL: srv.URL, Path: "g/p"},
			portstest.HistoryFixture{First: id("1"), Second: id("2"), Third: id("3"), Hotfix: id("4")}
	})
}

func TestCountWithoutTotalIsALowerBound(t *testing.T) {
	h := newHistory()
	h.noTotal = true
	srv := h.server(t)
	c := &Commits{Client: &Client{Token: token}}
	p := ports.ReleaseProject{BaseURL: srv.URL, Path: "g/p"}
	behind, capped, _, err := c.Count(context.Background(), p, id("1"), "main", nil)
	if err != nil || !capped || behind != lotsOfCommits {
		t.Errorf("Count = %d, capped %v, %v; want a lower bound", behind, capped, err)
	}
	behind, capped, _, err = c.Count(context.Background(), p, id("2"), "main", nil)
	if err != nil || capped || behind != 1 {
		t.Errorf("one commit without X-Total = %d, capped %v, %v", behind, capped, err)
	}
}

func TestCountFallsBackToCompare(t *testing.T) {
	h := newHistory()
	h.noRanges = true
	srv := h.server(t)
	c := &Commits{Client: &Client{Token: token}}
	behind, capped, on, err := c.Count(context.Background(), ports.ReleaseProject{BaseURL: srv.URL, Path: "g/p"}, id("1"), "main", nil)
	if err != nil || behind != 2 || capped || !on {
		t.Errorf("Count through compare = %d, %v, %v, %v", behind, capped, on, err)
	}
}

func TestCountPathsUnionAndCap(t *testing.T) {
	h := newHistory()
	srv := h.server(t)
	c := &Commits{Client: &Client{Token: token}}
	p := ports.ReleaseProject{BaseURL: srv.URL, Path: "g/p"}
	behind, capped, _, err := c.Count(context.Background(), p, id("1"), "main", []string{"svc/", "other/", "svc/"})
	if err != nil || behind != 2 || capped {
		t.Errorf("Count by paths = %d, %v, %v; want 2 (the union)", behind, capped, err)
	}
	// More than a page for a path: a lower bound.
	var many []string
	for i := range pathPage + 5 {
		many = append(many, strconv.Itoa(1000000+i)+strings.Repeat("a", 33))
	}
	h.branches["big"] = append([]string{id("1")}, many...)
	for _, m := range many {
		h.touches[m] = []string{"svc/x"}
	}
	behind, capped, _, err = c.Count(context.Background(), p, id("1"), "big", []string{"svc/"})
	if err != nil || behind != pathPage || !capped {
		t.Errorf("Count past a page = %d, %v, %v", behind, capped, err)
	}
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
