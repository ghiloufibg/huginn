// Package logformat implements ports.LogDecoder for JSON lines described
// by a format profile, for text lines read with a regular expression, and
// for plain text; Selector picks the decoder of each container from the
// formats' match rules.
//
// Nothing here knows a company's field names or layouts: they come from the
// config folder (formats/<name>.yaml, docs/CONFIG.md).
package logformat
