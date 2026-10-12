// Package static embeds the site's vendored and hand-written assets.
package static

import "embed"

//go:embed htmx.min.js app.css app.js admin.js admin.css favicon.svg design
var FS embed.FS
