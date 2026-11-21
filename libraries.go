package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// orderedLibraries 按已保存的排序返回媒体库（未在顺序内的按名称排在最后）。
func (s *Store) orderedLibraries() []*Library {
	s.mu.RLock()
	libs := make([]*Library, 0, len(s.Libraries))
	for _, l := range s.Libraries {
		libs = append(libs, l)
	}
	raw := s.Settings["library_order"]
	s.mu.RUnlock()
	idx := map[string]*Library{}
	for _, l := range libs {
		idx[l.Id] = l
	}
	out := make([]*Library, 0, len(libs))
	used := map[string]bool{}
	if strings.TrimSpace(raw) != "" {
		for _, id := range strings.Split(raw, ",") {
			if l, ok := idx[id]; ok && !used[id] {
				out = append(out, l)
				used[id] = true
			}
		}
	}
	rest := make([]*Library, 0)
	for _, l := range libs {
		if !used[l.Id] {
			rest = append(rest, l)
		}
	}
	sort.Slice(rest, func(i, j int) bool { return rest[i].Name < rest[j].Name })
	return append(out, rest...)
}

// handleLibraries 媒体库管理：列表 / 新建 / 修改 / 删除 / 单库扫描 / 刷新全部 / 扫描并发。
// 路径前缀 /admin/libraries
func (a *App) handleLibraries(w http.ResponseWriter, r *http.Request, u *User) {
	seg := segs(r.URL.Path) // ["admin","libraries", ...]
	rest := []string{}
	if len(seg) > 2 {
		rest = seg[2:]
	}
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodGet:
			a.listLibraries(w)
		case http.MethodPost:
			a.createLibrary(w, r)
		default:
			writeErr(w, http.StatusMethodNotAllowed, "不支持的方法")
		}
		return
	}
	if rest[0] == "scan-all" && r.Method == http.MethodPost {
		a.enqueueScan("")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "已开始刷新全部媒体库"})
		return
	}
	if rest[0] == "concurrency" {
		if r.Method == http.MethodPut {
			var b struct{ Value int }
			_ = json.NewDecoder(r.Body).Decode(&b)
			if b.Value < 1 {
				b.Value = 1
			}
			if b.Value > 64 {
				b.Value = 64
			}
			a.store.setSetting("scan_concurrency", itoa(b.Value))
			go a.store.Save()
		}
		writeJSON(w, http.StatusOK, map[string]any{"ScanConcurrency": a.settingInt("scan_concurrency", 2)})
		return
	}
	if rest[0] == "order" {
		if r.Method == http.MethodPut {
			var b struct{ Ids []string }
			_ = json.NewDecoder(r.Body).Decode(&b)
			a.store.setSetting("library_order", strings.Join(b.Ids, ","))
			go a.store.Save()
			a.store.addActivity("媒体库排序", "更新媒体库显示顺序", "LibraryOrder", "Information")
		}
		ids := []string{}
		for _, l := range a.store.orderedLibraries() {
			ids = append(ids, l.Id)
		}
		writeJSON(w, http.StatusOK, map[string]any{"Ids": ids})
		return
	}
	id := rest[0]
	if len(rest) == 2 && rest[1] == "scan" && r.Method == http.MethodPost {
		if !a.libraryExists(id) {
			writeErr(w, http.StatusNotFound, "媒体库不存在")
			return
		}
		a.enqueueScan(id)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "已开始扫描该媒体库"})
		return
	}
	switch r.Method {
	case http.MethodPut:
		a.updateLibrary(w, r, id)
	case http.MethodDelete:
		a.deleteLibrary(w, id)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "不支持的方法")
	}
}

func (a *App) libraryExists(id string) bool {
	a.store.mu.RLock()
	defer a.store.mu.RUnlock()
	_, ok := a.store.Libraries[id]
	return ok
}

