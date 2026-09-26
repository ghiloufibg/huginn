package domain

import "testing"

func TestContainerFilter(t *testing.T) {
	f := ContainerFilter{Deny: []string{"istio-proxy", "vault-agent", "fluent-bit"}, Allow: []string{"fluent-bit"}}
	tests := []struct {
		c    Container
		want bool
	}{
		{Container{Name: "api"}, true},
		{Container{Name: "istio-proxy"}, false},
		{Container{Name: "vault-agent-init"}, false},
		{Container{Name: "istio-init", Init: true}, false},
		{Container{Name: "migrate", Init: true}, false},
		{Container{Name: "sidecar", Image: "hashicorp/vault-agent:1.18"}, false},
		{Container{Name: "fluent-bit"}, true},
		{Container{Name: "istio-proxy-helper"}, false},
		{Container{Name: "worker", Image: "eu.gcr.io/acme/worker:v1"}, true},
	}
	for _, tt := range tests {
		if got := f.IsApp(tt.c, "api"); got != tt.want {
			t.Errorf("%s (%s): got %v", tt.c.Name, tt.c.Image, got)
		}
	}
	if !(ContainerFilter{IncludeInit: true}).IsApp(Container{Name: "migrate", Init: true}, "api") {
		t.Error("IncludeInit")
	}
	if !(ContainerFilter{Deny: []string{"api"}}).IsApp(Container{Name: "api"}, "api") {
		t.Error("container named like its workload is always the app")
	}
}

func TestPrimaryApp(t *testing.T) {
	p := Pod{OwnerName: "api", Containers: []Container{{Name: "istio-proxy"}, {Name: "helper"}, {Name: "api"}}}
	if c, ok := (ContainerFilter{Deny: []string{"istio-proxy"}}).PrimaryApp(p); !ok || c.Name != "api" {
		t.Fatalf("got %v", c.Name)
	}
}

func TestImageTag(t *testing.T) {
	for in, want := range map[string]string{
		"eu.gcr.io/acme/api:v2.14.3":               "v2.14.3",
		"localhost:5000/api":                       "latest",
		"localhost:5000/api:a41c9e2":               "a41c9e2",
		"api@sha256:1a2b3c4d5e6f7a8b9c0d":          "sha256:1a2b3c4",
		"docker.io/istio/proxyv2:1.24@sha256:ffff": "sha256:ffff",
	} {
		if got := ImageTag(in); got != want {
			t.Errorf("%s: got %q, want %q", in, got, want)
		}
	}
}
