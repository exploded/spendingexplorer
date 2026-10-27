// Package web embeds the templates and static assets so the binary deploys alone.
package web

import (
	"embed"
	"io/fs"
)

//go:embed templates static
var files embed.FS

// Templates holds layout.html, partials.html and one file per page.
var Templates, _ = fs.Sub(files, "templates")

// Static holds CSS, JS and icons served under /static/.
var Static, _ = fs.Sub(files, "static")
