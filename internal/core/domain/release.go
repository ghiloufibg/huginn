package domain

import (
	"strings"
	"time"
)

// Release is a deployment of a commit to an environment, as the CI system
// recorded it (docs/plan/M16-deployed-commit.md).
type Release struct {
	// SHA is the full commit id the deployment deployed.
	SHA string
	// Ref is the branch or tag the pipeline ran on.
	Ref string
	// Subject is the first line of the commit message; Author and
	// CommittedAt describe the commit.
	Subject     string
	Author      string
	CommittedAt time.Time
	// DeployedAt is when the deployment finished; DeployedBy who ran it.
	DeployedAt time.Time
	DeployedBy string
	// Pipeline is the number of the pipeline that deployed it (0 unknown).
	Pipeline int64
}

// ReleaseState says how the commit running a workload was found.
type ReleaseState int

// Release states.
const (
	// ReleaseUnknown: no commit could be found.
	ReleaseUnknown ReleaseState = iota
	// ReleaseFound: a deployment of the environment printed the digest the
	// pods run, so its commit is proven.
	ReleaseFound
	// ReleaseGuessed: no deployment matched; the commit was read from the
	// image tag and confirmed by git, but not proven by a deployment.
	ReleaseGuessed
)

// TrunkCount compares the running commit with the trunk (compare_ref).
type TrunkCount struct {
	// Known is false when the count could not be computed.
	Known bool
	// Ref is the reference compared with, e.g. origin/HEAD.
	Ref string
	// Behind is the number of commits of Ref the running commit lacks.
	Behind int
	// OnTrunk is false when the running commit is not an ancestor of Ref
	// (a hotfix branch, a force-push).
	OnTrunk bool
	// Scoped is true when only the commits touching the workload's paths
	// were counted (a monorepo), false when the whole project's were.
	Scoped bool
	// WholeProject is true when the repository is a monorepo (some of its
	// workloads have paths) but this workload has none: Behind then counts
	// the other services' commits too, and must not read "not deployed".
	WholeProject bool
	// Stale is true when the count was read from the user's local clone
	// because the remote could not be reached.
	Stale bool
}

// RunningRelease is what the preview shows about the commit running one
// image digest.
type RunningRelease struct {
	State   ReleaseState
	Release Release
	// Guess says where a guessed commit came from ("image tag",
	// "git tag v2.14.3").
	Guess string
	// NotRecent is true when the release history was searched and no
	// recent deployment printed the digest: a rollback beyond the searched
	// deployments or a manual rollout.
	NotRecent bool
	// Count is the comparison with the trunk.
	Count TrunkCount
	// Problem, when set, is why the release history or git could not
	// answer (no token, unauthorized, unreachable). It is shown, never
	// fatal: a guess may still be there.
	Problem error
}

// Short returns the abbreviated commit id shown on screen.
func (r RunningRelease) Short() string { return ShortSHA(r.Release.SHA) }

// ShortSHA abbreviates a commit id to 8 characters.
func ShortSHA(sha string) string { return sha[:min(len(sha), 8)] }

// WorkloadImage is an image a workload runs, to look up its release.
type WorkloadImage struct {
	// Workload is the workload's name (for per-workload settings).
	Workload string
	// Digest is the running digest ("sha256:…"); Tag the image tag.
	Digest, Tag string
}

// ImageDigest returns the digest ("sha256:<hex>") of a container status's
// imageID ("docker-pullable://repo@sha256:…", "repo@sha256:…" or
// "sha256:…"), or "" when it holds none.
func ImageDigest(imageID string) string {
	if _, d, ok := strings.Cut(imageID, "@"); ok {
		imageID = d
	}
	if !IsDigest(imageID) {
		return ""
	}
	return imageID
}

// IsDigest reports whether s is a full "sha256:<64 lower-case hex>" digest.
func IsDigest(s string) bool {
	hex, ok := strings.CutPrefix(s, "sha256:")
	return ok && len(hex) == 64 && isHex(hex)
}

// IsCommitID reports whether s can be a (possibly abbreviated) commit id:
// 7 to 40 lower-case hexadecimal characters.
func IsCommitID(s string) bool { return len(s) >= 7 && len(s) <= 40 && isHex(s) }

