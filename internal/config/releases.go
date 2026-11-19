package config

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Releases is releases.yaml: where the release running in an environment
// is read (docs/plan/M16-deployed-commit.md). Optional.
type Releases struct {
	Version  int    `yaml:"version" doc:"Structure version of this file; must be 1." required:"true"`
	Provider string `yaml:"provider" doc:"Where deployments are recorded." enum:"gitlab" required:"true"`
	URL      string `yaml:"url" doc:"Base URL of the GitLab server, e.g. https://gitlab.example.com. Default: https:// and the host of each clone's remote."`
	Token    Token  `yaml:"token" doc:"Where the GitLab access token (scope read_api, role Reporter or above) is read." required:"true"`
	Remote   string `yaml:"remote" doc:"Remote of each clone (repos_root/<repository>) that names its GitLab project. Default origin."`
	// PerPage and MaxDeployments bound the search of one digest.
	PerPage        int    `yaml:"per_page" doc:"Deployments read per request, newest first. Default 5, at most 100."`
	MaxDeployments int    `yaml:"max_deployments" doc:"Deployments searched before the line says the image is not a recent release. Default 20."`
	MaxLogBytes    string `yaml:"max_log_bytes" doc:"A job log is read up to this size, e.g. 20MiB. Default 20MiB."`
	CompareRef     string `yaml:"compare_ref" doc:"Reference the running commit is compared with, to count the commits not deployed. Default origin/HEAD, the remote's default branch."`
	Refresh        string `yaml:"refresh" doc:"How old Huginn's copy of a remote, and a 'not a recent release' answer, may get before they are read again. At least 10s. Default 1m."`
	// Repos holds the exceptions of some repositories.
	Repos map[string]ReleaseRepo `yaml:"repos" doc:"Exceptions for some repositories, by name."`
}

// Token says where the access token is: a dotenv file and a key.
type Token struct {
	File string `yaml:"file" doc:"Dotenv file holding the token, relative to the config folder, e.g. .env. Keep it out of git and readable by you only." required:"true"`
	Key  string `yaml:"key" doc:"Key of the token in that file, e.g. GITLAB_TOKEN." required:"true"`
}

// ReleaseRepo is a repository's exceptions.
type ReleaseRepo struct {
	CompareRef     string                     `yaml:"compare_ref" doc:"Reference compared with for this repository."`
	MaxDeployments int                        `yaml:"max_deployments" doc:"Deployments searched for this repository (a monorepo needs more)."`
	Environment    map[string]string          `yaml:"environment" doc:"GitLab environment by Huginn environment, instead of environments.yaml's."`
	Job            map[string]string          `yaml:"job" doc:"Job by Huginn environment, instead of environments.yaml's."`
	Workloads      map[string]ReleaseWorkload `yaml:"workloads" doc:"Exceptions of some workloads, by name (a monorepo)."`
}

// ReleaseWorkload is a workload's exceptions.
type ReleaseWorkload struct {
	Paths       []string          `yaml:"paths" doc:"Folders of this workload's code in the repository: only the commits touching them count as not deployed."`
	Environment map[string]string `yaml:"environment" doc:"GitLab environment by Huginn environment."`
	Job         map[string]string `yaml:"job" doc:"Job by Huginn environment."`
}

// Settings returns the limits as values. The folder was validated.
func (r *Releases) Settings() (maxLog int64, refresh time.Duration) {
	maxLog, _ = ParseByteSize(r.MaxLogBytes)
	refresh, _ = time.ParseDuration(r.Refresh)
	return maxLog, refresh
}

func releasesDefaults(r *Releases) {
	if r.Remote == "" {
		r.Remote = "origin"
	}
	if r.PerPage == 0 {
		r.PerPage = 5
	}
	if r.MaxDeployments == 0 {
		r.MaxDeployments = 20
	}
	if r.MaxLogBytes == "" {
		r.MaxLogBytes = "20MiB"
	}
	if r.CompareRef == "" {
		r.CompareRef = "origin/HEAD"
	}
	if r.Refresh == "" {
		r.Refresh = "1m"
	}
}

