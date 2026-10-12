package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"

	"github.com/foomo/dockprox/pkg/config"
	"github.com/invopop/jsonschema"
)

func main() {
	out := flag.String("out", "dockprox.schema.json", "output path")

	flag.Parse()

	r := &jsonschema.Reflector{
		Anonymous:                 true,
		AllowAdditionalProperties: false,
	}
	schema := r.Reflect(&config.Config{})

	buf, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		log.Fatalf("marshal: %v", err)
	}

	if err := os.WriteFile(*out, append(buf, '\n'), 0o644); err != nil { //nolint:gosec
		log.Fatalf("write: %v", err)
	}
}
