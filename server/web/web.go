// Package web holds the browser client: static files, no build step, served by
// the API server at / so one port carries the TV API and the web app.
package web

import "embed"

// Static is the web app. index.html is the entry point; everything else is
// loaded from it with relative paths.
//
//go:embed static
var Static embed.FS
