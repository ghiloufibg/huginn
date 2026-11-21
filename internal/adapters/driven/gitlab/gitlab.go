// Package gitlab implements ports.ReleaseHistory with GitLab's REST API
// (v4), read only: it only sends GET requests, with a token that needs the
// read_api scope (docs/plan/M16-deployed-commit.md §4).
package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// Client reads deployments, pipeline jobs and job logs.
type Client struct {
	// HTTP sends the requests; nil means a client that honours the
	// environment's proxy settings and refuses redirects to another host.
	HTTP *http.Client
	// Token returns the access token. It is read on each request, so a
	// token added or fixed while Huginn runs is used at once.
	Token func(ctx context.Context) (domain.Secret, error)
	// BufSize is the read buffer of LogFind (tests); 0 means 32 KiB.
	BufSize int
}

const maxJobPages = 10 // a pipeline of more than 1 000 jobs is not read further

// Deployments implements ports.ReleaseHistory. The newest are the ones
// created last (order_by=id): ordering by finish time is refused by some
// GitLab versions without a time range.
func (c *Client) Deployments(ctx context.Context, p ports.ReleaseProject, env string, page, perPage int) ([]ports.Deployment, error) {
	q := url.Values{
		"environment": {env}, "status": {"success"}, "order_by": {"id"}, "sort": {"desc"},
		"per_page": {strconv.Itoa(perPage)}, "page": {strconv.Itoa(page)},
	}
	var raw []deployment
	if _, err := c.getJSON(ctx, p, "/deployments", q, &raw); err != nil {
		return nil, err
	}
	out := make([]ports.Deployment, 0, len(raw))
	for _, d := range raw {
		out = append(out, d.toPort())
	}
	return out, nil
}

// PipelineJobs implements ports.ReleaseHistory.
func (c *Client) PipelineJobs(ctx context.Context, p ports.ReleaseProject, pipeline int64) ([]ports.Job, error) {
	var out []ports.Job
	for page := 1; page <= maxJobPages; page++ {
		q := url.Values{"per_page": {"100"}, "page": {strconv.Itoa(page)}}
		var raw []job
		next, err := c.getJSON(ctx, p, "/pipelines/"+strconv.FormatInt(pipeline, 10)+"/jobs", q, &raw)
		if err != nil {
			return nil, err
		}
		for _, j := range raw {
			out = append(out, ports.Job{ID: j.ID, Name: j.Name})
		}
		if next == "" {
			break
		}
	}
	return out, nil
}

// LogFind implements ports.ReleaseHistory. The log is streamed and never
// kept: each read is searched together with the end of the previous one,
// so a needle split across two reads is still found.
func (c *Client) LogFind(ctx context.Context, p ports.ReleaseProject, jobID int64, needles []string, limit int64) ([]string, error) {
	if len(needles) == 0 {
		return nil, nil
	}
	resp, err := c.get(ctx, p, "/jobs/"+strconv.FormatInt(jobID, 10)+"/trace", nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	longest := 0
	for _, n := range needles {
		longest = max(longest, len(n))
	}
	size := c.BufSize
	if size <= 0 {
		size = 32 << 10
	}
	buf := make([]byte, 0, longest+size)
	found := make([]bool, len(needles))
	left, read := len(needles), int64(0)
	for left > 0 && read < limit {
		n, rerr := io.ReadFull(resp.Body, buf[len(buf):len(buf)+int(min(int64(size), limit-read))])
		read += int64(n)
		buf = buf[:len(buf)+n]
		for i, needle := range needles {
			if !found[i] && bytes.Contains(buf, []byte(needle)) {
				found[i] = true
				left--
			}
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) || errors.Is(rerr, io.ErrUnexpectedEOF) {
				break
			}
			return nil, classify(fmt.Errorf("read the log of job %d: %w", jobID, rerr))
		}
		keep := min(len(buf), longest-1) // what a split needle may have left
		copy(buf, buf[len(buf)-keep:])
		buf = buf[:keep]
	}
	var out []string
	for i, f := range found {
		if f {
			out = append(out, needles[i])
		}
	}
	return out, nil
}

func (c *Client) getJSON(ctx context.Context, p ports.ReleaseProject, path string, q url.Values, v any) (next string, err error) {
	h, err := c.getJSONHeader(ctx, p, path, q, v)
	if err != nil {
		return "", err
	}
	return h.Get("X-Next-Page"), nil
}

// getJSONHeader decodes the answer into v and returns its headers.
func (c *Client) getJSONHeader(ctx context.Context, p ports.ReleaseProject, path string, q url.Values, v any) (http.Header, error) {
	resp, err := c.get(ctx, p, path, q)
	if err != nil {
		return nil, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10)) // read to the end: the connection is reused
		_ = resp.Body.Close()
	}()
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(v); err != nil {
		return nil, fmt.Errorf("read GitLab's answer to %s: %w", path, err)
	}
	return resp.Header, nil
}

