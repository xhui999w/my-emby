package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

func (a *App) effectiveServerName() string {
	if v := a.store.setting("server_name"); strings.TrimSpace(v) != "" {
		return v
	}
	return a.cfg.ServerName
}

func (a *App) settingInt(k string, def int) int {
	v := a.store.setting(k)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// enhancementValues 汇总增强设置（供后台读写）。
func (a *App) enhancementValues() map[string]any {
	ports := []int{}
	if raw := a.store.setting("third_party_proxy_ports"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &ports)
	}
	return map[string]any{
		"ProxyDebug":                   a.store.settingBool("proxy_debug", false),
		"FastCDN":                      a.store.settingBool("fast_cdn", true),
		"WatchEnabled":                 a.store.settingBool("watch_enabled", a.cfg.WatchEnabled),
		"SearchByInitials":             a.store.settingBool("search_by_initials", true),
		"HideMissingActorImages":       a.store.settingBool("hide_missing_actor_images", false),
		"MergeVersionsInFolder":        a.store.settingBool("merge_versions_folder", false),
		"MergeVersionsAcrossLibraries": a.store.settingBool("merge_versions_libraries", false),
		"VirtualLibrary":               a.store.settingBool("virtual_library", false),
		"Theme":                        a.store.settingBool("theme", false),
		"TmdbStartup":                  a.store.settingBool("tmdb_startup", false),
		"Trakt":                        a.store.settingBool("trakt", false),
		"PerformanceMode":              a.store.settingBool("performance_mode", false),
		"PosterBadge":                  a.store.settingBool("poster_badge", true),
		"FastPath":                     a.store.settingBool("fast_path", false),
		"ScanConcurrency":              a.settingInt("scan_concurrency", 1),
		"UpdateConcurrency":            a.settingInt("update_concurrency", 1),
		"ServerName":                   a.effectiveServerName(),
		"WatchDelaySeconds":            a.settingInt("watch_delay_seconds", 30),
		"ThirdPartyProxyPorts":         ports,
	}
}

// handleEnhancements GET/PUT /admin/enhancements
func (a *App) handleEnhancements(w http.ResponseWriter, r *http.Request, u *User) {
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, a.enhancementValues())
		return
	}
	if r.Method != http.MethodPut {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET/PUT")
		return
	}
	var b struct {
		ProxyDebug                   *bool
		FastCDN                      *bool
		WatchEnabled                 *bool
		SearchByInitials             *bool
		HideMissingActorImages       *bool
		MergeVersionsInFolder        *bool
		MergeVersionsAcrossLibraries *bool
		VirtualLibrary               *bool
		Theme                        *bool
		TmdbStartup                  *bool
		Trakt                        *bool
		PerformanceMode              *bool
		PosterBadge                  *bool
		FastPath                     *bool
		ScanConcurrency              *int
		UpdateConcurrency            *int
		ServerName                   *string
		WatchDelaySeconds            *int
		ThirdPartyProxyPorts         *[]int
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体无效")
		return
	}
	setB := func(k string, v *bool) {
		if v != nil {
			a.store.setSetting(k, boolStr(*v))
		}
	}
	setB("proxy_debug", b.ProxyDebug)
	setB("fast_cdn", b.FastCDN)
	setB("watch_enabled", b.WatchEnabled)
	setB("search_by_initials", b.SearchByInitials)
	setB("hide_missing_actor_images", b.HideMissingActorImages)
	setB("merge_versions_folder", b.MergeVersionsInFolder)
	setB("merge_versions_libraries", b.MergeVersionsAcrossLibraries)
	setB("virtual_library", b.VirtualLibrary)
	setB("theme", b.Theme)
	setB("tmdb_startup", b.TmdbStartup)
	setB("trakt", b.Trakt)
	setB("performance_mode", b.PerformanceMode)
	setB("poster_badge", b.PosterBadge)
	setB("fast_path", b.FastPath)

	if b.ScanConcurrency != nil && (*b.ScanConcurrency < 1 || *b.ScanConcurrency > 64) {
		writeErr(w, http.StatusBadRequest, "扫描并发需为 1–64")
		return
	}
	if b.ScanConcurrency != nil {
		a.store.setSetting("scan_concurrency", strconv.Itoa(*b.ScanConcurrency))
	}
	if b.UpdateConcurrency != nil && (*b.UpdateConcurrency < 1 || *b.UpdateConcurrency > 64) {
		writeErr(w, http.StatusBadRequest, "更新并发需为 1–64")
		return
	}
	if b.UpdateConcurrency != nil {
		a.store.setSetting("update_concurrency", strconv.Itoa(*b.UpdateConcurrency))
	}
	if b.WatchDelaySeconds != nil {
		if *b.WatchDelaySeconds < 10 || *b.WatchDelaySeconds > 86400 {
			writeErr(w, http.StatusBadRequest, "监听延时需为 10–86400 秒")
			return
		}
		a.store.setSetting("watch_delay_seconds", strconv.Itoa(*b.WatchDelaySeconds))
	}
	if b.ServerName != nil {
		name := strings.TrimSpace(*b.ServerName)
		if name == "" || len(name) > 128 {
			writeErr(w, http.StatusBadRequest, "服务器名称需为 1–128 字节")
			return
		}
		a.store.setSetting("server_name", name)
	}
	if b.ThirdPartyProxyPorts != nil {
		ports := *b.ThirdPartyProxyPorts
		if len(ports) > 32 {
			writeErr(w, http.StatusBadRequest, "最多 32 个反代端口")
			return
		}
		seen := map[int]bool{}
		for _, p := range ports {
			if p < 1 || p > 65535 || p == 8097 || seen[p] {
				writeErr(w, http.StatusBadRequest, "端口需在 1–65535，且不重复、不使用 8097")
				return
			}
			seen[p] = true
		}
		raw, _ := json.Marshal(ports)
		a.store.setSetting("third_party_proxy_ports", string(raw))
	}
	a.store.Save()
	a.store.addActivity("增强设置", "更新增强功能设置", "EnhancementUpdated", "Information")
	writeJSON(w, http.StatusOK, a.enhancementValues())
}
