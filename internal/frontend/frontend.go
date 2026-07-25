// Package frontend serves the built single-page application.
//
// Vite writes its output into this package's dist/ directory so it can be
// embedded into the binary — that is what makes manualbox a single file to
// deploy, with no separate web server or static-asset volume to configure.
//
// dist/ contains only a committed .gitkeep until the SPA is built. The embed
// directive uses the all: prefix so that placeholder counts, which keeps
// `go build ./...` working on a fresh clone before anyone has run npm.
package frontend

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var embedded embed.FS

// notBuiltPage is served when the binary was compiled without a built SPA. It
// says so plainly rather than returning a blank page or a 404, because "I
// visited the URL and got nothing" is a miserable thing to debug.
const notBuiltPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>manualbox — UI not built</title>
<style>
  body { font: 16px/1.6 system-ui, sans-serif; max-width: 34rem; margin: 15vh auto; padding: 0 1.5rem; color: #1a1a1a; }
  code { background: #f2f0eb; padding: .15em .4em; border-radius: 3px; font-size: .9em; }
  .hint { color: #666; font-size: .9em; margin-top: 2rem; }
</style></head>
<body>
  <h1>manualbox</h1>
  <p>The API is running, but this binary was built without the web interface.</p>
  <p>Build it with <code>make build</code>, which compiles the SPA into the binary.
     For frontend development run <code>make dev</code> and use the Vite dev server instead.</p>
  <p class="hint">The API itself is available under <code>/api/v1</code>.</p>
</body></html>`

// FS returns the embedded SPA filesystem rooted at dist, and whether a real
// build is present.
func FS() (fs.FS, bool) {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		return nil, false
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return sub, false
	}
	return sub, true
}

// Handler serves the SPA with history fallback: any path that is not a real file
// returns index.html so client-side routes such as /devices/dev_123 work on a
// hard refresh.
//
// Requests for assets that genuinely do not exist still 404, rather than being
// answered with HTML — a bundle referencing a missing chunk should fail loudly
// instead of silently receiving a page.
func Handler() http.Handler {
	files, built := FS()
	if !built {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Behave like the built handler for asset requests: a request for a
			// .js or .css file has no business receiving an HTML page, and
			// answering 200 would make a missing-bundle bug look like a working
			// server serving the wrong content.
			if ext := path.Ext(path.Clean(r.URL.Path)); ext != "" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(notBuiltPage))
		})
	}

	fileServer := http.FileServer(http.FS(files))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upath := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if upath == "" {
			upath = "index.html"
		}

		if _, err := fs.Stat(files, upath); err == nil {
			// Hashed asset filenames are immutable, so they can be cached hard.
			// index.html must not be, or a deploy never reaches the browser.
			if upath != "index.html" && strings.HasPrefix(upath, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			fileServer.ServeHTTP(w, r)
			return
		}

		// A missing file with an extension is a genuine 404; anything else is a
		// client-side route.
		if ext := path.Ext(upath); ext != "" {
			http.NotFound(w, r)
			return
		}

		index, err := fs.ReadFile(files, "index.html")
		if err != nil {
			http.Error(w, "index.html missing from the embedded build", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}
