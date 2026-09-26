// Package domain holds Huginn's pure business types and rules: environments,
// repositories and their workloads, pods and containers, log entries, log
// levels and time windows.
//
// It depends on the standard library only. Anything specific to a cluster,
// a log format or a company convention lives in adapters, never here.
package domain
