package config

import (
	"testing"
	"testing/fstest"
	"time"
)

func withReleases(releases, envs string) fstest.MapFS {
	fs := valid()
	fs["huginn.yaml"] = &fstest.MapFile{Data: []byte("version: 1\ndefault_env: rec\nrepos_root: /repos\n")}
	if envs != "" {
		fs["environments.yaml"] = &fstest.MapFile{Data: []byte(envs)}
	}
	fs["releases.yaml"] = &fstest.MapFile{Data: []byte(releases)}
	return fs
}

const gitlabEnvs = "version: 1\nenvironments:\n  rec: {namespaces: [app-rec]}\n  prd:\n    namespaces: [app-prd]\n    gitlab: {environment: prd, job: 'promote-{workload}-prd'}\n"

func TestReleasesDefaults(t *testing.T) {
	c, msg := load(t, withReleases("version: 1\nprovider: gitlab\ntoken: {file: .env, key: GITLAB_TOKEN}\n", gitlabEnvs))
	if msg != "" {
		t.Fatal(msg)
	}
	r := c.Releases
	if r == nil || r.Remote != "origin" || r.PerPage != 5 || r.MaxDeployments != 20 || r.CompareRef != "origin/HEAD" {
		t.Fatalf("releases = %+v", r)
	}
	if maxLog, refresh := r.Settings(); maxLog != 20<<20 || refresh != time.Minute {
		t.Errorf("settings = %d, %v", maxLog, refresh)
	}
	if g := c.Environments.ByName["prd"].GitLab; g == nil || g.Job != "promote-{workload}-prd" {
		t.Errorf("gitlab = %+v", g)
	}
}

func TestNoReleasesFile(t *testing.T) {
	c, msg := load(t, valid())
	if msg != "" || c.Releases != nil {
		t.Fatalf("releases = %+v, %s", c.Releases, msg)
	}
}

func TestReleasesErrors(t *testing.T) {
	fs := withReleases(`version: 1
provider: github
url: gitlab.example.com
token: {file: ../secrets/.env}
remote: "--upload-pack=x"
per_page: 500
max_deployments: -1
max_log_bytes: lots
compare_ref: "origin/main..x"
refresh: 1s
repos:
  mono:
    job: {qa: promote}
    workloads:
      api:
        paths: [/abs, ""]
        environment: {prd: "prd/{service}"}
`, "version: 1\nenvironments:\n  rec: {namespaces: [app-rec]}\n  prd:\n    namespaces: [app-prd]\n    gitlab: {environment: prd}\n")
	fs["huginn.yaml"] = &fstest.MapFile{Data: []byte("version: 1\ndefault_env: rec\n")}
	_, msg := load(t, fs)
	wantErrors(t, msg,
		`"github" is not one of: gitlab`,
		`missing required key "key"`,
		`releases.yaml needs repos_root`,
		`url: must start with https://`,
		`token.file: must be a file of the config folder`,
		`"--upload-pack=x" is not a remote name`,
		`per_page: must be between 1 and 100`,
		`max_deployments: must be at least 1`,
		`max_log_bytes`,
		`"origin/main..x" is not --upload-pack=x/HEAD`,
		`"1s" is not a duration of at least 10s`,
		`"qa" is not in environments.yaml`,
		`"/abs" is not a folder relative to the repository`,
		`unknown placeholder {service}`,
		`environments.prd.gitlab: missing required key "job"`,
	)
}

func TestUnknownRootFileSuggestsReleases(t *testing.T) {
	fs := valid()
	fs["release.yaml"] = &fstest.MapFile{Data: []byte("version: 1\n")}
	_, msg := load(t, fs)
	wantErrors(t, msg, "did you mean releases.yaml?")
}

func TestReleasesCompareRef(t *testing.T) {
	for ref, ok := range map[string]bool{
		"origin/HEAD": true, "origin/main": true, "origin/release/2026": true,
		"main": false, "upstream/main": false, "origin/": false, "origin/a..b": false,
	} {
		_, msg := load(t, withReleases("version: 1\nprovider: gitlab\ntoken: {file: .env, key: K}\ncompare_ref: "+ref+"\n", gitlabEnvs))
		if got := msg == ""; got != ok {
			t.Errorf("compare_ref %q: loaded %v, want %v (%s)", ref, got, ok, msg)
		}
		if !ok {
			wantErrors(t, msg, `is not origin/HEAD or origin/<branch>`)
		}
	}
}
