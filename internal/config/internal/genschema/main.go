// Command genschema writes the JSON Schemas of the config folder files, one
// per file kind, for editor completion (docs/CONFIG.md).
package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"

	"github.com/ghiloufibg/huginn/internal/config"
)

func main() {
	out := flag.String("out", "schema", "output folder")
	flag.Parse()
	schemas, err := config.Schemas()
	if err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		log.Fatal(err)
	}
	for name, b := range schemas {
		if err := os.WriteFile(filepath.Join(*out, name), b, 0o644); err != nil {
			log.Fatal(err)
		}
	}
}
