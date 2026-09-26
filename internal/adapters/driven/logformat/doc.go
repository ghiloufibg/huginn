// Package logformat implements ports.LogDecoder for structured JSON logs
// described by a format profile ("json-fields") and for plain text lines
// ("plain"), including the Spring Boot console layout.
//
// Nothing here knows a company's field names: they come from the profile.
package logformat
