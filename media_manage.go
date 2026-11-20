package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// allowedMediaPath 判断路径是否位于任一媒体根目录内（防越界删除）。
func (a *App) allowedMediaPath(full string) bool {
	clean := filepath.Clean(full)
	for _, root := range a.cfg.MediaRoots {
		r := filepath.Clean(root)
		if clean == r || strings.HasPrefix(clean, r+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

// handleItemPut PUT /Items/{id}  {Name}：设置自定义显示名（跨扫描持久化）。
func (a *App) handleItemPut(w http.ResponseWriter, r *http.Request, u *User, id string) {
	if !u.IsAdmin {
		writeErr(w, http.StatusForbidden, "需要管理员权限")
		return
	}
	var b struct {
		Name string `json:"Name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体无效")
		return
	}
	b.Name = strings.TrimSpace(b.Name)
	if b.Name == "" || len([]rune(b.Name)) > 256 {
		writeErr(w, http.StatusBadRequest, "名称长度必须为 1–256 字")
		return
	}
	a.store.mu.Lock()
	it, ok := a.store.Items[id]
	if !ok {
		a.store.mu.Unlock()
		writeErr(w, http.StatusNotFound, "媒体不存在")
		return
	}
	a.store.DisplayNames[id] = b.Name
	it.Name = b.Name
	a.store.mu.Unlock()
	go a.store.Save()
	a.store.addActivity("修改名称", "将 "+id+" 重命名为 "+b.Name, "MediaRename", "Information")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "Id": id, "Name": b.Name})
}

// handleItemDelete DELETE /Items/{id}[?DeleteDirectory=true]：删除媒体源/STRM/目录。
func (a *App) handleItemDelete(w http.ResponseWriter, r *http.Request, u *User, id string) {
	if !u.IsAdmin {
		writeErr(w, http.StatusForbidden, "需要管理员权限")
		return
	}
	it := a.getItem(id)
	if it == nil {
		writeErr(w, http.StatusNotFound, "媒体不存在")
		return
	}
	delDir := boolParam(r, "DeleteDirectory")

	var targets []string
	if it.Type == "Movie" || it.Type == "Episode" {
		if it.SourcePath != "" {
			if delDir {
				targets = append(targets, filepath.Dir(it.SourcePath))
			} else {
				targets = append(targets, it.SourcePath)
			}
		}
	} else if it.Type == "Series" || it.Type == "Season" {
		for _, d := range a.descendants(id) {
			if d.SourcePath != "" {
				if delDir {
					targets = append(targets, filepath.Dir(d.SourcePath))
				} else {
					targets = append(targets, d.SourcePath)
				}
			}
		}
	}

	seen := map[string]bool{}
	uniq := targets[:0]
	for _, t := range targets {
		c := filepath.Clean(t)
		if !seen[c] {
			seen[c] = true
			uniq = append(uniq, c)
		}
	}
	targets = uniq

	// 安全校验：必须在媒体目录内，且不得为媒体库根
	for _, t := range targets {
		if !a.allowedMediaPath(t) {
			writeErr(w, http.StatusBadRequest, "路径无效或超出媒体目录")
			return
		}
		if a.isLibraryRoot(t) {
			writeErr(w, http.StatusBadRequest, "不能删除媒体库根目录")
			return
		}
	}

	for _, t := range targets {
		var err error
		if delDir {
			err = os.RemoveAll(t)
		} else {
			err = os.Remove(t)
		}
		if err != nil && !os.IsNotExist(err) {
			writeErr(w, http.StatusInternalServerError, "删除失败: "+err.Error())
			return
		}
	}

	// 从索引移除相关条目
	removeIDs := []string{id}
	if it.Type == "Series" || it.Type == "Season" {
		for _, d := range a.descendants(id) {
			removeIDs = append(removeIDs, d.Id)
		}
	}
	a.store.mu.Lock()
	for _, rid := range removeIDs {
		delete(a.store.Items, rid)
		delete(a.store.DisplayNames, rid)
	}
	a.store.mu.Unlock()
	a.store.rebuildOrder()
	go a.store.Save()
	a.store.addActivity("删除媒体", "删除 "+it.Name, "MediaDeleted", "Information")
	// 触发该库重扫，最终以磁盘为准
	if it.LibraryId != "" {
		a.enqueueScan(it.LibraryId)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": len(targets)})
}
