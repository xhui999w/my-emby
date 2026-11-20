package main

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// handleItemImageUpload POST /Items/{id}/Images/{type}，raw body 为图片字节。
func (a *App) handleItemImageUpload(w http.ResponseWriter, r *http.Request, u *User, itemID, imageType string) {
	if !u.IsAdmin {
		writeErr(w, http.StatusForbidden, "需要管理员权限")
		return
	}
	it := a.getItem(itemID)
	if it == nil {
		writeErr(w, http.StatusNotFound, "条目不存在")
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 20<<20))
	if err != nil || len(data) == 0 {
		writeErr(w, http.StatusBadRequest, "缺少图片数据（请以图片字节作为 body 上传）")
		return
	}
	ext := ".jpg"
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	switch {
	case strings.Contains(ct, "png"):
		ext = ".png"
	case strings.Contains(ct, "webp"):
		ext = ".webp"
	case strings.Contains(ct, "gif"):
		ext = ".gif"
	}
	dir := filepath.Join(a.cfg.DataDir, "images")
	if err := ensureDir(dir); err != nil {
		writeErr(w, http.StatusInternalServerError, "图片目录不可写")
		return
	}
	typ := strings.ToLower(imageType)
	if typ == "" {
		typ = "primary"
	}
	p := filepath.Join(dir, "upload-"+itemID+"-"+typ+ext)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		writeErr(w, http.StatusInternalServerError, "保存图片失败")
		return
	}
	a.store.mu.Lock()
	if typ == "backdrop" || typ == "background" {
		it.BackdropPath = p
		it.HasBackdrop = true
		it.BackdropImageTags = []string{h16(p)}
	} else {
		it.PosterPath = p
		it.HasPoster = true
		it.ImageTags = map[string]string{"Primary": h16(p)}
	}
	a.store.mu.Unlock()
	go a.store.Save()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "type": typ, "size": len(data)})
}