func (a *App) listLibraries(w http.ResponseWriter) {
	a.store.mu.RLock()
	type lc struct {
		movies, episodes, series int
		samples                  []string
	}
	counts := map[string]*lc{}
	for _, it := range a.store.Items {
		c := counts[it.LibraryId]
		if c == nil {
			c = &lc{}
			counts[it.LibraryId] = c
		}
		switch it.Type {
		case "Movie":
			c.movies++
		case "Episode":
			c.episodes++
		case "Series":
			c.series++
		}
		if (it.Type == "Movie" || it.Type == "Series") && len(c.samples) < 4 {
			c.samples = append(c.samples, it.Id)
		}
	}
	a.store.mu.RUnlock()
	libs := a.store.orderedLibraries()
	out := make([]map[string]any, 0, len(libs))
	for _, l := range libs {
		c := counts[l.Id]
		mv, ep, se := 0, 0, 0
		samples := []string{}
		if c != nil {
			mv, ep, se = c.movies, c.episodes, c.series
			samples = c.samples
		}
		out = append(out, map[string]any{
			"Id": l.Id, "Name": l.Name, "CollectionType": l.CollectionType, "Paths": l.Paths,
			"Movies": mv, "Episodes": ep, "Series": se, "Total": mv + ep,
			"SampleIds": samples, "Status": "ready",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"Items": out, "TotalRecordCount": len(out),
		"ScanConcurrency": a.settingInt("scan_concurrency", 2),
	})
}

// resolveMediaPath 把目录选择器返回的路径解析为真实存在的目录。
// 选择器路径相对媒体根（如 /影视库/华语电影 → /media/影视库/华语电影）；
// 依次尝试各媒体根，取第一个存在的；也兼容容器内绝对路径与相对路径。
func (a *App) resolveMediaPath(p string) (string, bool) {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	if p == "" {
		return "", false
	}
	isAbs := strings.HasPrefix(p, "/")
	if isAbs {
		rel := strings.TrimPrefix(p, "/")
		for _, root := range a.cfg.MediaRoots {
			cand := filepath.Join(root, filepath.FromSlash(rel))
			if fi, err := os.Stat(cand); err == nil && fi.IsDir() {
				return cand, true
			}
		}
		// 容器内绝对路径本身存在（如 /media）
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			return filepath.Clean(p), true
		}
		return "", false
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", false
	}
	if fi, err := os.Stat(abs); err == nil && fi.IsDir() {
		return abs, true
	}
	return "", false
}

func (a *App) createLibrary(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Name           string
		CollectionType string
		Type           string
		Paths          []string
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体无效")
		return
	}
	name := strings.TrimSpace(b.Name)
	if name == "" {
		writeErr(w, http.StatusBadRequest, "媒体库名称不能为空")
		return
	}
	ct := normalizeCollectionType(firstNonEmpty(b.CollectionType, b.Type))
	if len(b.Paths) == 0 {
		writeErr(w, http.StatusBadRequest, "请至少选择一个媒体目录")
		return
	}
	paths := make([]string, 0, len(b.Paths))
	for _, p := range b.Paths {
		resolved, ok := a.resolveMediaPath(p)
		if !ok {
			writeErr(w, http.StatusBadRequest, "目录不存在或不是文件夹: "+strings.TrimSpace(p))
			return
		}
		paths = append(paths, resolved)
	}
	if len(paths) == 0 {
		writeErr(w, http.StatusBadRequest, "请至少选择一个有效媒体目录")
		return
	}
	id := "lib-" + h16(name+"|"+strings.Join(paths, "|"))
	a.store.mu.Lock()
	if _, ok := a.store.Libraries[id]; ok {
		a.store.mu.Unlock()
		writeErr(w, http.StatusConflict, "同名同路径的媒体库已存在")
		return
	}
	a.store.Libraries[id] = &Library{Id: id, Name: name, CollectionType: ct, Paths: paths}
	a.store.mu.Unlock()
	go a.store.Save()
	a.enqueueScan(id)
	a.store.addActivity("新建媒体库", "创建媒体库 "+name, "LibraryCreated", "Information")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "Id": id})
}

func (a *App) updateLibrary(w http.ResponseWriter, r *http.Request, id string) {
	var b struct {
		Name           string
		CollectionType string
		Type           string
		Paths          []string
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体无效")
		return
	}
	a.store.mu.Lock()
	l, ok := a.store.Libraries[id]
	if !ok {
		a.store.mu.Unlock()
		writeErr(w, http.StatusNotFound, "媒体库不存在")
		return
	}
	if strings.TrimSpace(b.Name) != "" {
		l.Name = strings.TrimSpace(b.Name)
	}
	if ct := normalizeCollectionType(firstNonEmpty(b.CollectionType, b.Type)); ct != "" {
		l.CollectionType = ct
	}
	if len(b.Paths) > 0 {
		paths := make([]string, 0, len(b.Paths))
		for _, p := range b.Paths {
			resolved, ok := a.resolveMediaPath(p)
			if !ok {
				writeErr(w, http.StatusBadRequest, "目录不存在或不是文件夹: "+strings.TrimSpace(p))
				return
			}
			paths = append(paths, resolved)
		}
		if len(paths) > 0 {
			l.Paths = paths
		}
	}
	a.store.mu.Unlock()
	go a.store.Save()
	a.enqueueScan(id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) deleteLibrary(w http.ResponseWriter, id string) {
	a.store.mu.Lock()
	if _, ok := a.store.Libraries[id]; !ok {
		a.store.mu.Unlock()
		writeErr(w, http.StatusNotFound, "媒体库不存在")
		return
	}
	delete(a.store.Libraries, id)
	for iid, it := range a.store.Items {
		if it.LibraryId == id {
			delete(a.store.Items, iid)
		}
	}
	a.store.mu.Unlock()
	a.store.rebuildOrder()
	go a.store.Save()
	a.store.addActivity("删除媒体库", "删除媒体库 "+id, "LibraryDeleted", "Information")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func normalizeCollectionType(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "movie", "movies", "电影", "films":
		return "movies"
	case "tv", "tvshows", "show", "shows", "series", "电视剧", "剧集":
		return "tvshows"
	}
	return "movies"
}
