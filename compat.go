package main

import (
	"net/http"
	"sort"
	"strings"
)

// firstHeader 取逗号分隔头的第一个值（用于 X-Forwarded-Proto/Host）。
func firstHeader(r *http.Request, key string) string {
	if v := r.Header.Get(key); v != "" {
		return strings.TrimSpace(strings.Split(v, ",")[0])
	}
	return ""
}

// normalizePath 规范化请求路径：合并重复斜杠、去除 /emby 前缀、去掉末尾斜杠。
func normalizePath(p string) string {
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	for {
		lp := strings.ToLower(p)
		if strings.HasPrefix(lp, "/emby/") {
			p = p[len("/emby"):]
			continue
		}
		if lp == "/emby" {
			p = "/"
		}
		break
	}
	if p == "" {
		p = "/"
	}
	if len(p) > 1 && strings.HasSuffix(p, "/") {
		p = strings.TrimRight(p, "/")
		if p == "" {
			p = "/"
		}
	}
	return p
}

func clientName(r *http.Request) string {
	if c := r.Header.Get("X-Emby-Client"); c != "" {
		return c
	}
	return r.URL.Query().Get("X-Emby-Client")
}

// IsLenna 判断是否 Lenna 客户端。
func IsLenna(r *http.Request) bool {
	if strings.EqualFold(clientName(r), "Lenna") {
		return true
	}
	return strings.HasPrefix(strings.ToLower(r.UserAgent()), "lenna/")
}

// absoluteBase 依据反向代理头构造 scheme://host。
func absoluteBase(r *http.Request) (string, string) {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if fp := firstHeader(r, "X-Forwarded-Proto"); fp == "https" || fp == "http" {
		scheme = fp
	}
	host := r.Host
	if fh := firstHeader(r, "X-Forwarded-Host"); fh != "" && !strings.ContainsAny(fh, "/\\?#@ \t\r\n") {
		host = fh
	}
	return scheme, host
}

func canonKind(s string) string {
	known := []string{"Movie", "Series", "Season", "Episode", "Folder", "CollectionFolder",
		"UserView", "MusicVideo", "Audio", "MusicAlbum", "MusicArtist", "BoxSet"}
	t := strings.TrimSpace(s)
	for _, k := range known {
		if strings.EqualFold(t, k) {
			return k
		}
	}
	return t
}

// normalizeIncludeTypes 规范化 IncludeItemTypes 大小写，并修正 Lenna 对 TV 库误用 Movie 的 quirk。
func (a *App) normalizeIncludeTypes(raw string, r *http.Request, parentID string) string {
	if strings.TrimSpace(raw) == "" {
		return raw
	}
	parts := strings.Split(raw, ",")
	for i, p := range parts {
		parts[i] = canonKind(p)
	}
	if IsLenna(r) && len(parts) == 1 && parts[0] == "Movie" && parentID != "" {
		a.store.mu.RLock()
		lib := a.store.Libraries[parentID]
		a.store.mu.RUnlock()
		if lib != nil && lib.CollectionType == "tvshows" {
			parts[0] = "Series"
		}
	}
	return strings.Join(parts, ",")
}

func sharedGenres(a, b []string) int {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	set := map[string]bool{}
	for _, g := range a {
		set[strings.ToLower(strings.TrimSpace(g))] = true
	}
	n := 0
	for _, g := range b {
		if set[strings.ToLower(strings.TrimSpace(g))] {
			n++
		}
	}
	return n
}

// similarItems 基于同类型 + 共享题材 + 同年代的相似推荐。
func (a *App) similarItems(it *Item, limit int) []*Item {
	if limit <= 0 {
		limit = 12
	}
	if limit > 50 {
		limit = 50
	}
	type cand struct {
		it    *Item
		score int
	}
	a.store.mu.RLock()
	var cands []cand
	for _, x := range a.store.Items {
		if x.Id == it.Id || x.Type != it.Type {
			continue
		}
		score := 0
		if x.ProductionYear > 0 && x.ProductionYear == it.ProductionYear {
			score += 2
		}
		score += sharedGenres(it.Genres, x.Genres)
		if score > 0 {
			cands = append(cands, cand{it: x, score: score})
		}
	}
	a.store.mu.RUnlock()
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score > cands[j].score
		}
		return cands[i].it.Name < cands[j].it.Name
	})
	out := []*Item{}
	for _, c := range cands {
		if len(out) >= limit {
			break
		}
		out = append(out, c.it)
	}
	return out
}
