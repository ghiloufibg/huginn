package domain

import (
	"slices"
	"strings"
)

// KindPod is the kind of a standalone group made of one bare pod.
const KindPod WorkloadKind = "Pod"

// Owns reports whether pod p belongs to workload w. A standalone group
// owns the pods it was made of (same owner, or the bare pod itself); a
// workload owns the pods naming it as owner and, among pods without an
// owner, those its selector matches.
func (w Workload) Owns(p Pod) bool {
	if p.Namespace != w.Ref.Namespace {
		return false
	}
	if w.Standalone {
		kind, name := standaloneKey(p)
		return kind == w.Ref.Kind && name == w.Ref.Name
	}
	sel := w.Selector
	if p.OwnerName == "" {
		return len(sel) > 0 && selectorMatches(sel, p.Labels)
	}
	if p.OwnerName != w.Ref.Name || (p.OwnerKind != "" && WorkloadKind(p.OwnerKind) != w.Ref.Kind) {
		return false
	}
	return len(sel) == 0 || selectorMatches(sel, p.Labels)
}

func selectorMatches(sel, labels map[string]string) bool {
	for k, v := range sel {
		if labels[k] != v {
			return false
		}
	}
	return true
}

// standaloneKey is the group of a pod no workload claims: its owner (kind
// and name), else the pod itself.
func standaloneKey(p Pod) (WorkloadKind, string) {
	if p.OwnerName == "" {
		return KindPod, p.Name
	}
	kind := WorkloadKind(p.OwnerKind)
	if kind == "" {
		kind = "Owner"
	}
	return kind, p.OwnerName
}

// StandaloneWorkloads groups the pods that none of ws owns into synthetic
// workloads (docs/DECISIONS.md D-038): a bare pod, a Job made by hand, a
// ReplicaSet whose Deployment is gone, pods of a controller Huginn does
// not know. They carry the labels of their oldest pod, so the repository
// resolvers can attribute them like any workload.
func StandaloneWorkloads(ws []Workload, pods []Pod) []Workload {
	type key struct {
		ns   string
		kind WorkloadKind
		name string
	}
	groups := map[key]*Workload{}
	var order []key
	for _, p := range pods {
		if slices.ContainsFunc(ws, func(w Workload) bool { return w.Owns(p) }) {
			continue
		}
		kind, name := standaloneKey(p)
		k := key{p.Namespace, kind, name}
		g, ok := groups[k]
		if !ok {
			g = &Workload{Ref: WorkloadRef{Env: p.Env, Namespace: p.Namespace, Kind: kind, Name: name}, Standalone: true, Labels: p.Labels, Created: p.Created}
			groups[k] = g
			order = append(order, k)
		}
		if p.Created.Before(g.Created) {
			g.Created, g.Labels = p.Created, p.Labels
		}
		if p.Deleted || p.Phase == PodSucceeded {
			continue
		}
		g.DesiredReplicas++
		g.UpdatedReplicas++
		if podReady(p) {
			g.ReadyReplicas++
		}
	}
	slices.SortFunc(order, func(a, b key) int {
		return strings.Compare(a.ns+"/"+a.name, b.ns+"/"+b.name)
	})
	out := make([]Workload, 0, len(order))
	for _, k := range order {
		out = append(out, *groups[k])
	}
	return out
}

// podReady reports whether every regular container of p is ready.
func podReady(p Pod) bool {
	n := 0
	for _, c := range p.Containers {
		if c.Init {
			continue
		}
		if !c.Ready {
			return false
		}
		n++
	}
	return n > 0
}
