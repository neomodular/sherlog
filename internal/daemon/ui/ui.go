// Package ui holds the embedded Case Board browser assets and serves them on the
// daemon's listener (case-board-ui spec; design D1). Everything the UI needs —
// HTML, CSS, JS, and the mascot SVG — is compiled into the binary with go:embed,
// so the single binary stays single and the page issues zero external network
// requests.
package ui

import (
	"embed"
	"io/fs"
	"net/http"
)

// assets carries the static Case Board files. The directory is plain in-repo
// files (design risk note: no versioning beyond the binary), embedded at build so
// the daemon serves them with no filesystem dependency at runtime.
//
//go:embed assets
var assets embed.FS

// Handler returns the GET-only file server for the Case Board, rooted at the
// embedded assets directory and served at the daemon's "/" (design D2: the UI is
// read-only — a file server only ever reads). A request for a path with no file
// (the SPA's hash routes live client-side) falls back to index.html so a deep
// link or reload lands on the app rather than a 404.
func Handler() http.Handler {
	sub, err := fs.Sub(assets, "assets")
	if err != nil {
		// The embed path is a compile-time constant; a failure here is a build bug,
		// not a runtime condition, so panicking surfaces it immediately in tests.
		panic("ui: embedded assets missing: " + err.Error())
	}
	files := http.FS(sub)
	server := http.FileServer(files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			// The Case Board is strictly read-only (design D2); reject any write verb.
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		// Serve real files directly; route everything else to index.html so the
		// vanilla-JS hash router (design D7) owns navigation. Probes/health/api live
		// under their own prefixes in the daemon mux, so this handler only ever sees
		// UI paths.
		if r.URL.Path != "/" {
			if f, err := files.Open(r.URL.Path); err == nil {
				_ = f.Close()
				server.ServeHTTP(w, r)
				return
			}
			r.URL.Path = "/"
		}
		server.ServeHTTP(w, r)
	})
}
