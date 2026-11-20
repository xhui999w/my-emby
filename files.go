package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var errBadPath = errors.New("路径不合法（禁止目录遍历或非法字符）")

type fileEntry struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	IsDir    bool      `json:"isDir"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

// fileRel 把客户端传入的"根相对路径"规范化并校验，拒绝 ../ 与非法字符。
func (a *App) fileRel(p string) (string, error) {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	if strings.ContainsAny(p, "\x00\r\n:*?\"<>|") {
		return "", errBadPath
	}
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return ".", nil
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", errBadPath
		}
	}
	native := filepath.FromSlash(p)
	if !filepath.IsLocal(native) {
		return "", errBadPath
	}
	return native, nil
}

// handleFiles 统一入口：GET /api/files、POST /api/files/rename、DELETE /api/files
func (a *App) handleFiles(w http.ResponseWriter, r *http.Request, u *User) {
	if a.cfg.FileRoot == "" {
		writeErr(w, http.StatusInternalServerError, "未配置文件管理根目录（FILE_MANAGER_ROOT）")
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/files":
		a.listFiles(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/files/rename":
		a.renameFile(w, r)
	case r.Method == http.MethodDelete && r.URL.Path == "/api/files":
		a.deleteFiles(w, r)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "不支持的请求")
	}
}

func (a *App) listFiles(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rel, err := a.fileRel(q.Get("path"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	full := filepath.Join(a.cfg.FileRoot, rel)
	fi, err := os.Stat(full)
	if err != nil || !fi.IsDir() {
		writeErr(w, http.StatusBadRequest, "路径不是目录")
		return
	}
	search := strings.ToLower(strings.TrimSpace(q.Get("search")))
	global := q.Get("scope") == "global" && search != ""
	order := q.Get("sort")
	desc := q.Get("order") == "desc"

	entries := []fileEntry{}
	visited := 0
	var walk func(dir, relDir string)
	walk = func(dir, relDir string) {
		f, e := os.Open(dir)
		if e != nil {
			return
		}
		ds, _ := f.ReadDir(-1)
		f.Close()
		for _, d := range ds {
			visited++
			if visited > 200000 {
				return
			}
			name := d.Name()
			childRel := filepath.ToSlash(filepath.Join(relDir, name))
			info, e := d.Info()
			if e != nil {
				continue
			}
			if info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			if search != "" && !strings.Contains(strings.ToLower(name), search) {
				if !(global && info.IsDir()) {
					continue
				}
			} else if search == "" || strings.Contains(strings.ToLower(name), search) {
				sz := info.Size()
				if info.IsDir() {
					sz = 0
				}
				entries = append(entries, fileEntry{Name: name, Path: "/" + childRel, IsDir: info.IsDir(), Size: sz, Modified: info.ModTime()})
			}
			if global && info.IsDir() && !skipDirs[name] {
				walk(filepath.Join(dir, name), childRel)
			}
		}
	}
	walk(full, filepath.ToSlash(rel))
	if rel == "." {
		// 修正根路径下的子项路径前缀
		for i := range entries {
			entries[i].Path = "/" + strings.TrimPrefix(entries[i].Path, "/./")
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		x, y := entries[i], entries[j]
		c := 0
		switch order {
		case "time":
			if x.Modified.Before(y.Modified) {
				c = -1
			} else if x.Modified.After(y.Modified) {
				c = 1
			}
		case "size":
			if x.Size < y.Size {
				c = -1
			} else if x.Size > y.Size {
				c = 1
			}
		default:
			c = strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name))
		}
		if c == 0 {
			c = strings.Compare(x.Path, y.Path)
		}
		if desc {
			return c > 0
		}
		return c < 0
	})
	writeJSON(w, http.StatusOK, map[string]any{"path": q.Get("path"), "entries": entries, "total": len(entries)})
}

func (a *App) renameFile(w http.ResponseWriter, r *http.Request) {
	var b struct{ OldPath, NewPath string }
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体无效")
		return
	}
	oldRel, e1 := a.fileRel(b.OldPath)
	newRel, e2 := a.fileRel(b.NewPath)
	if e1 != nil || e2 != nil {
		writeErr(w, http.StatusBadRequest, errBadPath.Error())
		return
	}
	if oldRel == "." || newRel == "." ||
		filepath.Dir(oldRel) != filepath.Dir(newRel) ||
		strings.TrimSpace(filepath.Base(newRel)) == "" {
		writeErr(w, http.StatusBadRequest, "只能重命名同级目录，不能操作根目录")
		return
	}
	oldFull := filepath.Join(a.cfg.FileRoot, oldRel)
	newFull := filepath.Join(a.cfg.FileRoot, newRel)
	if fi, err := os.Stat(oldFull); err != nil || !fi.IsDir() {
		writeErr(w, http.StatusBadRequest, "只支持重命名文件夹")
		return
	}
	if _, err := os.Stat(newFull); err == nil {
		writeErr(w, http.StatusBadRequest, "同级目录已存在该名称")
		return
	}
	if err := os.Rename(oldFull, newFull); err != nil {
		writeErr(w, http.StatusInternalServerError, "重命名失败: "+err.Error())
		return
	}
	a.rescanForPaths(oldFull, newFull)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) deleteFiles(w http.ResponseWriter, r *http.Request) {
	var b struct{ Paths []string }
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil || len(b.Paths) == 0 || len(b.Paths) > 100 {
		writeErr(w, http.StatusBadRequest, "请选择 1–100 个文件")
		return
	}
	rels := make([]string, 0, len(b.Paths))
	fulls := make([]string, 0, len(b.Paths))
	for _, s := range b.Paths {
		rel, err := a.fileRel(s)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if rel == "." {
			writeErr(w, http.StatusBadRequest, "不能删除根目录")
			return
		}
		full := filepath.Join(a.cfg.FileRoot, rel)
		if a.isLibraryRoot(full) || a.containsLibraryRoot(full) {
			writeErr(w, http.StatusBadRequest, "不能删除媒体库根目录或其父目录")
			return
		}
		for _, prev := range fulls {
			if pathOverlap(prev, full) {
				writeErr(w, http.StatusBadRequest, "删除路径不能重复或互相包含")
				return
			}
		}
		rels = append(rels, rel)
		fulls = append(fulls, full)
	}
	for _, full := range fulls {
		if err := os.RemoveAll(full); err != nil {
			writeErr(w, http.StatusInternalServerError, "删除失败: "+err.Error())
			return
		}
	}
	a.rescanForPaths(fulls...)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": rels})
}

func pathOverlap(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+string(os.PathSeparator)) || strings.HasPrefix(b, a+string(os.PathSeparator))
}

func (a *App) isLibraryRoot(full string) bool {
	a.store.mu.RLock()
	defer a.store.mu.RUnlock()
	for _, l := range a.store.Libraries {
		for _, root := range l.Paths {
			if filepath.Clean(root) == filepath.Clean(full) {
				return true
			}
		}
	}
	return false
}

func (a *App) containsLibraryRoot(full string) bool {
	a.store.mu.RLock()
	defer a.store.mu.RUnlock()
	for _, l := range a.store.Libraries {
		for _, root := range l.Paths {
			if strings.HasPrefix(filepath.Clean(root), filepath.Clean(full)+string(os.PathSeparator)) {
				return true
			}
		}
	}
	return false
}

// rescanForPaths 依据变更路径触发对应媒体库的增量扫描。
func (a *App) rescanForPaths(paths ...string) {
	libs := map[string]bool{}
	for _, p := range paths {
		if lib := a.libForPath(p); lib != "" {
			libs[lib] = true
		}
	}
	for lib := range libs {
		a.enqueueScan(lib)
	}
}
