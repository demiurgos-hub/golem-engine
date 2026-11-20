package main

import "embed"

// embeddedWeb holds the UI built by `npm run build` in golem-inspector/, which
// writes into web/dist. Only web/dist/.gitkeep is committed; release builds
// run the UI build before `go build` (see scripts/build-golem-inspector.*).
//
//go:embed all:web/dist
var embeddedWeb embed.FS

// placeholderWeb is served when the binary was built without the UI.
//
//go:embed placeholder/index.html
var placeholderWeb embed.FS
