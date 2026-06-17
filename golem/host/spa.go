package host

import (
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
	"strings"
)

// SPAOptions configures SPAHandler.
type SPAOptions struct {
	APIHandler  http.Handler
	APIPrefix   string
	EmbeddedFS  fs.FS
	DiskDir     string
	Index       string
	DiskLogName string
}

// SPAHandler serves an API prefix plus a single-page app from disk or an fs.FS.
// DiskDir wins when it exists, which lets local builds override embedded assets.
func SPAHandler(opts SPAOptions) http.Handler {
	if opts.APIPrefix == "" {
		opts.APIPrefix = "/api"
	}
	if opts.Index == "" {
		opts.Index = "index.html"
	}
	docRoot := opts.EmbeddedFS
	if opts.DiskDir != "" {
		if fi, err := os.Stat(opts.DiskDir); err == nil && fi.IsDir() {
			if opts.DiskLogName == "" {
				opts.DiskLogName = opts.DiskDir
			}
			log.Printf("golem/host: serving static files from disk: %s", opts.DiskLogName)
			docRoot = os.DirFS(opts.DiskDir)
		}
	}
	if docRoot == nil {
		return opts.APIHandler
	}
	return spaOverFS(opts.APIHandler, opts.APIPrefix, docRoot, opts.Index)
}

func spaOverFS(apiHandler http.Handler, apiPrefix string, docRoot fs.FS, index string) http.Handler {
	fileSrv := http.FileServer(http.FS(docRoot))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if apiHandler != nil && strings.HasPrefix(r.URL.Path, apiPrefix) {
			apiHandler.ServeHTTP(w, r)
			return
		}

		openName := normalizeURLPathForFS(r.URL.Path)
		fi, statErr := fs.Stat(docRoot, openName)
		if statErr == nil && fi.IsDir() {
			idxPath := path.Join(openName, index)
			if idxFI, ierr := fs.Stat(docRoot, idxPath); ierr == nil && !idxFI.IsDir() {
				http.ServeFileFS(w, r, docRoot, idxPath)
				return
			}
		}
		if statErr == nil {
			fileSrv.ServeHTTP(w, r)
			return
		}

		http.ServeFileFS(w, r, docRoot, index)
	})
}

func normalizeURLPathForFS(urlPath string) string {
	p := path.Clean("/" + urlPath)
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return "."
	}
	return p
}
