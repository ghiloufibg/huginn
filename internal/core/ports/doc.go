// Package ports declares the interfaces between Huginn's core and the outside
// world. Driven ports (ClusterClient, LogSource, LogDecoder, LogRenderer,
// ManifestScanner, RepoResolver, SecretsProvider, Clock) are implemented by
// adapters; driving ports (ServiceCatalog, LogSession, Diagnostics) are
// implemented by the core and called by the TUI and CLI.
//
// Interfaces are shaped by what the core needs and speak domain types only:
// no Kubernetes, UI or cloud SDK type may appear here.
package ports
