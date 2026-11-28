package domain

import "testing"

func TestNameFilterMatches(t *testing.T) {
	f := NameFilter{Patterns: []string{"cloud-sql-proxy*", "*-db-proxy", "redis-tunnel", "[bad"}}
	for name, want := range map[string]bool{
		"cloud-sql-proxy": true, "cloud-sql-proxy-eu": true, "orders-db-proxy": true, "redis-tunnel": true,
		"redis-tunnel-2": false, "my-cloud-sql-proxy": false, "Redis-Tunnel": false, "payment-service": false, "": false,
	} {
		if got := f.Matches(name); got != want {
			t.Errorf("Matches(%q) = %v, want %v", name, got, want)
		}
	}
	if (NameFilter{}).Matches("anything") {
		t.Error("an empty filter matched")
	}
}

func TestNameFilterHidesPods(t *testing.T) {
	f := NameFilter{Patterns: []string{"proxy*"}}
	proxy := Workload{Ref: WorkloadRef{Namespace: "ns", Kind: KindDeployment, Name: "proxy-a"}, Selector: map[string]string{"app": "proxy"}}
	pods := map[string]struct {
		pod  Pod
		want bool
	}{
		"owned by a hidden deployment":  {Pod{Namespace: "ns", Name: "proxy-a-1", OwnerKind: "Deployment", OwnerName: "proxy-a"}, true},
		"owned by another":              {Pod{Namespace: "ns", Name: "api-1", OwnerKind: "Deployment", OwnerName: "api"}, false},
		"bare pod with a hidden name":   {Pod{Namespace: "ns", Name: "proxy-debug"}, true},
		"bare pod of a hidden selector": {Pod{Namespace: "ns", Name: "x", Labels: map[string]string{"app": "proxy"}}, true},
		"bare pod, nothing in common":   {Pod{Namespace: "ns", Name: "x", Labels: map[string]string{"app": "api"}}, false},
	}
	for name, tt := range pods {
		if got := f.Hides(tt.pod, []Workload{proxy}); got != tt.want {
			t.Errorf("%s: Hides = %v, want %v", name, got, tt.want)
		}
	}
}
