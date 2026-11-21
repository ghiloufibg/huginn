package gitlab

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// Commits implements ports.CommitLog with GitLab's repository API, read
// only, on the same client and token as the deployments
// (docs/plan/M16.1-commits-from-gitlab-api.md §3). No git command runs.
type Commits struct {
	Client *Client
}

// lotsOfCommits is the count shown, as a lower bound, when GitLab does not
// say how many commits a range has (it drops X-Total for large lists).
const lotsOfCommits = 10000

// pathPage is how many commits are listed per path of a monorepo workload;
// beyond it the count is a lower bound.
const pathPage = 100

var refName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._+/-]*$`)

func validRef(s string) bool { return refName.MatchString(s) && !strings.Contains(s, "..") }

// Commit implements ports.CommitLog.
func (c *Commits) Commit(ctx context.Context, p ports.ReleaseProject, sha string) (ports.Commit, error) {
	if !domain.IsCommitID(sha) {
		return ports.Commit{}, domain.KindError(domain.ErrNotFound, "not a commit id")
	}
	var raw struct {
		ID            string    `json:"id"`
		Title         string    `json:"title"`
		AuthorName    string    `json:"author_name"`
		CommittedDate time.Time `json:"committed_date"`
	}
	if _, err := c.Client.getJSONHeader(ctx, p, "/repository/commits/"+url.PathEscape(sha), nil, &raw); err != nil {
		return ports.Commit{}, notFound(err, "unknown commit "+sha+" in "+p.Path)
	}
	return ports.Commit{SHA: raw.ID, Author: raw.AuthorName, Subject: raw.Title, At: raw.CommittedDate}, nil
}

// Tag implements ports.CommitLog.
func (c *Commits) Tag(ctx context.Context, p ports.ReleaseProject, tag string) (string, error) {
	if !validRef(tag) {
		return "", domain.KindError(domain.ErrNotFound, "not a tag name")
	}
	var raw struct {
		Commit struct {
			ID string `json:"id"`
		} `json:"commit"`
	}
	if _, err := c.Client.getJSONHeader(ctx, p, "/repository/tags/"+url.PathEscape(tag), nil, &raw); err != nil {
		return "", notFound(err, "no tag "+tag+" in "+p.Path)
	}
	return raw.Commit.ID, nil
}

// DefaultBranch implements ports.CommitLog.
func (c *Commits) DefaultBranch(ctx context.Context, p ports.ReleaseProject) (string, error) {
	var raw struct {
		DefaultBranch string `json:"default_branch"`
	}
	if _, err := c.Client.getJSONHeader(ctx, p, "", nil, &raw); err != nil {
		return "", err
	}
	if raw.DefaultBranch == "" {
		return "", domain.KindError(domain.ErrNotFound, p.Path+" has no default branch")
	}
	return raw.DefaultBranch, nil
}

// BranchTip implements ports.CommitLog.
func (c *Commits) BranchTip(ctx context.Context, p ports.ReleaseProject, branch string) (string, error) {
	if !validRef(branch) {
		return "", domain.KindError(domain.ErrConfig, "invalid branch name "+strconv.Quote(branch))
	}
	var raw struct {
		Commit struct {
			ID string `json:"id"`
		} `json:"commit"`
	}
	if _, err := c.Client.getJSONHeader(ctx, p, "/repository/branches/"+url.PathEscape(branch), nil, &raw); err != nil {
		return "", notFound(err, "no branch "+branch+" in "+p.Path)
	}
	return raw.Commit.ID, nil
}

// Count implements ports.CommitLog: the commits of sha..branch are counted
// from the X-Total header of a one-item page, and sha is on branch when it
// is their merge base. With paths, the commit ids of each path are listed
// (one page each) and their union counted.
func (c *Commits) Count(ctx context.Context, p ports.ReleaseProject, sha, branch string, paths []string) (int, bool, bool, error) {
	if !domain.IsCommitID(sha) || !validRef(branch) {
		return 0, false, false, domain.KindError(domain.ErrConfig, "invalid commit or branch")
	}
	for _, path := range paths {
		if path == "" || strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
			return 0, false, false, domain.KindError(domain.ErrConfig, "invalid path "+strconv.Quote(path))
		}
	}
	on, err := c.onBranch(ctx, p, sha, branch)
	if err != nil {
		return 0, false, false, err
	}
	var behind int
	var capped bool
	if len(paths) == 0 {
		behind, capped, err = c.countRange(ctx, p, sha, branch)
	} else {
		behind, capped, err = c.countPaths(ctx, p, sha, branch, paths)
	}
	return behind, capped, on, err
}

type commitID struct {
	ID string `json:"id"`
}

func (c *Commits) onBranch(ctx context.Context, p ports.ReleaseProject, sha, branch string) (bool, error) {
	var base commitID
	q := url.Values{"refs[]": {sha, branch}}
	if _, err := c.Client.getJSONHeader(ctx, p, "/repository/merge_base", q, &base); err != nil {
		return false, err
	}
	return base.ID != "" && (strings.HasPrefix(base.ID, sha) || strings.HasPrefix(sha, base.ID)), nil
}

// countRange counts sha..branch, falling back to the compare API when the
// server refuses a revision range in ref_name (an old GitLab).
func (c *Commits) countRange(ctx context.Context, p ports.ReleaseProject, sha, branch string) (int, bool, error) {
	var page []commitID
	q := url.Values{"ref_name": {sha + ".." + branch}, "per_page": {"1"}}
	h, err := c.Client.getJSONHeader(ctx, p, "/repository/commits", q, &page)
	var se *statusErr
	if errors.As(err, &se) && (se.code == http.StatusBadRequest || se.code == http.StatusNotFound) {
		return c.compare(ctx, p, sha, branch)
	}
	if err != nil {
		return 0, false, err
	}
	if total, err := strconv.Atoi(h.Get("X-Total")); err == nil {
		return total, false, nil
	}
	if h.Get("X-Next-Page") != "" { // more than one, and GitLab does not say how many
		return lotsOfCommits, true, nil
	}
	return len(page), false, nil
}

func (c *Commits) compare(ctx context.Context, p ports.ReleaseProject, sha, branch string) (int, bool, error) {
	var raw struct {
		Commits        []commitID `json:"commits"`
		CompareTimeout bool       `json:"compare_timeout"`
	}
	q := url.Values{"from": {sha}, "to": {branch}}
	if _, err := c.Client.getJSONHeader(ctx, p, "/repository/compare", q, &raw); err != nil {
		return 0, false, err
	}
	return len(raw.Commits), raw.CompareTimeout, nil
}

func (c *Commits) countPaths(ctx context.Context, p ports.ReleaseProject, sha, branch string, paths []string) (int, bool, error) {
	seen := map[string]bool{}
	capped := false
	for _, path := range paths {
		var page []commitID
		q := url.Values{"ref_name": {sha + ".." + branch}, "path": {strings.TrimSuffix(path, "/")}, "per_page": {strconv.Itoa(pathPage)}}
		h, err := c.Client.getJSONHeader(ctx, p, "/repository/commits", q, &page)
		if err != nil {
			return 0, false, err
		}
		for _, id := range page {
			seen[id.ID] = true
		}
		capped = capped || h.Get("X-Next-Page") != ""
	}
	return len(seen), capped, nil
}

// notFound gives a 404 the message of what was looked for.
func notFound(err error, msg string) error {
	if errors.Is(err, domain.ErrNotFound) {
		return domain.KindError(domain.ErrNotFound, msg)
	}
	return err
}
