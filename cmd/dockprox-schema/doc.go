// Command dockprox-schema generates the JSON Schema for the dockprox YAML
// configuration. The output is consumed by editors (via
// yaml-language-server) and rendered in the VitePress docs.
//
// Usage:
//
//	dockprox-schema [-out PATH]
//
// The -out flag sets the output file (default dockprox.schema.json).
package main
