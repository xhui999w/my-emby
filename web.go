package main

import (
	_ "embed"
	"net/http"
)

//go:embed web/index.html
var webIndexHTML []byte

func (a *App) handleWeb(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(webIndexHTML)
}
