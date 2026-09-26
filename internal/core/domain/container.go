package domain

import (
	"slices"
	"strings"
)

// ContainerFilter decides which containers are application containers.
// Auxiliary containers (service-mesh sidecars, secret agents, log shippers,
// init containers) are hidden from the logs and from restart counts.
type ContainerFilter struct {
	// Deny lists auxiliary container names. A container is auxiliary when
	// its name equals an entry or starts with the entry followed by "-", or
	// when its image name (last path element, without tag) equals an entry.
	Deny []string
	// Allow lists names that are always application containers.
	Allow []string
	// IncludeInit treats init containers as application containers.
	IncludeInit bool
}

// IsApp reports whether c is an application container of workload.
func (f ContainerFilter) IsApp(c Container, workload string) bool {
	if slices.Contains(f.Allow, c.Name) {
		return true
	}
	if c.Init && !f.IncludeInit {
		return false
	}
	if c.Name == workload {
		return true
	}
	img := imageName(c.Image)
	for _, d := range f.Deny {
		if c.Name == d || strings.HasPrefix(c.Name, d+"-") || img == d {
			return false
		}
	}
	return true
}

// AppContainers returns the application containers of p.
func (f ContainerFilter) AppContainers(p Pod) []Container {
	var out []Container
	for _, c := range p.Containers {
		if f.IsApp(c, p.OwnerName) {
			out = append(out, c)
		}
	}
	return out
}

// PrimaryApp returns the main application container of p: the one named
// like its workload, else the first application container.
func (f ContainerFilter) PrimaryApp(p Pod) (Container, bool) {
	apps := f.AppContainers(p)
	for _, c := range apps {
		if c.Name == p.OwnerName {
			return c, true
		}
	}
	if len(apps) > 0 {
		return apps[0], true
	}
	return Container{}, false
}

// imageName returns "proxyv2" for "docker.io/istio/proxyv2:1.24@sha256:…".
func imageName(image string) string {
	image, _, _ = strings.Cut(image, "@")
	if i := strings.LastIndex(image, "/"); i >= 0 {
		image = image[i+1:]
	}
	name, _, _ := strings.Cut(image, ":")
	return name
}

// ImageTag returns the tag of an image reference ("v2.14.3"), the short
// digest when pinned by digest ("sha256:1a2b3c4"), or "latest".
func ImageTag(image string) string {
	if _, digest, ok := strings.Cut(image, "@"); ok {
		return digest[:min(len(digest), len("sha256:")+7)]
	}
	last := image[strings.LastIndex(image, "/")+1:]
	if _, tag, ok := strings.Cut(last, ":"); ok {
		return tag
	}
	return "latest"
}
