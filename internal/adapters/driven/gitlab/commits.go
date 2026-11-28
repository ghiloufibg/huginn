package gitlab

import (
	"context"
	"errors"
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

// A range is counted by listing its commits page by page, following
// GitLab's documented X-Next-Page header; X-Total is not relied on (GitLab
// leaves it out of large lists). Beyond maxPages pages the count is a
// lower bound, shown as "1000+".
const (
	pageSize = 100
	maxPages = 10
)

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
		ParentIDs     []string  `json:"parent_ids"`
	}
	if _, err := c.Client.getJSONHeader(ctx, p, "/repository/commits/"+url.PathEscape(sha), nil, &raw); err != nil {
		return ports.Commit{}, notFound(err, "unknown commit "+sha+" in "+p.Path)
	}
	return ports.Commit{SHA: raw.ID, Author: raw.AuthorName, Subject: raw.Title, At: raw.CommittedDate, Parents: raw.ParentIDs}, nil
}

// introducedWindow is how many commits of a merge are read to find one that
// is not itself a merge (a branch tip is often "Merge branch 'main' into …").
const introducedWindow = 20

// Introduced implements ports.CommitLog: the newest commit of first..second
// with at most one parent. GitLab lists a range newest first.
func (c *Commits) Introduced(ctx context.Context, p ports.ReleaseProject, first, second string) (ports.Commit, error) {
	if !domain.IsCommitID(first) || !domain.IsCommitID(second) {
		return ports.Commit{}, domain.KindError(domain.ErrNotFound, "not a commit id")
	}
	var raw []struct {
		ID            string    `json:"id"`
		Title         string    `json:"title"`
		AuthorName    string    `json:"author_name"`
		CommittedDate time.Time `json:"committed_date"`
		ParentIDs     []string  `json:"parent_ids"`
	}
	q := url.Values{"ref_name": {first + ".." + second}, "per_page": {strconv.Itoa(introducedWindow)}}
	if _, err := c.Client.getJSONHeader(ctx, p, "/repository/commits", q, &raw); err != nil {
		return ports.Commit{}, err
	}
	for _, r := range raw {
		if len(r.ParentIDs) <= 1 {
			return ports.Commit{SHA: r.ID, Author: r.AuthorName, Subject: r.Title, At: r.CommittedDate, Parents: r.ParentIDs}, nil
		}
	}
	return ports.Commit{}, domain.KindError(domain.ErrNotFound, "no commit but merges in "+p.Path)
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

// Count implements ports.CommitLog: the commits of sha..branch (only those
// touching one of paths, when given) are listed and counted, and sha is on
// branch when it is their merge base.
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
	if len(paths) == 0 {
		paths = []string{""}
	}
	seen := map[string]bool{}
	capped := false
	for _, path := range paths {
		more, err := c.listRange(ctx, p, sha+".."+branch, strings.TrimSuffix(path, "/"), seen)
		if err != nil {
			return 0, false, false, err
		}
		capped = capped || more
	}
	return len(seen), capped, on, nil
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

// listRange adds to seen the commits of a revision range (touching path,
// when set), page by page; more says pages were left after maxPages.
func (c *Commits) listRange(ctx context.Context, p ports.ReleaseProject, rng, path string, seen map[string]bool) (more bool, err error) {
	page := "1"
	for range maxPages {
		q := url.Values{"ref_name": {rng}, "per_page": {strconv.Itoa(pageSize)}, "page": {page}}
		if path != "" {
			q.Set("path", path)
		}
		var ids []commitID
		h, err := c.Client.getJSONHeader(ctx, p, "/repository/commits", q, &ids)
		if err != nil {
			return false, err
		}
		for _, id := range ids {
			seen[id.ID] = true
		}
		if page = h.Get("X-Next-Page"); page == "" {
			return false, nil
		}
	}
	return true, nil
}

// notFound gives a 404 the message of what was looked for.
func notFound(err error, msg string) error {
	if errors.Is(err, domain.ErrNotFound) {
		return domain.KindError(domain.ErrNotFound, msg)
	}
	return err
}
