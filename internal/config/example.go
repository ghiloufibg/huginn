package config

import _ "embed"

//go:generate go run ./internal/genschema -out ../../docs/config.schema.json -example ../../examples/config.yaml

// Example is the documented sample configuration printed by
// `huginn config example`.
//
//go:embed example.yaml
var Example []byte
