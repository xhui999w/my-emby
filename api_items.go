package main

import (
	"net/http"
	"sort"
	"strings"
)

// childrenOf 返回 parentID 的直接子条目。
func (a *App) childrenOf(parentID string) []*Item {
	a.store.mu.RLock()
	defer a.store.mu.RUnlock()
	var out []*Item
	for _, it := range a.store.Items {
		if it.ParentId == parentID {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ParentIndexNumber != out[j].ParentIndexNumber {
			return out[i].ParentIndexNumber < out[j].ParentIndexNumber
		}
		if out[i].IndexNumber != out[j].IndexNumber {
			return out[i].IndexNumber < out[j].IndexNumber
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// descendants 递归收集 parentID 下所有后代。
func (a *App) descendants(parentID string) []*Item {
	out := []*Item{}
	stack := a.childrenOf(parentID)
	for len(stack) > 0 {
		cur := stack[0]
		stack = stack[1:]
		out = append(out, cur)
		stack = append(stack, a.childrenOf(cur.Id)...)
	}
	return out
}

func typeAllowed(t string, include, exclude map[string]bool) bool {
	if len(include) > 0 && !include[t] {
		return false
	}
	if exclude[t] {
		return false
	}
	return true
}

func parseTypeSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			m[p] = true
		}
	}
	return m
}

// queryItems 实现 /Users/{userId}/Items 的通用查询。
func (a *App) queryItems(w http.ResponseWriter, r *http.Request, u *User, userID string) {
	q := r.URL.Query()
	parentID := q.Get("ParentId")
	recursive := boolParam(r, "Recursive")
	include := parseTypeSet(a.normalizeIncludeTypes(q.Get("IncludeItemTypes"), r, parentID))
	exclude := parseTypeSet(q.Get("ExcludeItemTypes"))
	search := strings.ToLower(strings.TrimSpace(q.Get("SearchTerm")))

	includeRaw := q.Get("IncludeItemTypes")

	// 库发现：明确要 CollectionFolder/UserView，或 parentless 无筛选时返回库视图
	if parentID == "" {
		lr := strings.ToLower(includeRaw)
		if strings.Contains(lr, "collectionfolder") || strings.Contains(lr, "userview") {
			a.handleUserViews(w, r, u)
			return
		}
		if search == "" && strings.TrimSpace(includeRaw) == "" &&
			q.Get("Ids") == "" && q.Get("Filters") == "" && !recursive {
			a.handleUserViews(w, r, u)
			return
		}
	}

	var pool []*Item
	if parentID != "" {
		if recursive {
			pool = a.descendants(parentID)
		} else {
			pool = a.childrenOf(parentID)
		}
	} else {
		// 无 ParentId：默认返回顶层电影与剧集；若显式指定了 IncludeItemTypes，
		// 则在整个库中按类型检索（例如 IncludeItemTypes=Episode 列出所有剧集）。
		a.store.mu.RLock()
		for _, it := range a.store.Items {
			if len(include) > 0 {
				pool = append(pool, it)
			} else if it.Type == "Movie" || it.Type == "Series" {
				pool = append(pool, it)
			}
		}
		a.store.mu.RUnlock()
		sort.Slice(pool, func(i, j int) bool { return pool[i].Name < pool[j].Name })
	}

	filtered := make([]*Item, 0, len(pool))
	for _, it := range pool {
		if !typeAllowed(it.Type, include, exclude) {
			continue
		}
		if search != "" {
			nm := strings.ToLower(it.Name)
			sn := strings.ToLower(it.SeriesName)
			matched := strings.Contains(nm, search) || strings.Contains(sn, search)
			if !matched && a.store.settingBool("search_by_initials", true) {
				if ini := initials(it.Name); ini != "" && (ini == search || strings.HasPrefix(ini, search)) {
					matched = true
				}
			}
			if !matched {
				continue
			}
		}
		filtered = append(filtered, it)
	}
	// 多版本合并：列表去重
	filtered = a.dedupVersions(filtered)

	// 排序
	sortBy := q.Get("SortBy")
	if strings.Contains(sortBy, "ProductionYear") {
		sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].ProductionYear > filtered[j].ProductionYear })
	} else if !strings.Contains(sortBy, "Random") {
		sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].Name < filtered[j].Name })
	}

	total := len(filtered)
	start := atoiDefault(q.Get("StartIndex"), 0)
	if start < 0 || start > total {
		start = 0
	}
	end := total
	if lim := atoiDefault(q.Get("Limit"), 0); lim > 0 && start+lim < end {
		end = start + lim
	}
	page := filtered[start:end]

	out := make([]any, 0, len(page))
	for _, it := range page {
		out = append(out, a.itemWithUserData(it, u))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"Items": out, "TotalRecordCount": total, "StartIndex": start,
	})
}

