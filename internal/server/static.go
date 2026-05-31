package server

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:static
var staticFS embed.FS

// staticHandler serves the embedded Web UI. It strips the "/" prefix and
// falls back to index.html for SPA routing.
func (s *Server) staticHandler() http.Handler {
	// Try to open the embedded static directory. If it doesn't exist (e.g.
	// during development before the UI is built), return a no-op handler.
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				w.Header().Set("Content-Type", "text/html")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(indexHTMLFallback))
				return
			}
			http.NotFound(w, r)
		})
	}

	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// API routes should never reach here (mux routes them first), but
		// guard defensively so a missing API route doesn't fall through to
		// the SPA fallback.
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}

		// Clean the path and try to open the file.
		cleanPath := path.Clean(r.URL.Path)
		if cleanPath == "/" {
			cleanPath = "/index.html"
		}

		_, err := sub.Open(strings.TrimPrefix(cleanPath, "/"))
		if err != nil {
			// File not found — serve index.html for SPA routing.
			cleanPath = "/index.html"
		}

		r.URL.Path = cleanPath
		fileServer.ServeHTTP(w, r)
	})
}

// indexHTMLFallback is a minimal placeholder served when the embedded static/
// directory is absent. It lets the server start and respond to / even before
// the Web UI assets are built.
const indexHTMLFallback = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<title>Octo Agent</title>
<style>
body{font-family:system-ui,-apple-system,sans-serif;max-width:800px;margin:40px auto;padding:0 20px;line-height:1.6}
h1{color:#333}code{background:#f4f4f4;padding:2px 6px;border-radius:3px}
</style>
</head>
<body>
<h1>🐙 Octo Agent Server</h1>
<p>The Web UI assets are not embedded yet. Build them into <code>internal/server/static/</code> and recompile.</p>
<p>API endpoints available:</p>
<ul>
<li><code>POST /api/chat</code> — create a new session and send a message</li>
<li><code>POST /api/chat/:id/turn</code> — send a message to an existing session</li>
<li><code>GET  /api/sessions</code> — list recent sessions</li>
<li><code>GET  /api/sessions/:id</code> — get session details</li>
<li><code>GET  /api/tools</code> — list available tools</li>
<li><code>GET  /api/skills</code> — list available skills</li>
<li><code>GET  /api/health</code> — health check</li>
</ul>
</body>
</html>
`
