package main

import (
	"sort"
	"strings"
)

// mergeMode 返回多版本合并模式：none / folder（同库） / libraries（跨库）。
func (a *App) mergeMode() string {
	if a.store.settingBool("merge_versions_libraries", false) {
		return "libraries"
	}
	if a.store.settingBool("merge_versions_folder", false) {
		return "folder"
	}
	return "none"
}

// versionKey 生成稳定的"同片不同版本"分组键。
// 电影按 名称+年份 优先（避免一部有 tmdb、一部没有而分不到一起），其次 tmdb；
// 剧集按 剧名+季+集。
func (a *App) versionKey(it *Item) string {
	switch it.Type {
	case "Movie", "Episode":
	default:
		return "id:" + it.Id
	}
	if it.Type == "Episode" {
		sn := strings.ToLower(strings.TrimSpace(it.SeriesName))
		if sn == "" {
			sn = strings.ToLower(strings.TrimSpace(it.Name))
		}
		return "ep:" + sn + ":s" + itoa(it.ParentIndexNumber) + ":e" + itoa(it.IndexNumber)
	}
	name := strings.ToLower(strings.TrimSpace(it.Name))
	if name != "" && it.ProductionYear > 0 {
		return "name:" + name + ":" + itoa(it.ProductionYear)
	}
	if it.ProviderIds != nil {
		if t := strings.TrimSpace(it.ProviderIds["Tmdb"]); t != "" {
			return "tmdb:" + t
		}
	}
	return "id:" + it.Id
}

// versionGroup 返回与该条目同属一个"多版本组"的所有条目（含自身）。
func (a *App) versionGroup(it *Item) []*Item {
	mode := a.mergeMode()
	if mode == "none" {
		return []*Item{it}
	}
	key := a.versionKey(it)
	a.store.mu.RLock()
	var out []*Item
	for _, x := range a.store.Items {
		if x.Type != it.Type {
			continue
		}
		if mode == "folder" && x.LibraryId != it.LibraryId {
			continue
		}
		if a.versionKey(x) == key {
			out = append(out, x)
		}
	}
	a.store.mu.RUnlock()
	if len(out) == 0 {
		return []*Item{it}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Id < out[j].Id })
	return out
}

// dedupVersions 在列表结果中按版本键去重，只保留一个代表条目。
func (a *App) dedupVersions(items []*Item) []*Item {
	mode := a.mergeMode()
	if mode == "none" {
		return items
	}
	seen := map[string]bool{}
	out := make([]*Item, 0, len(items))
	for _, it := range items {
		key := a.versionKey(it)
		if mode == "folder" {
			key += "|" + it.LibraryId
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, it)
	}
	return out
}