func isHex(s string) bool {
	for i := range len(s) {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// CommitCandidates returns the parts of an image tag that may be a commit
// id ("2.14.3-9f03b11" → ["9f03b11"]), longest first. They are only
// candidates: git must confirm one before it is shown.
func CommitCandidates(tag string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(tag, func(r rune) bool { return r == '-' || r == '_' || r == '.' || r == '+' }) {
		if p := strings.ToLower(part); IsCommitID(p) && !isDecimal(p) {
			out = append(out, p)
		}
	}
	for i := 1; i < len(out); i++ { // longest first: the least ambiguous
		for j := i; j > 0 && len(out[j]) > len(out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// isDecimal: a build number or a date (20261119) is not a commit id.
func isDecimal(s string) bool {
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// RunningImages returns the image of the application container of w's
// newest pods and, during a rollout, the one of the previous pods: the
// images are told apart by digest and ordered by the creation of their
// first pod, as WorkloadVersion orders tags. Sidecars are skipped with f.
// A zero Digest means no pod of that image has pulled it yet.
func RunningImages(w Workload, pods []Pod, f ContainerFilter) (current, previous WorkloadImage) {
	var buf [4]imageSeen
	imgs := buf[:0]
	for i := range pods {
		p := &pods[i]
		if p.Deleted || !w.Owns(*p) {
			continue
		}
		if c, ok := f.PrimaryApp(*p); ok {
			imgs = addImage(imgs, WorkloadImage{Workload: w.Ref.Name, Digest: ImageDigest(c.ImageID), Tag: ImageTag(c.Image)}, p.Created)
		}
	}
	var cur, prev *imageSeen
	for i := range imgs {
		s := &imgs[i]
		switch {
		case cur == nil || s.first.After(cur.first):
			prev, cur = cur, s
		case prev == nil || s.first.After(prev.first):
			prev = s
		}
	}
	if cur != nil {
		current = cur.img
	}
	if prev != nil {
		previous = prev.img
	}
	return current, previous
}

// imageSeen is an image of a workload and the creation of its first pod.
type imageSeen struct {
	img   WorkloadImage
	first time.Time
}

// addImage records that a pod created at runs img. Images are the same
// when their digests are; a pod that has not pulled yet (no digest) joins
// the image of the same tag.
func addImage(imgs []imageSeen, img WorkloadImage, at time.Time) []imageSeen {
	for i := range imgs {
		s := &imgs[i]
		same := s.img.Digest == img.Digest && s.img.Tag == img.Tag
		switch {
		case !same && img.Digest == "" && s.img.Tag == img.Tag:
			same = true
		case !same && s.img.Digest == "" && s.img.Tag == img.Tag:
			s.img.Digest, same = img.Digest, true
		}
		if same {
			if at.Before(s.first) {
				s.first = at
			}
			return imgs
		}
	}
	return append(imgs, imageSeen{img, at})
}

// GitProject is a repository's project on its git hosting, read from the
// clone's remote URL.
type GitProject struct {
	// Host is the server name ("gitlab.example.com"); Path the project's
	// full path ("group/sub/project"), without ".git".
	Host, Path string
}

// ParseGitRemote reads a git remote URL: scp-like
// ("git@host:group/project.git"), ssh://, https:// or http://. It reports
// false for anything else (a local path).
func ParseGitRemote(url string) (GitProject, bool) {
	var host, path string
	switch {
	case strings.Contains(url, "://"):
		scheme, rest, _ := strings.Cut(url, "://")
		if scheme != "ssh" && scheme != "https" && scheme != "http" && scheme != "git+ssh" {
			return GitProject{}, false
		}
		host, path, _ = strings.Cut(rest, "/")
		if _, h, ok := strings.Cut(host, "@"); ok { // user[:password]@host
			host = h
		}
		if h, _, ok := strings.Cut(host, ":"); ok { // host:port
			host = h
		}
	case strings.Contains(url, ":") && !strings.HasPrefix(url, "/"):
		host, path, _ = strings.Cut(url, ":")
		if _, h, ok := strings.Cut(host, "@"); ok {
			host = h
		}
		if len(host) == 1 { // a Windows drive letter: a local path, as git reads it
			return GitProject{}, false
		}
	default:
		return GitProject{}, false
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	if host == "" || path == "" || strings.Contains(host, "/") {
		return GitProject{}, false
	}
	return GitProject{Host: host, Path: path}, true
}
