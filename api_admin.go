package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

func (a *App) handleActivityLog(w http.ResponseWriter, r *http.Request, u *User) {
	typ := strings.ToLower(r.URL.Query().Get("type"))
	limit := atoiDefault(r.URL.Query().Get("limit"), 200)
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	a.store.mu.RLock()
	all := a.store.Activity
	out := make([]Activity, 0, limit)
	for _, e := range all {
		if matchLogType(e, typ) {
			out = append(out, e)
			if len(out) >= limit {
				break
			}
		}
	}
	a.store.mu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{"Items": out, "TotalRecordCount": len(out)})
}

func matchLogType(e Activity, typ string) bool {
	switch typ {
	case "", "all":
		return true
	case "error":
		return e.Type == "error" || e.Severity == "Error"
	case "redirect":
		return e.Type == "PlaybackRedirect" || e.Type == "ProxyDebug"
	case "activity":
		return e.Type != "error" && e.Type != "PlaybackRedirect" && e.Type != "ProxyDebug"
	default:
		return strings.EqualFold(e.Type, typ)
	}
}

func (a *App) handleSessions(w http.ResponseWriter, r *http.Request, u *User) {
	a.store.mu.RLock()
	sessions := make([]*Session, 0, len(a.store.Sessions))
	for _, s := range a.store.Sessions {
		sessions = append(sessions, s)
	}
	a.store.mu.RUnlock()
	writeJSON(w, http.StatusOK, sessions)
}

// handlePlaybackProgress 接收客户端播放进度上报（开始/进度/停止）。
func (a *App) handlePlaybackProgress(w http.ResponseWriter, r *http.Request, u *User) {
	var body struct {
		ItemId        string `json:"ItemId"`
		PositionTicks int64  `json:"PositionTicks"`
		PlaySessionId string `json:"PlaySessionId"`
		PlayMethod    string `json:"PlayMethod"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	sid := body.PlaySessionId
	if sid == "" {
		sid = "sess-" + u.Id
	}
	a.store.mu.Lock()
	if a.store.Sessions == nil {
		a.store.Sessions = map[string]*Session{}
	}
	s, ok := a.store.Sessions[sid]
	if !ok {
		s = &Session{Id: sid, UserId: u.Id, DeviceName: r.Header.Get("X-Emby-Device-Name"),
			Client: r.Header.Get("X-Emby-Client")}
		a.store.Sessions[sid] = s
	}
	if body.ItemId != "" {
		s.ItemId = body.ItemId
	}
	s.PositionTicks = body.PositionTicks
	s.LastActivity = time.Now().Unix()
	a.store.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handlePlaybackStopped(w http.ResponseWriter, r *http.Request, u *User) {
	var body struct {
		ItemId        string `json:"ItemId"`
		PlaySessionId string `json:"PlaySessionId"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.PlaySessionId != "" {
		a.store.mu.Lock()
		delete(a.store.Sessions, body.PlaySessionId)
		a.store.mu.Unlock()
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAdminStats 供内置 Web 后台展示。
func (a *App) handleAdminStats(w http.ResponseWriter, r *http.Request, u *User) {
	a.store.mu.RLock()
	users := len(a.store.Users)
	libs := len(a.store.Libraries)
	movies, episodes, series, strms, probed := 0, 0, 0, 0, 0
	for _, it := range a.store.Items {
		switch it.Type {
		case "Movie":
			movies++
		case "Episode":
			episodes++
		case "Series":
			series++
		}
		if it.StreamURL != "" {
			strms++
		}
		if it.Probed {
			probed++
		}
	}
	activity := len(a.store.Activity)
	a.store.mu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"serverId": a.store.ServerId, "serverName": a.effectiveServerName(),
		"version": serverVersion, "users": users, "libraries": libs,
		"movies": movies, "episodes": episodes, "series": series, "activity": activity,
		"mediaRoots":        a.store.cfg.MediaRoots,
		"backend":           a.store.backend,
		"tmdbEnabled":       a.cfg.TMDBAPIKey != "",
		"doubanEnabled":     a.cfg.DoubanEnabled,
		"scrapers":          a.scraperStatus(),
		"probedCount":       probed,
		"probeEnabled":      a.hasProbe(),
		"watchEnabled":      a.cfg.WatchEnabled,
		"watchIntervalSec":  a.cfg.WatchIntervalSec,
		"scanPaused":        a.store.scanPaused(),
		"fileRoot":          a.cfg.FileRoot,
		"thumbnailEnabled":  a.hasFFmpeg(),
		"strmCount":         strms,
		"xiaoyaConfigured":  a.cfg.XiaoyaSource != "",
		"xiaoyaIntervalMin": a.cfg.XiaoyaInterval,
		"license":           a.licenseStatus(),
	})
}

// handleAdminScrape 手动触发 TMDB 刮削。
func (a *App) handleAdminScrape(w http.ResponseWriter, r *http.Request, u *User) {
	if !a.hasScrapers() {
		writeErr(w, http.StatusBadRequest, "未配置任何刮削源（TMDB_API_KEY / DOUBAN_ENABLED），无法刮削")
		return
	}
	go func() {
		n, hit := a.scrapeAll()
		a.store.addActivity("多源刮削", "检查 "+itoa(n)+" 项，更新 "+itoa(hit)+" 项", "MetadataScrape", "Information")
		a.store.Save()
	}()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "刮削已开始"})
}

// handleAdminXiaoyaSync 手动触发 Xiaoya/OpenList STRM 同步。
func (a *App) handleAdminXiaoyaSync(w http.ResponseWriter, r *http.Request, u *User) {
	if a.cfg.XiaoyaSource == "" {
		writeErr(w, http.StatusBadRequest, "未配置 XIAOYA_SOURCE，无法同步")
		return
	}
	go func() {
		n, err := a.syncXiaoya()
		if err != nil {
			a.store.addActivity("Xiaoya 同步", "失败: "+err.Error(), "StrmSync", "Error")
		} else {
			a.store.addActivity("Xiaoya 同步", "生成 "+itoa(n)+" 个 .strm", "StrmSync", "Information")
		}
		a.store.Save()
	}()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "同步已开始"})
}
