package main

import (
	_ "embed"
	"html"
	"net/http"
	"strings"
)

//go:embed web/index.html
var webIndexHTML []byte

func (a *App) handleWeb(w http.ResponseWriter, r *http.Request) {
	// 品牌跟随「服务器显示名称」设置，界面里的 {{SERVER_NAME}} 占位符替换
	name := html.EscapeString(a.effectiveServerName())
	out := strings.ReplaceAll(string(webIndexHTML), "{{SERVER_NAME}}", name)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(out))
}
