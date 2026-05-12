// Package config defines the dockprox configuration schema and provides
// Load, which reads YAML from a file or io.Reader and validates it.
//
// Use jsonschema struct tags to keep the generated schema
// (./dockprox.schema.json) in sync with the Go types.
package config

//go:generate go run ../../cmd/dockprox-schema --out ../../dockprox.schema.json
