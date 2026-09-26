// Command genschema writes the JSON Schema of Huginn's configuration and a
// copy of the example configuration for people browsing the repository.
package main

import (
	"flag"
	"log"
	"os"

	"github.com/ghiloufibg/huginn/internal/config"
)

func main() {
	out := flag.String("out", "config.schema.json", "schema output file")
	example := flag.String("example", "", "example config output file (optional)")
	flag.Parse()
	b, err := config.Schema()
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*out, append(b, '\n'), 0o644); err != nil {
		log.Fatal(err)
	}
	if *example != "" {
		if err := os.WriteFile(*example, config.Example, 0o644); err != nil {
			log.Fatal(err)
		}
	}
}
