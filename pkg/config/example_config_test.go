package config_test

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/foomo/dockprox/pkg/config"
	"gopkg.in/yaml.v3"
)

const examplePath = "../../dockprox.example.yaml"

// TestExampleConfig_ValidAndComplete guards dockprox.example.yaml against
// drift: it must parse and validate as a real Config, and every yaml field
// reachable from Config must appear as a key somewhere in the example, so
// new fields can't ship undocumented.
func TestExampleConfig_ValidAndComplete(t *testing.T) {
	if _, err := config.LoadFile(examplePath); err != nil {
		t.Fatalf("LoadFile(%s): %v", examplePath, err)
	}

	buf, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatal(err)
	}

	var doc any
	if err := yaml.Unmarshal(buf, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	present := map[string]bool{}
	collectKeys(doc, present)

	for _, field := range yamlFields(reflect.TypeFor[config.Config]()) {
		if !present[field] {
			t.Errorf("field %q not documented in %s", field, examplePath)
		}
	}
}

// yamlFields returns every yaml struct-tag name reachable from t, recursing
// into nested structs, pointers, slices, and maps.
func yamlFields(t reflect.Type) []string {
	seen := map[reflect.Type]bool{}

	var fields []string

	var walk func(reflect.Type)

	walk = func(t reflect.Type) {
		switch t.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
			walk(t.Elem())
			return
		case reflect.Struct:
		default:
			return
		}

		if seen[t] {
			return
		}

		seen[t] = true

		for f := range t.Fields() {
			name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
			if name != "" && name != "-" {
				fields = append(fields, name)
			}

			walk(f.Type)
		}
	}
	walk(t)

	return fields
}

// collectKeys walks a generic YAML document (as decoded by yaml.Unmarshal
// into `any`) and records every map key encountered, at any depth.
func collectKeys(node any, out map[string]bool) {
	switch v := node.(type) {
	case map[string]any:
		for k, val := range v {
			out[k] = true
			collectKeys(val, out)
		}
	case []any:
		for _, val := range v {
			collectKeys(val, out)
		}
	}
}
