package main

import (
	"mime"
	"net/http"
	"os"
	"path/filepath"
)

// handleStream 处理 /Videos/{id}/stream、/Audio/{id}/stream 与 /Items/{id}/Download。
// 采用 http.ServeContent 原生支持 HTTP Range（拖动进度条）。
func (a *App) handleStream(w http.ResponseWriter, r *http.Request, u *User, itemID string) {
	a.store.mu.RLock()
	it, ok := a.store.Items[itemID]
	a.store.mu.RUnlock()
	if !ok {
		writeErr(w, http.StatusNotFound, "条目不存在")
		return
	}
	// .strm 条目：直接 302 跳转到远端直链
	if it.StreamURL != "" {
		a.recordRedirect(u, it, it.StreamURL)
		http.Redirect(w, r, it.StreamURL, http.StatusFound)
		return
	}
	path := it.SourcePath
	if path == "" {
		path = it.Path
	}
	f, err := os.Open(path)
	if err != nil {
		writeErr(w, http.StatusNotFound, "媒体文件无法打开")
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		writeErr(w, http.StatusNotFound, "媒体文件不可用")
		return
	}
	ctype := mime.TypeByExtension(filepath.Ext(path))
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Accept-Ranges", "bytes")
	// 文件名用于下载
	if r.URL.Query().Get("Static") == "" {
		w.Header().Set("Content-Disposition", "inline; filename=\""+filepath.Base(path)+"\"")
	}
	http.ServeContent(w, r, filepath.Base(path), fi.ModTime(), f)
}

// recordPlayback 在开始播放时记一条活动（供后台日志展示）。
func (a *App) recordPlayback(u *User, it *Item) {
	a.store.addActivity("开始播放", u.Name+" 播放 "+it.Name, "PlaybackStart", "Information")
}
