package main

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed webui
var webAssets embed.FS

func webAssetsDir() fs.FS {
	sub, err := fs.Sub(webAssets, "webui")
	if err != nil {
		Log.Error("reading the embedded settings page: %v", err)
		return webAssets
	}
	return sub
}

func serveAsset(w http.ResponseWriter, r *http.Request, name, contentType string) {
	data, err := fs.ReadFile(webAssetsDir(), name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}