// itemWithUserData 复制一份 Item 并附加当前用户的播放数据（不改动底层对象）。
func (a *App) itemWithUserData(it *Item, u *User) Item {
	cp := *it
	cp.UserData = &UserData{}
	if cp.Etag == "" {
		cp.Etag = h16(it.Id + "|" + it.Name + "|" + itoa(int(it.RunTimeTicks)))
	}
	if a.store.settingBool("hide_missing_actor_images", false) {
		cp.People = nil
	}
	return cp
}

func (a *App) handleItem(w http.ResponseWriter, r *http.Request, u *User, id string) {
	a.store.mu.RLock()
	it, ok := a.store.Items[id]
	if !ok {
		// 也可能是媒体库本身
		if lib, lok := a.store.Libraries[id]; lok {
			a.store.mu.RUnlock()
			writeJSON(w, http.StatusOK, map[string]any{
				"Id": lib.Id, "Name": lib.Name, "Type": "CollectionFolder",
				"IsFolder": true, "CollectionType": lib.CollectionType, "ServerId": a.store.ServerId,
			})
			return
		}
	}
	a.store.mu.RUnlock()
	if !ok {
		writeErr(w, http.StatusNotFound, "条目不存在")
		return
	}
	writeJSON(w, http.StatusOK, a.itemWithUserData(it, u))
}

func (a *App) handlePlaybackInfo(w http.ResponseWriter, r *http.Request, u *User, id string) {
	a.store.mu.RLock()
	it, ok := a.store.Items[id]
	a.store.mu.RUnlock()
	if !ok {
		writeErr(w, http.StatusNotFound, "条目不存在")
		return
	}
	if !a.authorizePlayback(w, r, u) {
		return
	}
	tok := tokenFromRequest(r)
	scheme, host := absoluteBase(r)
	versions := a.versionGroup(it)
	sources := make([]MediaSource, 0, len(it.MediaSources))
	for _, v := range versions {
		for _, ms := range v.MediaSources {
			cp := ms
			urlStr := ms.DirectStreamUrl
			// 仅对内部相对地址追加 api_key；.strm 等远端绝对直链保持原样
			if !strings.HasPrefix(urlStr, "http://") && !strings.HasPrefix(urlStr, "https://") {
				sep := "?"
				if strings.Contains(urlStr, "?") {
					sep = "&"
				}
				urlStr = urlStr + sep + "api_key=" + tok
			}
			cp.DirectStreamUrl = urlStr
			if strings.HasPrefix(urlStr, "http://") || strings.HasPrefix(urlStr, "https://") {
				cp.Path = urlStr
			} else {
				cp.Path = scheme + "://" + host + urlStr
			}
			cp.SupportsDirectPlay = true
			cp.SupportsDirectStream = true
			cp.SupportsTranscoding = false
			if len(versions) > 1 {
				cp.Name = v.Name
			}
			sources = append(sources, cp)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"MediaSources":  sources,
		"PlaySessionId": newID(),
	})
}

func (a *App) handleSimilar(w http.ResponseWriter, r *http.Request, u *User, id string) {
	it := a.getItem(id)
	if it == nil {
		writeErr(w, http.StatusNotFound, "条目不存在")
		return
	}
	limit := atoiDefault(r.URL.Query().Get("Limit"), 12)
	items := a.similarItems(it, limit)
	out := make([]any, 0, len(items))
	for _, x := range items {
		out = append(out, a.itemWithUserData(x, u))
	}
	writeJSON(w, http.StatusOK, map[string]any{"Items": out, "TotalRecordCount": len(out)})
}

func (a *App) handleLatest(w http.ResponseWriter, r *http.Request, u *User, userID string) {
	a.store.mu.RLock()
	var pool []*Item
	for _, it := range a.store.Items {
		if it.Type == "Movie" || it.Type == "Episode" {
			pool = append(pool, it)
		}
	}
	a.store.mu.RUnlock()
	sort.Slice(pool, func(i, j int) bool { return pool[i].Name < pool[j].Name })
	if len(pool) > 20 {
		pool = pool[:20]
	}
	out := make([]any, 0, len(pool))
	for _, it := range pool {
		out = append(out, a.itemWithUserData(it, u))
	}
	writeJSON(w, http.StatusOK, out)
}
