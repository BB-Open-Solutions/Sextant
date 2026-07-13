package web

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
)

// staticHandler serves the embedded assets with a content-hash ETag and
// revalidation. Assets live under a stable filename (e.g. the icon font), so
// without this a browser caches them indefinitely and never sees an update -
// a new font subset or stylesheet stays invisible until a manual hard-refresh.
// A per-file ETag lets the browser revalidate cheaply: unchanged files answer
// 304, a changed file is refetched on the next load.
func staticHandler() http.Handler {
	etags := map[string]string{}
	_ = fs.WalkDir(assets, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, err := assets.ReadFile(p)
		if err != nil {
			return nil
		}
		sum := sha256.Sum256(b)
		etags["/"+p] = `"` + hex.EncodeToString(sum[:16]) + `"`
		return nil
	})
	files := http.FileServerFS(assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if et, ok := etags[r.URL.Path]; ok {
			// no-cache = store, but revalidate before use. ServeContent (via
			// FileServerFS) honours If-None-Match against this ETag and
			// returns 304 when the content is unchanged.
			w.Header().Set("ETag", et)
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}
