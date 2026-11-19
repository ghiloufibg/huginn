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
	"testing"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
	"github.com/ghiloufibg/huginn/internal/core/ports/portstest"
)

const testToken = "glpat-secret-value"

func token(context.Context) (domain.Secret, error) { return domain.NewSecret(testToken), nil }

// server is a GitLab serving fx for project g/sub/p, checking the token
// and that only GET requests are sent.
func server(t *testing.T, fx portstest.ReleaseFixture) *httptest.Server {
	t.Helper()
	const prefix = "/api/v4/projects/g%2Fsub%2Fp"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("%s %s: only GET is allowed", r.Method, r.URL)
		}
		if r.Header.Get("PRIVATE-TOKEN") != testToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		path, ok := strings.CutPrefix(r.URL.EscapedPath(), prefix)
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		q := r.URL.Query()
		page, _ := strconv.Atoi(q.Get("page"))
		perPage, _ := strconv.Atoi(q.Get("per_page"))
		switch {
		case path == "/deployments":
			if q.Get("status") != "success" || q.Get("sort") != "desc" {
				t.Errorf("deployments query %v", q)
			}
			all := fx.Deployments[q.Get("environment")]
			from := (page - 1) * perPage
			var out []map[string]any
			for _, d := range all[min(from, len(all)):min(from+perPage, len(all))] {
				out = append(out, map[string]any{
					"id": d.ID, "sha": d.SHA, "ref": d.Ref, "status": "success", "finished_at": d.FinishedAt,
					"user": map[string]any{"username": d.User, "name": "Jane Doe"},
					"deployable": map[string]any{
						"id": d.Job.ID, "name": d.Job.Name, "pipeline": map[string]any{"id": d.Pipeline},
						"commit": map[string]any{"title": d.Subject, "author_name": d.Author, "created_at": d.CommittedAt},
					},
				})
			}
			_ = json.NewEncoder(w).Encode(out)
		case strings.HasPrefix(path, "/pipelines/"):
			id, _ := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(path, "/pipelines/"), "/jobs"), 10, 64)
			var out []map[string]any
			for _, j := range fx.Jobs[id] {
				out = append(out, map[string]any{"id": j.ID, "name": j.Name})
			}
			_ = json.NewEncoder(w).Encode(out)
		case strings.HasPrefix(path, "/jobs/"):
			id, _ := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(path, "/jobs/"), "/trace"), 10, 64)
			_, _ = w.Write([]byte(fx.Logs[id]))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestContract(t *testing.T) {
	portstest.RunReleaseHistoryContract(t, func(t *testing.T, fx portstest.ReleaseFixture) (ports.ReleaseHistory, ports.ReleaseProject) {
		srv := server(t, fx)
		return &Client{Token: token, BufSize: 1024}, ports.ReleaseProject{BaseURL: srv.URL, Path: "g/sub/p"}
	})
}

func TestLogFindAcrossReads(t *testing.T) {
	dg := "sha256:" + strings.Repeat("e", 64)
	for cut := 1; cut < len(dg); cut += 7 { // the digest split at every place
		log := strings.Repeat("x", 100-cut) + dg + "tail"
		srv := server(t, portstest.ReleaseFixture{Logs: map[int64]string{1: log}})
		c := &Client{Token: token, BufSize: 100}
		hits, err := c.LogFind(context.Background(), ports.ReleaseProject{BaseURL: srv.URL, Path: "g/sub/p"}, 1, []string{dg}, 1<<20)
		if err != nil || !slices.Equal(hits, []string{dg}) {
			t.Fatalf("cut %d: hits = %v, %v", cut, hits, err)
		}
	}
}

func TestErrors(t *testing.T) {
	p := func(url string) ports.ReleaseProject { return ports.ReleaseProject{BaseURL: url, Path: "g/sub/p"} }
	status := func(code int, header ...string) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if len(header) == 2 {
				w.Header().Set(header[0], header[1])
			}
			w.WriteHeader(code)
		}))
		t.Cleanup(srv.Close)
		return srv
	}
	tests := []struct {
		name string
		srv  *httptest.Server
		kind error
		msg  string
	}{
		{"401", status(401), domain.ErrUnauthorized, "refused the token"},
		{"403", status(403), domain.ErrForbidden, "Reporter"},
		{"404", status(404), domain.ErrNotFound, "no project g/sub/p"},
		{"429", status(429, "Retry-After", "30"), domain.ErrUnreachable, "retry after 30s"},
		{"500", status(500), domain.ErrUnreachable, "500"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := (&Client{Token: token}).Deployments(context.Background(), p(tt.srv.URL), "prd", 1, 5)
			if !errors.Is(err, tt.kind) || !strings.Contains(err.Error(), tt.msg) || strings.Contains(err.Error(), testToken) {
				t.Errorf("err = %v", err)
			}
		})
	}
	t.Run("unreachable", func(t *testing.T) {
		srv := status(200)
		srv.Close()
		_, err := (&Client{Token: token}).Deployments(context.Background(), p(srv.URL), "prd", 1, 5)
		if !errors.Is(err, domain.ErrUnreachable) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("no token", func(t *testing.T) {
		missing := domain.KindError(domain.ErrUnauthorized, "no GitLab token (.env GITLAB_TOKEN)")
		_, err := (&Client{Token: func(context.Context) (domain.Secret, error) { return domain.Secret{}, missing }}).Deployments(context.Background(), p("http://127.0.0.1:1"), "prd", 1, 5)
		if !errors.Is(err, missing) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestRedirectToAnotherHostIsRefused(t *testing.T) {
	leaked := false
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("PRIVATE-TOKEN") != ""
	}))
	t.Cleanup(other.Close)
	// 127.0.0.1 and localhost are different hosts for the redirect check.
	target := strings.Replace(other.URL, "127.0.0.1", "localhost", 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	_, err := (&Client{Token: token}).Deployments(context.Background(), ports.ReleaseProject{BaseURL: srv.URL, Path: "g/p"}, "prd", 1, 5)
	if !errors.Is(err, domain.ErrConfig) || leaked {
		t.Errorf("err = %v, token sent to the other host: %v", err, leaked)
	}
}
