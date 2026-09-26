// Package tui is the terminal UI driving adapter, built with Bubble Tea v2
// and Lip Gloss v2. It talks to the core only through driving ports and
// never touches Kubernetes directly.
//
// Layout (see docs/DECISIONS.md D-020): one header line, the body, one
// status line. No icons: states are words, colors reinforce them.
package tui
