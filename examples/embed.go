// Package examples embeds the example config folders. examples/config is
// also the folder used by --demo, so the demo needs nothing the user could
// not write (docs/DECISIONS.md D-030).
package examples

import (
	"embed"
	"io/fs"
)

//go:embed config
var folders embed.FS

// Demo returns the config folder used by --demo.
func Demo() fs.FS {
	sub, err := fs.Sub(folders, "config")
	if err != nil {
		panic(err) // embedded at build time: cannot happen
	}
	return sub
}
