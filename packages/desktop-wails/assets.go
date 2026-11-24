package main

import (
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func frontendHandler(assets fs.FS, budgetFiles http.Handler) http.Handler {
	bundled := application.BundledAssetFileServer(assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, fsAPIPrefix) {
			budgetFiles.ServeHTTP(w, r)
			return
		}
		// React Router uses real paths. Wails' file server doesn't fall back
		// to index.html, so a full reload on /budget would otherwise be blank.
		// Keep runtime and asset failures as 404s rather than returning HTML.
		assetPath := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		root, _, _ := strings.Cut(assetPath, "/")
		isAsset := root == "wails" || root == "static" || root == "kcab" ||
			root == "data" || root == "service-worker"
		if (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
			!isAsset && path.Ext(assetPath) == "" {
			if _, err := fs.Stat(assets, assetPath); err != nil {
				r = r.Clone(r.Context())
				r.URL.Path = "/index.html"
			}
		}
		bundled.ServeHTTP(w, r)
	})
}
