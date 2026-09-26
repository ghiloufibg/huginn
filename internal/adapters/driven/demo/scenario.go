package demo

import (
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// repoSpec describes one demo repository.
type repoSpec struct {
	name      string
	workloads []string
	replicas  int
	version   string
	age       time.Duration
	pkg       string // Java package used in logger names
	vault     bool   // has a vault-agent sidecar
}

// repos mirrors the UX prototype's services.
var repos = []repoSpec{
	{"payment-service", []string{"payment-service", "payment-worker"}, 2, "v2.14.3", 12 * day, "io.gimle.payment", true},
	{"notification-worker", []string{"notification-worker"}, 3, "v1.8.0", 3 * time.Hour, "io.gimle.notification", false},
	{"user-api", []string{"user-api", "user-api-admin", "user-api-sync"}, 2, "v4.2.1", 8 * day, "io.gimle.user", true},
	{"billing-gateway", []string{"billing-gateway", "billing-scheduler"}, 3, "v3.9.2", 47 * time.Minute, "io.gimle.billing", true},
	{"catalog-indexer", []string{"catalog-indexer"}, 2, "v1.3.7", 21 * time.Minute, "io.gimle.catalog", false},
	{"identity-provider", []string{"identity-provider", "identity-admin"}, 2, "v5.0.1", 31 * day, "io.gimle.identity", true},
	{"order-orchestrator", []string{"order-orchestrator", "order-saga"}, 3, "v2.7.4", time.Hour, "io.gimle.order", false},
	{"fraud-detector", []string{"fraud-detector"}, 3, "v0.19.6", 5 * day, "io.gimle.fraud", false},
	{"document-renderer", []string{"document-renderer"}, 1, "v1.11.0", 14 * time.Minute, "io.gimle.document", false},
	{"audit-stream", []string{"audit-stream", "audit-archiver"}, 2, "v2.4.8", 18 * day, "io.gimle.audit", false},
	{"search-api", []string{"search-api", "search-indexer"}, 3, "v3.1.2", 6 * day, "io.gimle.search", false},
	{"email-dispatcher", []string{"email-dispatcher"}, 2, "v1.6.9", 8 * time.Minute, "io.gimle.email", false},
	{"pricing-engine", []string{"pricing-engine", "pricing-cache"}, 2, "v2.12.0", 9 * day, "io.gimle.pricing", false},
	{"ledger-writer", []string{"ledger-writer"}, 2, "a41c9e2", 26 * time.Minute, "io.gimle.ledger", false},
}

const day = 24 * time.Hour

// condition is what goes wrong (or not) with a repo's primary workload in
// an environment.
type condition int

const (
	healthy condition = iota
	crashLoop
	oomKilled
	imagePull
	degraded
	pending
	rollingOut
	restartedOnce // healthy, one pod restarted after an OOM kill 2h ago
)

// conditions per environment; repos not listed are healthy.
var conditions = map[domain.Env]map[string]condition{
	domain.Env("rec"): {
		"catalog-indexer":     crashLoop,
		"order-orchestrator":  oomKilled,
		"document-renderer":   imagePull,
		"billing-gateway":     degraded,
		"email-dispatcher":    pending,
		"notification-worker": rollingOut,
		"ledger-writer":       rollingOut,
		"payment-service":     restartedOnce,
		"fraud-detector":      restartedOnce,
	},
	domain.Env("dev"): {
		"catalog-indexer": crashLoop,
		"search-api":      rollingOut,
	},
	domain.Env("prprd"): {
		"billing-gateway": rollingOut,
	},
	domain.Env("prd"): {
		"payment-service": restartedOnce,
	},
}

// liveRollout is the rollout played during the session: a new pod of the
// payment-service Deployment appears, becomes ready, and an old pod goes.
var liveRollout = struct {
	repo, workload, version string
	start, ready, retire    time.Duration
}{"payment-service", "payment-service", "v2.14.4", 90 * time.Second, 110 * time.Second, 130 * time.Second}
