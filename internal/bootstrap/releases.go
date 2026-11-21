package bootstrap

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/ghiloufibg/huginn/internal/adapters/driven/demo"
	"github.com/ghiloufibg/huginn/internal/adapters/driven/gitlab"
	"github.com/ghiloufibg/huginn/internal/adapters/driven/localfiles"
	"github.com/ghiloufibg/huginn/internal/config"
	"github.com/ghiloufibg/huginn/internal/core/app"
	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// newReleases wires the COMMIT line of the services preview, nil when the
// folder has no releases.yaml. With --demo the release history and the
// project histories are synthetic; otherwise both are GitLab's API, and
// each clone's remote is read from its .git/config: no git command runs.
// notice is a warning about the token file to show in the status bar.
func newReleases(c *config.Config, clientName string, cluster Cluster, clock ports.Clock, log *slog.Logger) (r ports.Releases, notice string) {
	rc := c.Releases
	if rc == nil {
		return nil, ""
	}
	maxLog, refresh := rc.Settings()
	settings := app.ReleaseSettings{
		ReposRoot: c.Huginn.ReposRoot, Remote: rc.Remote, BaseURL: strings.TrimRight(rc.URL, "/"),
		PerPage: rc.PerPage, MaxDeployments: rc.MaxDeployments, MaxLogBytes: maxLog,
		CompareRef: rc.CompareRef, Refresh: refresh,
		Envs: map[domain.Env]app.ReleaseNames{}, Repos: map[string]app.ReleaseRepo{},
	}
	for name, e := range c.Environments.ByName {
		if e.GitLab != nil {
			settings.Envs[domain.Env(name)] = app.ReleaseNames{Environment: e.GitLab.Environment, Job: e.GitLab.Job}
		}
	}
	for name, rp := range rc.Repos {
		repo := app.ReleaseRepo{CompareRef: rp.CompareRef, MaxDeployments: rp.MaxDeployments, Envs: envNames(rp.Environment, rp.Job), Workloads: map[string]app.ReleaseWorkload{}}
		for wl, w := range rp.Workloads {
			repo.Workloads[wl] = app.ReleaseWorkload{Paths: w.Paths, Envs: envNames(w.Environment, w.Job)}
		}
		settings.Repos[name] = repo
	}
	uc := &app.Releases{Clock: clock, Log: log, Settings: settings}
	if dc, ok := cluster.(*demo.Cluster); ok && clientName == "demo" {
		uc.History, uc.Commits, uc.Remotes = &demo.Releases{Cluster: dc}, demo.Commits{}, demo.Remotes{}
		return uc, ""
	}
	file := filepath.Join(c.Dir, rc.Token.File)
	client := &gitlab.Client{Token: tokenReader(file, rc.Token.File, rc.Token.Key)}
	uc.History, uc.Commits, uc.Remotes = client, &gitlab.Commits{Client: client}, &localfiles.Files{}
	notice = tokenNotice(c.Dir, rc.Token.File)
	if notice != "" {
		log.Warn(notice)
	}
	return uc, notice
}

// envNames merges per-environment environment and job overrides.
func envNames(envs, jobs map[string]string) map[domain.Env]app.ReleaseNames {
	out := map[domain.Env]app.ReleaseNames{}
	for env, v := range envs {
		n := out[domain.Env(env)]
		n.Environment = v
		out[domain.Env(env)] = n
	}
	for env, v := range jobs {
		n := out[domain.Env(env)]
		n.Job = v
		out[domain.Env(env)] = n
	}
	return out
}

// tokenReader reads the token from the dotenv file when it changes, so a
// token added or renewed while Huginn runs is used at once, while each
// request costs only a stat of the file. The token is wrapped in a Secret
// at once and never logged.
func tokenReader(path, name, key string) func(context.Context) (domain.Secret, error) {
	missing := domain.KindError(domain.ErrUnauthorized, fmt.Sprintf("no GitLab token (%s %s)", name, key))
	var mu sync.Mutex
	var seen os.FileInfo
	var tok domain.Secret
	return func(context.Context) (domain.Secret, error) {
		st, err := os.Stat(path)
		if err != nil {
			return domain.Secret{}, missing
		}
		mu.Lock()
		defer mu.Unlock()
		if seen != nil && st.ModTime().Equal(seen.ModTime()) && st.Size() == seen.Size() && os.SameFile(st, seen) {
			if tok.Reveal() == "" {
				return domain.Secret{}, missing
			}
			return tok, nil
		}
		tok, seen = readToken(path, key), st
		if tok.Reveal() == "" {
			return domain.Secret{}, missing
		}
		return tok, nil
	}
}

// readToken returns the token of key in a dotenv file, empty when absent.
func readToken(path, key string) domain.Secret {
	f, err := os.Open(path)
	if err != nil {
		return domain.Secret{}
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, 64<<10))
	if err != nil {
		return domain.Secret{}
	}
	return domain.NewSecret(strings.TrimSpace(domain.ParseDotenv(b)[key].Reveal()))
}

// tokenNotice warns when the token file could leak: readable by other
// users, or in a git work tree (a shared config folder) without being
// ignored. The ignore check reads the folder's .gitignore without git: it
// knows the common lines only, so it may warn about a file ignored
// elsewhere (a parent folder, a global excludes file), never the reverse.
func tokenNotice(dir, name string) string {
	path := filepath.Join(dir, name)
	st, err := os.Stat(path)
	if err != nil {
		return ""
	}
	var warns []string
	if runtime.GOOS != "windows" && st.Mode().Perm()&0o077 != 0 {
		warns = append(warns, fmt.Sprintf("%s is readable by others (chmod 600 %s)", name, path))
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil && !ignores(filepath.Join(dir, ".gitignore"), name) {
		warns = append(warns, fmt.Sprintf("%s is not git-ignored (add it to %s)", name, filepath.Join(dir, ".gitignore")))
	}
	return strings.Join(warns, "; ")
}

// ignores tells whether a .gitignore has a line ignoring name, a file at
// the folder's root: the name itself, rooted ("/name"), or a pattern of
// the file's base name ("*.env", ".env*", "*").
func ignores(gitignore, name string) bool {
	b, err := os.ReadFile(gitignore)
	if err != nil {
		return false
	}
	ignored := false
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		negate := strings.HasPrefix(line, "!")
		pattern := strings.TrimPrefix(strings.TrimPrefix(line, "!"), "/")
		if ok, _ := filepath.Match(pattern, name); ok || pattern == name {
			ignored = !negate
		}
	}
	return ignored
}
