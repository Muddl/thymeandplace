// Package static embeds the site's vendored and hand-written assets.
package static

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"sync"
)

//go:embed htmx.min.js app.css app.js admin.js admin.css favicon.svg
var FS embed.FS

// Version is a short hash of every embedded file's path and content. Asset
// URLs carry it, so a URL with the current version names these exact bytes
// and can be cached for a year. It is computed once, on first use.
var Version = sync.OnceValue(func() string { return hashFS(FS) })

// Asset is the versioned URL of an embedded file.
func Asset(name string) string { return "/static/" + name + "?v=" + Version() }

// hashFS returns a short hash of every file's path and content in fsys.
func hashFS(fsys fs.FS) string {
	h := sha256.New()
	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		h.Write([]byte(path))
		h.Write([]byte{0})
		h.Write(b)
		return nil
	})
	if err != nil { // embedded files can't fail to read; don't version on the impossible
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))[:10]
}
