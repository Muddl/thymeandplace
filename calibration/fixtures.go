// Package calibfixtures embeds the published-guide calibration fixtures.
package calibfixtures

import (
	"embed"
	"io/fs"
)

//go:embed fixtures
var files embed.FS

// FS returns the fixtures directory as a filesystem rooted at fixtures/.
func FS() fs.FS {
	sub, err := fs.Sub(files, "fixtures")
	if err != nil {
		panic(err) // the directory is embedded at build time
	}
	return sub
}
