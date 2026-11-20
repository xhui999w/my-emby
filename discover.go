package main

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
)

// 一批客户端常用的"发现/筛选"接口，返回 Emby 兼容结构。

func (a *App) handleSearchHints(w http.ResponseWriter, r *http.Request, u *User) {
	q := r.URL.Query()
	term := strings.ToLower(strings.TrimSpace(q.Get("SearchTerm")))
	limit := atoiDefault(q.Get("Limit"), 20)
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	include := parseTypeSet(a.normalizeIncludeTypes(q.Get("IncludeItemTypes"), r, ""))
	hints := make([]map[string]any, 0, limit)
	a.store.mu.RLock()
	for _, it := range a.store.Items {
		if it.Type != "Movie" && it.Type != "Series" && it.Type != "Episode" {
			continue
		}
		if len(include) > 0 && !include[it.Type] {
			continue
		}
		nm := strings.ToLower(it.Name)
		sn := strings.ToLower(it.SeriesName)
		match := term == "" || strings.Contains(nm, term) || strings.Contains(sn, term)
		if !match && term != "" {
			if ini := initials(it.Name); ini != "" && strings.HasPrefix(ini, term) {
				match = true
			}
		}
		if !match {
			continue
		}
		primary := ""
		if it.ImageTags != nil {
			primary = it.ImageTags["Primary"]
		}
		hints = append(hints, map[string]any{
			"ItemId": it.Id, "Id": it.Id, "Name": it.Name, "Type": it.Type,
			"MediaType": it.MediaType, "ProductionYear": it.ProductionYear,
			"SeriesName": it.SeriesName, "IndexNumber": it.IndexNumber,
			"ParentIndexNumber": it.ParentIndexNumber,
			"ImageTags":         it.ImageTags, "PrimaryImageTag": primary,
		})
		if len(hints) >= limit {
			break
		}
	}
	a.store.mu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{"SearchHints": hints, "TotalRecordCount": len(hints)})
}

func (a *App) handleFilters(w http.ResponseWriter, r *http.Request, u *User) {
	genres := map[string]bool{}
	years := map[string]bool{}
	ratings := map[string]bool{}
	a.store.mu.RLock()
	for _, it := range a.store.Items {
		if it.Type != "Movie" && it.Type != "Series" && it.Type != "Episode" {
			continue
		}
		for _, g := range it.Genres {
			if g = strings.TrimSpace(g); g != "" {
				genres[g] = true
			}
		}
		if it.ProductionYear > 0 {
			years[itoa(it.ProductionYear)] = true
		}
		if it.OfficialRating != "" {
			ratings[it.OfficialRating] = true
		}
	}
	a.store.mu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"Genres":          sortedKeys(genres),
		"Tags":            []string{},
		"OfficialRatings": sortedKeys(ratings),
		"Years":           sortedKeys(years),
	})
}

func (a *App) handleGenres(w http.ResponseWriter, r *http.Request, u *User) {
	set := map[string]bool{}
	a.store.mu.RLock()
	for _, it := range a.store.Items {
		for _, g := range it.Genres {
			if g = strings.TrimSpace(g); g != "" {
				set[g] = true
			}
		}
	}
	a.store.mu.RUnlock()
	items := make([]map[string]any, 0, len(set))
	for _, g := range sortedKeys(set) {
		items = append(items, map[string]any{"Name": g, "Id": h16("genre:" + g)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"Items": items, "TotalRecordCount": len(items)})
}

func (a *App) handlePersons(w http.ResponseWriter, r *http.Request, u *User) {
	type p struct{ id, name, typ string }
	seen := map[string]p{}
	a.store.mu.RLock()
	for _, it := range a.store.Items {
		for _, per := range it.People {
			if per.Name == "" {
				continue
			}
			key := per.Type + ":" + per.Name
			if _, ok := seen[key]; !ok {
				id := per.Id
				if id == "" {
					id = h16("person:" + per.Name)
				}
				seen[key] = p{id, per.Name, per.Type}
			}
		}
	}
	a.store.mu.RUnlock()
	items := make([]map[string]any, 0, len(seen))
	for _, v := range seen {
		items = append(items, map[string]any{"Name": v.name, "Id": v.id, "Type": v.typ})
	}
	writeJSON(w, http.StatusOK, map[string]any{"Items": items, "TotalRecordCount": len(items)})
}

func (a *App) handleStudios(w http.ResponseWriter, r *http.Request, u *User) {
	seen := map[string]int64{}
	a.store.mu.RLock()
	for _, it := range a.store.Items {
		for _, s := range it.Studios {
			if s.Name != "" {
				seen[s.Name] = s.Id
			}
		}
	}
	a.store.mu.RUnlock()
	items := make([]map[string]any, 0, len(seen))
	for name, id := range seen {
		items = append(items, map[string]any{"Name": name, "Id": id})
	}
	writeJSON(w, http.StatusOK, map[string]any{"Items": items, "TotalRecordCount": len(items)})
}

func (a *App) handleYears(w http.ResponseWriter, r *http.Request, u *User) {
	set := map[string]bool{}
	a.store.mu.RLock()
	for _, it := range a.store.Items {
		if it.ProductionYear > 0 {
			set[itoa(it.ProductionYear)] = true
		}
	}
	a.store.mu.RUnlock()
	items := make([]map[string]any, 0, len(set))
	for _, y := range sortedKeys(set) {
		items = append(items, map[string]any{"Name": y, "Id": y})
	}
	writeJSON(w, http.StatusOK, map[string]any{"Items": items, "TotalRecordCount": len(items)})
}

func (a *App) handleSystemEndpoint(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"IsLocal": true, "IsInNetwork": true})
}

// handleDisplayPreferences GET 返回已保存偏好；POST/PUT 覆盖保存（按 userId 作用域）。
func (a *App) handleDisplayPreferences(w http.ResponseWriter, r *http.Request, u *User, id string) {
	key := "display_prefs:" + id
	if r.Method == http.MethodGet {
		raw := a.store.setting(key)
		if raw == "" {
			writeJSON(w, http.StatusOK, map[string]any{"Id": id, "CustomPrefs": map[string]any{}})
			return
		}
		var v any
		if json.Unmarshal([]byte(raw), &v) == nil {
			writeJSON(w, http.StatusOK, v)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	if r.Method == http.MethodPost || r.Method == http.MethodPut {
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeErr(w, http.StatusBadRequest, "请求体无效")
			return
		}
		if len(raw) > 0 && !json.Valid(raw) {
			writeErr(w, http.StatusBadRequest, "非合法 JSON")
			return
		}
		a.store.setSetting(key, string(raw))
		go a.store.Save()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	writeErr(w, http.StatusMethodNotAllowed, "不支持的方法")
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
