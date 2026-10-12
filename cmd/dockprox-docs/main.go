package main

import (
	"flag"
	"log"
	"os"

	"github.com/foomo/dockprox/internal/cli"
	"github.com/spf13/cobra/doc"
)

func main() {
	out := flag.String("out", "docs/reference/cli", "output directory")

	flag.Parse()

	if err := os.MkdirAll(*out, 0o755); err != nil {
		log.Fatalf("mkdir: %v", err)
	}

	root := cli.NewRoot()

	root.DisableAutoGenTag = true
	if err := doc.GenMarkdownTree(root, *out); err != nil {
		log.Fatalf("gen: %v", err)
	}
}
