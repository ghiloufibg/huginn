package domain

import (
	"slices"
	"strings"
	"testing"
)

const (
	digA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestImageDigest(t *testing.T) {
	tests := map[string]string{
		"docker-pullable://eu.gcr.io/x/api@" + digA: digA,
		"europe-docker.pkg.dev/p/r/api@" + digA:     digA,
		digA:                                        digA,
		"":                                          "",
		"eu.gcr.io/x/api:1.0":                       "",
		"repo@sha256:short":                         "",
		"repo@sha256:" + strings.ToUpper(digA[7:]): "",
	}
	for in, want := range tests {
		if got := ImageDigest(in); got != want {
			t.Errorf("ImageDigest(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCommitCandidates(t *testing.T) {
	tests := map[string][]string{
		"2.14.3-9f03b11":                  {"9f03b11"},
		"9f03b11c4e2a":                    {"9f03b11c4e2a"},
		"main-9f03b11":                    {"9f03b11"},
		"20261119.1-9F03B11":              {"9f03b11"},
		"2.14.3":                          nil,
		"latest":                          nil,
		"20261119":                        nil, // a date is not a commit id
		"deadbee-0123456789abcdef0123abc": {"0123456789abcdef0123abc", "deadbee"},
	}
	for in, want := range tests {
		if got := CommitCandidates(in); !slices.Equal(got, want) {
			t.Errorf("CommitCandidates(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestIsCommitID(t *testing.T) {
	for s, want := range map[string]bool{"9f03b11": true, "9f03b1": false, "9F03B11": false, strings.Repeat("a", 40): true, strings.Repeat("a", 41): false, "--upload": false} {
		if IsCommitID(s) != want {
			t.Errorf("IsCommitID(%q) != %v", s, want)
		}
	}
}

// ipod is a pod of Deployment "api" running image tag:digest, created at minute min.
func ipod(name, rev, tag, digest string, min int) Pod {
	p := dpod(name, rev, min)
	id := ""
	if digest != "" {
		id = "repo/api@" + digest
	}
	p.Containers = []Container{
		{Name: "istio-proxy", Image: "proxy:1", ImageID: "proxy@" + digB},
		{Name: "api", Image: "repo/api:" + tag, ImageID: id},
	}
	return p
}

func TestRunningImages(t *testing.T) {
	f := ContainerFilter{Deny: []string{"istio-proxy"}}
	w := deployment()
	tests := []struct {
		name          string
		pods          []Pod
		current, prev WorkloadImage
	}{
		{
			"settled",
			[]Pod{ipod("a", "r1", "1.0", digA, 0), ipod("b", "r1", "1.0", digA, 1)},
			WorkloadImage{"api", digA, "1.0"},
			WorkloadImage{},
		},
		{
			"rollout",
			[]Pod{ipod("a", "r1", "1.0", digA, 0), ipod("c", "r2", "1.1", digB, 10)},
			WorkloadImage{"api", digB, "1.1"},
			WorkloadImage{"api", digA, "1.0"},
		},
		{
			"rollback: the old image's pods are the new ones",
			[]Pod{ipod("c", "r2", "1.1", digB, 10), ipod("d", "r1", "1.0", digA, 20)},
			WorkloadImage{"api", digA, "1.0"},
			WorkloadImage{"api", digB, "1.1"},
		},
		{
			"a new pod still pulling joins its tag",
			[]Pod{ipod("a", "r2", "1.1", "", 12), ipod("c", "r2", "1.1", digB, 10)},
			WorkloadImage{"api", digB, "1.1"},
			WorkloadImage{},
		},
		{
			"nothing pulled yet",
			[]Pod{ipod("a", "r1", "1.0", "", 0)},
			WorkloadImage{"api", "", "1.0"},
			WorkloadImage{},
		},
		{
			"a restart keeps the image",
			[]Pod{ipod("a", "r1", "1.0", digA, 0), ipod("b", "r2", "1.0", digA, 5)},
			WorkloadImage{"api", digA, "1.0"},
			WorkloadImage{},
		},
		{"other workloads and deleted pods are ignored", []Pod{
			ipod("a", "r1", "1.0", digA, 0),
			func() Pod { p := ipod("x", "r9", "9", digB, 30); p.OwnerName = "other"; return p }(),
			func() Pod { p := ipod("y", "r2", "1.1", digB, 30); p.Deleted = true; return p }(),
		}, WorkloadImage{"api", digA, "1.0"}, WorkloadImage{}},
		{"no pod", nil, WorkloadImage{}, WorkloadImage{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cur, prev := RunningImages(w, tt.pods, f)
			if cur != tt.current || prev != tt.prev {
				t.Errorf("got %+v, %+v; want %+v, %+v", cur, prev, tt.current, tt.prev)
			}
		})
	}
}

func TestParseGitRemote(t *testing.T) {
	tests := map[string]GitProject{
		"git@gitlab.acme.example:payments/payment-service.git":     {"gitlab.acme.example", "payments/payment-service"},
		"ssh://git@gitlab.acme.example:2222/a/b/c.git":             {"gitlab.acme.example", "a/b/c"},
		"https://gitlab.acme.example/payments/payment-service.git": {"gitlab.acme.example", "payments/payment-service"},
		"https://user:tok@gitlab.acme.example:8443/g/p":            {"gitlab.acme.example", "g/p"},
		"gitlab.acme.example:g/p/":                                 {"gitlab.acme.example", "g/p"},
	}
	for in, want := range tests {
		if got, ok := ParseGitRemote(in); !ok || got != want {
			t.Errorf("ParseGitRemote(%q) = %+v, %v; want %+v", in, got, ok, want)
		}
	}
	for _, bad := range []string{"/srv/git/p.git", "file:///srv/git/p.git", "", "https://host/", "C:/repo"} {
		if got, ok := ParseGitRemote(bad); ok {
			t.Errorf("ParseGitRemote(%q) = %+v, want not ok", bad, got)
		}
	}
}

func TestShortSHA(t *testing.T) {
	if ShortSHA("9f03b11c4e2a") != "9f03b11c" || ShortSHA("abc") != "abc" {
		t.Error("ShortSHA")
	}
}
