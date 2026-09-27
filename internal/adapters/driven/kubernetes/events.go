package kubernetes

import (
	"context"
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// PodEvents reads the events of one pod on demand (the services preview
// asks when the cursor rests on a service), newest first.
func (c *Client) PodEvents(ctx context.Context, scope ports.Scope, namespace, pod string) ([]domain.Event, error) {
	cs, err := c.clientset(scope.Context)
	if err != nil {
		return nil, err
	}
	sel := fields.Set{"involvedObject.kind": "Pod", "involvedObject.name": pod}.AsSelector().String()
	list, err := cs.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{FieldSelector: sel})
	if err != nil {
		return nil, namespaced(namespace, err)
	}
	out := make([]domain.Event, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, toEvent(&list.Items[i]))
	}
	return merge(out), nil
}

// merge folds the events that say the same thing (the kubelet starts new
// series after a node restart) and sorts them newest first.
func merge(evs []domain.Event) []domain.Event {
	type key struct{ typ, reason, message string }
	at := map[key]int{}
	out := evs[:0]
	for _, e := range evs {
		k := key{e.Type, e.Reason, e.Message}
		i, seen := at[k]
		if !seen {
			at[k] = len(out)
			out = append(out, e)
			continue
		}
		m := &out[i]
		m.Count += e.Count
		if e.LastSeen.After(m.LastSeen) {
			m.LastSeen = e.LastSeen
		}
		if !e.FirstSeen.IsZero() && e.FirstSeen.Before(m.FirstSeen) {
			m.FirstSeen = e.FirstSeen
		}
	}
	slices.SortStableFunc(out, func(a, b domain.Event) int { return b.LastSeen.Compare(a.LastSeen) })
	return out
}