// refName is what a remote name or a reference may look like: no option,
// no range, no space (they reach git's command line).
var refName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._/-]*$`)

func (v *validator) releases() {
	r, f := v.c.Releases, FileReleases
	if r == nil {
		return
	}
	if v.c.Huginn.ReposRoot == "" {
		v.add(f, "", "releases.yaml needs repos_root in %s: the clones give each repository's GitLab project", FileHuginn)
	}
	if r.URL != "" && !strings.HasPrefix(r.URL, "https://") && !strings.HasPrefix(r.URL, "http://") {
		v.add(f, "url", "must start with https:// (or http://)")
	}
	if r.Token.File != "" && (strings.HasPrefix(r.Token.File, "/") || strings.Contains(r.Token.File, "..")) {
		v.add(f, "token.file", "must be a file of the config folder, e.g. .env")
	}
	if !refName.MatchString(r.Remote) {
		v.add(f, "remote", "%q is not a remote name", r.Remote)
	}
	if r.PerPage < 1 || r.PerPage > 100 {
		v.add(f, "per_page", "must be between 1 and 100")
	}
	if r.MaxDeployments < 1 {
		v.add(f, "max_deployments", "must be at least 1")
	}
	if n, err := ParseByteSize(r.MaxLogBytes); err != nil {
		v.add(f, "max_log_bytes", "%v", err)
	} else if n < 1 {
		v.add(f, "max_log_bytes", "must be at least 1 byte")
	}
	v.ref("compare_ref", r.CompareRef)
	if d, err := time.ParseDuration(r.Refresh); err != nil || d < 10*time.Second {
		v.add(f, "refresh", "%q is not a duration of at least 10s, such as 1m", r.Refresh)
	}
	for _, name := range sortedKeys(r.Repos) {
		rp, p := r.Repos[name], "repos."+name
		if rp.CompareRef != "" {
			v.ref(p+".compare_ref", rp.CompareRef)
		}
		if rp.MaxDeployments < 0 {
			v.add(f, p+".max_deployments", "must not be negative")
		}
		v.envNames(p+".environment", rp.Environment)
		v.envNames(p+".job", rp.Job)
		for _, wl := range sortedKeys(rp.Workloads) {
			w, wp := rp.Workloads[wl], p+".workloads."+wl
			v.envNames(wp+".environment", w.Environment)
			v.envNames(wp+".job", w.Job)
			for i, path := range w.Paths {
				if path == "" || strings.HasPrefix(path, "/") || strings.HasPrefix(path, "-") || strings.Contains(path, "..") {
					v.add(f, fmt.Sprintf("%s.paths[%d]", wp, i), "%q is not a folder relative to the repository", path)
				}
			}
		}
	}
}

func (v *validator) ref(at, ref string) {
	if !refName.MatchString(ref) || strings.Contains(ref, "..") {
		v.add(FileReleases, at, "%q is not a git reference such as origin/HEAD or origin/main", ref)
	}
}

// envNames checks a map keyed by Huginn environment.
func (v *validator) envNames(at string, m map[string]string) {
	for _, env := range sortedKeys(m) {
		if _, ok := v.c.Environments.ByName[env]; !ok && len(v.c.Environments.Names) > 0 {
			v.add(FileReleases, at+"."+env, "%q is not in %s", env, FileEnvironments)
		}
		v.placeholders(FileReleases, at+"."+env, m[env])
	}
}

var placeholder = regexp.MustCompile(`\{[^}]*\}`)

// placeholders checks that a GitLab name uses only {repo} and {workload}.
func (v *validator) placeholders(file, at, s string) {
	if strings.TrimSpace(s) == "" {
		v.add(file, at, "must not be empty")
		return
	}
	for _, ph := range placeholder.FindAllString(s, -1) {
		if ph != "{repo}" && ph != "{workload}" {
			v.add(file, at, "unknown placeholder %s (use {repo} or {workload})", ph)
		}
	}
}
