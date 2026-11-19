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

	"github.com/ghiloufibg/huginn/internal/adapters/driven/demo"
	"github.com/ghiloufibg/huginn/internal/adapters/driven/gitcli"
	"github.com/ghiloufibg/huginn/internal/adapters/driven/gitlab"
	"github.com/ghiloufibg/huginn/internal/config"
	"github.com/ghiloufibg/huginn/internal/core/app"
	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// newReleases wires the COMMIT line of the services preview, nil when the
// folder has no releases.yaml. With --demo the release history and git are
// synthetic; otherwise GitLab's API and the git command. notice is a
// warning about the token file to show in the status bar.
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
		uc.History, uc.Git = &demo.Releases{Cluster: dc}, demo.Git{}
		return uc, ""
	}
	git := &gitcli.Git{CacheDir: filepath.Join(cacheDir(), "git")}
	file := filepath.Join(c.Dir, rc.Token.File)
	uc.History, uc.Git = &gitlab.Client{Token: tokenReader(file, rc.Token.File, rc.Token.Key)}, git
	notice = tokenNotice(git, c.Dir, rc.Token.File)
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

// tokenReader reads the token from the dotenv file on each use, so a token
// added while Huginn runs is used without a restart. It is wrapped in a
// Secret at once and never logged.
func tokenReader(path, name, key string) func(context.Context) (domain.Secret, error) {
	missing := domain.KindError(domain.ErrUnauthorized, fmt.Sprintf("no GitLab token (%s %s)", name, key))
	return func(context.Context) (domain.Secret, error) {
		f, err := os.Open(path)
		if err != nil {
			return domain.Secret{}, missing
		}
		defer func() { _ = f.Close() }()
		b, err := io.ReadAll(io.LimitReader(f, 64<<10))
		if err != nil {
			return domain.Secret{}, missing
		}
		tok, ok := domain.ParseDotenv(b)[key]
		if !ok || strings.TrimSpace(tok.Reveal()) == "" {
			return domain.Secret{}, missing
		}
		return domain.NewSecret(strings.TrimSpace(tok.Reveal())), nil
	}
}

// tokenNotice warns when the token file could leak: readable by other
// users, or in a git work tree (a shared config folder) without being
// ignored.
func tokenNotice(git *gitcli.Git, dir, name string) string {
	path := filepath.Join(dir, name)
	st, err := os.Stat(path)
	if err != nil {
		return ""
	}
	var warns []string
	if runtime.GOOS != "windows" && st.Mode().Perm()&0o077 != 0 {
		warns = append(warns, fmt.Sprintf("%s is readable by others (chmod 600 %s)", name, path))
	}
	if inRepo, ignored := git.Ignored(context.Background(), dir, name); inRepo && !ignored {
		warns = append(warns, fmt.Sprintf("%s is not git-ignored (add it to %s)", name, filepath.Join(dir, ".gitignore")))
	}
	return strings.Join(warns, "; ")
}
