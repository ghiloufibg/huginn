package domain

import "fmt"

// ContainerMode says which containers of a pod the logs follow.
type ContainerMode int

// Container modes (docs/DECISIONS.md D-038).
const (
	// ContainersApp follows the application containers (and an init
	// container blocking the pod): the common case when debugging.
	ContainersApp ContainerMode = iota
	// ContainersAll follows every container, sidecars and init containers
	// included.
	ContainersAll
)

// ContainerModeNames are the accepted names, in order.
var ContainerModeNames = []string{"app", "all"}

func (m ContainerMode) String() string { return ContainerModeNames[m] }

// ParseContainerMode reads "app" or "all" ("" is app).
func ParseContainerMode(s string) (ContainerMode, error) {
	switch s {
	case "", "app":
		return ContainersApp, nil
	case "all":
		return ContainersAll, nil
	}
	return ContainersApp, fmt.Errorf("unknown container mode %q (app or all)", s)
}

// ContainerRole is what a container is to its pod, for the logs screen.
type ContainerRole int

// Container roles.
const (
	RoleApp ContainerRole = iota
	RoleSidecar
	RoleInit
)

var roleNames = [...]string{"application", "sidecar", "init"}

func (r ContainerRole) String() string { return roleNames[r] }

// Role says whether c is an application container of p, a sidecar (matched
// by the filter's Deny list) or an init container.
func (f ContainerFilter) Role(p Pod, c Container) ContainerRole {
	switch {
	case f.IsApp(c, p.OwnerName):
		return RoleApp
	case c.Init:
		return RoleInit
	}
	return RoleSidecar
}

// StreamContainers returns the containers of p whose logs are followed in
// mode m.
func (f ContainerFilter) StreamContainers(p Pod, m ContainerMode) []Container {
	if m == ContainersAll {
		return p.Containers
	}
	return f.LogContainers(p)
}
