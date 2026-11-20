package main

import (
	"fmt"
	"net/http"
	"strings"
)

// statusWriter 包装 ResponseWriter，捕获状态码与错误响应体片段用于错误日志。
type statusWriter struct {
	http.ResponseWriter
	status int
	detail strings.Builder
	wrote  bool
}

func (w *statusWriter) WriteHeader(s int) {
	if w.wrote {
		return
	}
	w.status = s
	w.wrote = true
	w.ResponseWriter.WriteHeader(s)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	if w.status >= 400 && w.detail.Len() < 2048 {
		n := 2048 - w.detail.Len()
		if n > len(b) {
			n = len(b)
		}
		w.detail.Write(b[:n])
	}
	return w.ResponseWriter.Write(b)
}

// recordHTTPError 记录一次 4xx/5xx 请求。
func (a *App) recordHTTPError(r *http.Request, status int, detail string) {
	name := r.Method + " " + r.URL.Path
	overview := fmt.Sprintf("%d %s", status, strings.TrimSpace(detail))
	if len([]rune(overview)) > 300 {
		overview = string([]rune(overview)[:300])
	}
	a.store.addActivity(name, overview, "error", "Error")
}

// recordRedirect 记录播放跳转（.strm 等）。
func (a *App) recordRedirect(u *User, it *Item, url string) {
	overview := ""
	if u != nil {
		overview = u.Name + " -> "
	}
	if it != nil {
		overview += it.Name + " -> "
	}
	overview += url
	a.store.addActivity("播放跳转", overview, "PlaybackRedirect", "Information")
	if a.store.settingBool("proxy_debug", false) {
		a.store.addActivity("代理调试", "redirect: "+url, "ProxyDebug", "Information")
	}
}

// initials 提取标题首字母（按空格/点/下划线/连字符分词，取每词首字符，小写）。
func initials(name string) string {
	var b strings.Builder
	prevSep := true
	for _, r := range name {
		isSep := r == ' ' || r == '.' || r == '_' || r == '-' || r == ':' || r == '(' || r == '['
		if isSep {
			prevSep = true
			continue
		}
		if prevSep {
			b.WriteRune(toLowerRune(r))
			prevSep = false
		}
	}
	return b.String()
}

func toLowerRune(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + ('a' - 'A')
	}
	return r
}
