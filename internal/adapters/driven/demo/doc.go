// Package demo is a synthetic, deterministic cluster implementing
// ports.ClusterClient and ports.LogSource, used by `huginn --demo`, by
// tests and for development without a cluster or credentials.
//
// It models one cluster with a namespace per environment (configurable),
// the repositories of the UX prototype, application pods with Istio and
// Vault sidecars and init containers, crash loops with previous-container
// logs, an OOM kill, an image pull failure, a pending pod, rollouts in
// progress and one rollout that happens live during the session. Log lines
// are logstash-logback JSON enriched with Kubernetes metadata (the shape
// real Spring Boot services emit), plus plain lines such as the Spring
// banner, and multi-line stack traces.
//
// Given the same seed and clock, it produces the same cluster and lines.
package demo