// get sends one GET request to the project's API and classifies a failure
// into a domain error kind. The token never appears in an error.
func (c *Client) get(ctx context.Context, p ports.ReleaseProject, path string, q url.Values) (*http.Response, error) {
	if c.Token == nil {
		return nil, domain.KindError(domain.ErrUnauthorized, "no GitLab token")
	}
	tok, err := c.Token(ctx)
	if err != nil {
		return nil, err
	}
	u := strings.TrimRight(p.BaseURL, "/") + "/api/v4/projects/" + url.PathEscape(p.Path) + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("GitLab URL %s: %w", p.BaseURL, domain.ErrConfig)
	}
	req.Header.Set("PRIVATE-TOKEN", tok.Reveal())
	req.Header.Set("Accept", "application/json")
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, classify(err)
	}
	if resp.StatusCode == http.StatusOK {
		return resp, nil
	}
	_ = resp.Body.Close()
	return nil, statusError(resp, p.Path)
}

// statusErr is an HTTP failure: its domain kind (for errors.Is) and the
// status code, which Commits reads to choose a fallback.
type statusErr struct {
	code int
	err  error
}

func (e *statusErr) Error() string { return e.err.Error() }
func (e *statusErr) Unwrap() error { return e.err }

func statusError(resp *http.Response, project string) error {
	return &statusErr{code: resp.StatusCode, err: kindOf(resp, project)}
}

func kindOf(resp *http.Response, project string) error {
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return domain.KindError(domain.ErrUnauthorized, "GitLab refused the token (expired or revoked?)")
	case http.StatusForbidden:
		return domain.KindError(domain.ErrForbidden, "the token cannot read "+project+" (read_api scope and Reporter role needed)")
	case http.StatusNotFound:
		return domain.KindError(domain.ErrNotFound, "GitLab has no project "+project+" visible to the token")
	case http.StatusTooManyRequests:
		msg := "GitLab rate limit"
		if after := resp.Header.Get("Retry-After"); after != "" {
			msg += ", retry after " + after + "s"
		}
		return domain.KindError(domain.ErrUnreachable, msg)
	}
	return domain.KindError(domain.ErrUnreachable, fmt.Sprintf("GitLab answered %s", resp.Status))
}

func classify(err error) error {
	var ne net.Error
	var ue *url.Error
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.As(err, &ue) && ue.Err != nil && strings.Contains(ue.Err.Error(), "redirect"):
		return domain.KindError(domain.ErrConfig, "GitLab redirected to another host; set url in releases.yaml")
	case errors.As(err, &ne):
		return domain.KindError(domain.ErrUnreachable, "GitLab unreachable")
	}
	return domain.KindError(domain.ErrUnreachable, "GitLab unreachable: "+err.Error())
}

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 2 * time.Minute, CheckRedirect: sameHost}
}

// sameHost refuses a redirect to another host, which would receive the
// token header.
func sameHost(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return errors.New("too many redirects")
	}
	if req.URL.Host != via[0].URL.Host {
		return errors.New("refused redirect to another host")
	}
	return nil
}

type user struct {
	Username string `json:"username"`
	Name     string `json:"name"`
}

type commit struct {
	Title      string    `json:"title"`
	AuthorName string    `json:"author_name"`
	CreatedAt  time.Time `json:"created_at"`
}

type pipeline struct {
	ID int64 `json:"id"`
}

type job struct {
	ID       int64     `json:"id"`
	Name     string    `json:"name"`
	Commit   *commit   `json:"commit"`
	Pipeline *pipeline `json:"pipeline"`
}

type deployment struct {
	ID         int64      `json:"id"`
	SHA        string     `json:"sha"`
	Ref        string     `json:"ref"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	FinishedAt *time.Time `json:"finished_at"`
	User       *user      `json:"user"`
	Deployable *job       `json:"deployable"`
}

func (d deployment) toPort() ports.Deployment {
	out := ports.Deployment{ID: d.ID, SHA: d.SHA, Ref: d.Ref, FinishedAt: d.UpdatedAt}
	if d.FinishedAt != nil {
		out.FinishedAt = *d.FinishedAt
	}
	if d.User != nil {
		out.User = d.User.Username
	}
	if j := d.Deployable; j != nil {
		out.Job = ports.Job{ID: j.ID, Name: j.Name}
		if j.Pipeline != nil {
			out.Pipeline = j.Pipeline.ID
		}
		if j.Commit != nil {
			out.Subject, out.Author, out.CommittedAt = j.Commit.Title, j.Commit.AuthorName, j.Commit.CreatedAt
		}
	}
	return out
}
