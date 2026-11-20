package main

import (
	"context"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

type App struct {
	cfg        Config
	store      *Store
	httpClient *http.Client
	license    *licenseClient

	// 扫描队列
	scanQueue   chan string
	scanQMu     sync.Mutex
	scanPending map[string]bool

	// 文件监听快照
	watchMu   sync.Mutex
	watchSnap map[string]fileStamp
}

func main() {
	cfg := loadConfig()
	store := NewStore(cfg)
	app := &App{cfg: cfg, store: store, httpClient: &http.Client{Timeout: 60 * time.Second}, license: newLicenseClient(cfg)}
	go app.license.run(context.Background())

	// 扫描 worker + 实时监听；初次全量扫描通过队列触发（随后自动刮削/探测）。
	app.startScanWorker()
	app.startWatcher()
	app.enqueueScan("")
	// Xiaoya/OpenList STRM 定时同步（配置了才启用）
	app.startXiaoyaScheduler()
	if cfg.XiaoyaSource != "" {
		go func() {
			if _, err := app.syncXiaoya(); err != nil {
				log.Printf("启动时 Xiaoya 同步失败: %v", err)
			}
		}()
	}

	addr := ":" + cfg.Port
	srv := &http.Server{
		Addr:              addr,
		Handler:           app,
		ReadHeaderTimeout: 15 * time.Second,
	}
	log.Printf("Go-Emby(复刻版) %s 监听 %s", serverVersion, addr)
	log.Printf("Web 管理/海报墙: http://<server-ip>%s/", addr)
	log.Printf("Emby 客户端连接地址: http://<server-ip>%s", addr)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

// ServeHTTP 记录 4xx/5xx 错误日志后委派给 serve。
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if a.licenseBlocked(w, r) {
		return
	}
	sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
	a.serve(sw, r)
	if sw.status >= 400 && r.Method != http.MethodOptions {
		a.recordHTTPError(r, sw.status, sw.detail.String())
	}
}

func (a *App) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	path := r.URL.Path
	path = normalizePath(path)
	r.URL.Path = path

	// ---- 大小写/前缀兼容的发现与登录端点（/emby、重复斜杠、小写路径） ----
	switch strings.ToLower(path) {
	case "/system/ping":
		a.handleSystemPing(w, r)
		return
	case "/system/info/public":
		a.handleSystemInfoPublic(w, r)
		return
	case "/users/authenticatebyname":
		a.handleAuthenticate(w, r)
		return
	case "/users/me":
		a.store.requireAuth(a.handleUsersMe)(w, r)
		return
	case "/system/info":
		a.store.requireAuth(a.handleSystemInfo)(w, r)
		return
	case "/system/configuration":
		a.store.requireAuth(a.handleSystemConfiguration)(w, r)
		return
	case "/library/refresh":
		a.store.requireAuth(a.handleRefresh)(w, r)
		return
	case "/library/mediafolders":
		a.store.requireAuth(a.handleMediaFolders)(w, r)
		return
	case "/sessions":
		a.store.requireAuth(a.handleSessions)(w, r)
		return
	case "/system/activitylog/entries":
		a.store.requireAuth(a.handleActivityLog)(w, r)
		return
	case "/healthz", "/health":
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "serverId": a.store.ServerId})
		return
	}

	// ---- 精确路由 ----
	switch path {
	case "/license/status":
		a.handleLicenseStatus(w, r)
		return
	case "/license/refresh":
		a.handleLicenseRefresh(w, r)
		return
	case "/", "/web", "/web/", "/web/index.html", "/index.html":
		a.handleWeb(w, r)
		return
	case "/healthz", "/health":
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "serverId": a.store.ServerId})
		return
	case "/System/Info/Public":
		a.handleSystemInfoPublic(w, r)
		return
	case "/System/Ping":
		a.handleSystemPing(w, r)
		return
	case "/System/Info":
		a.store.requireAuth(a.handleSystemInfo)(w, r)
		return
	case "/System/Configuration":
		a.store.requireAuth(a.handleSystemConfiguration)(w, r)
		return
	case "/System/Configuration/Public":
		writeJSON(w, http.StatusOK, map[string]any{"ServerName": a.cfg.ServerName})
		return
	case "/Users/AuthenticateByName":
		a.handleAuthenticate(w, r)
		return
	case "/Users/Me":
		a.store.requireAuth(a.handleUsersMe)(w, r)
		return
	case "/Branding/Configuration", "/Branding/Splashscreen":
		a.handleBranding(w, r)
		return
	case "/Localization/Options":
		a.handleLocalizationOptions(w, r)
		return
	case "/Library/Refresh":
		a.store.requireAuth(a.handleRefresh)(w, r)
		return
	case "/Library/MediaFolders":
		a.store.requireAuth(a.handleMediaFolders)(w, r)
		return
	case "/Library/VirtualFolders":
		a.store.requireAuth(a.handleVirtualFolders)(w, r)
		return
	case "/Sessions":
		a.store.requireAuth(a.handleSessions)(w, r)
		return
	case "/Items":
		u := a.store.userByToken(r)
		if u == nil {
			writeErr(w, http.StatusUnauthorized, "认证失败")
			return
		}
		a.queryItems(w, r, u, u.Id)
		return
	case "/Items/Filters", "/Items/Filters2":
		a.store.requireAuth(a.handleFilters)(w, r)
		return
	case "/Genres":
		a.store.requireAuth(a.handleGenres)(w, r)
		return
	case "/Persons":
		a.store.requireAuth(a.handlePersons)(w, r)
		return
	case "/Studios":
		a.store.requireAuth(a.handleStudios)(w, r)
		return
	case "/Years":
		a.store.requireAuth(a.handleYears)(w, r)
		return
	case "/Search/Hints":
		a.store.requireAuth(a.handleSearchHints)(w, r)
		return
	case "/System/Endpoint":
		a.handleSystemEndpoint(w, r)
		return
	case "/System/ActivityLog/Entries":
		a.store.requireAuth(a.handleActivityLog)(w, r)
		return
	case "/ScheduledTasks":
		a.store.requireAuth(a.handleScheduledTasks)(w, r)
		return
	case "/admin/stats":
		a.store.requireAuth(a.handleAdminStats)(w, r)
		return
	case "/admin/scrape":
		a.store.requireAdmin(a.handleAdminScrape)(w, r)
		return
	case "/admin/probe":
		a.store.requireAdmin(a.handleAdminProbe)(w, r)
		return
	case "/admin/scan":
		a.store.requireAdmin(a.handleScanControl)(w, r)
		return
	case "/admin/enhancements":
		a.store.requireAdmin(a.handleEnhancements)(w, r)
		return
	case "/admin/logs":
		a.store.requireAdmin(a.handleActivityLog)(w, r)
		return
	case "/admin/xiaoya/sync":
		a.store.requireAdmin(a.handleAdminXiaoyaSync)(w, r)
		return
	case "/GET/Webhooks", "/Plugins", "/Packages", "/DisplayPreferences":
		writeJSON(w, http.StatusOK, []any{})
		return
	}

	// ---- 前缀路由 ----
	seg := segs(path)
	switch {
	case path == "/api/files" || strings.HasPrefix(path, "/api/files/"):
		a.store.requireAdmin(a.handleFiles)(w, r)
	case path == "/Users" || strings.HasPrefix(path, "/Users/"):
		a.routeUsers(w, r, seg)
	case strings.HasPrefix(path, "/Items/"):
		a.routeItems(w, r, seg)
	case strings.HasPrefix(path, "/Shows/"), strings.HasPrefix(path, "/shows/"):
		a.routeShows(w, r, seg)
	case strings.HasPrefix(path, "/DisplayPreferences/"):
		a.store.requireAuth(func(w2 http.ResponseWriter, r2 *http.Request, u2 *User) {
			a.handleDisplayPreferences(w2, r2, u2, seg[1])
		})(w, r)
	case strings.HasPrefix(path, "/Videos/"), strings.HasPrefix(path, "/Audio/"):
		a.routeStream(w, r, seg)
	case strings.HasPrefix(path, "/Sessions/Playing/Stopped"):
		a.store.requireAuth(a.handlePlaybackStopped)(w, r)
	case strings.HasPrefix(path, "/Sessions/Playing"):
		a.store.requireAuth(a.handlePlaybackProgress)(w, r)
	case strings.HasPrefix(path, "/Sessions/"):
		a.store.requireAuth(a.handleSessions)(w, r)
	default:
		// 对未知的 GET 接口宽松返回空对象，提升第三方客户端兼容性。
		if r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, map[string]any{})
			return
		}
		writeErr(w, http.StatusNotFound, "未知接口: "+path)
	}
}

func (a *App) routeUsers(w http.ResponseWriter, r *http.Request, seg []string) {
	u := a.store.userByToken(r)
	if u == nil {
		writeErr(w, http.StatusUnauthorized, "认证失败")
		return
	}
	if len(seg) < 2 {
		// /Users
		if r.Method == http.MethodPost {
			if !u.IsAdmin {
				writeErr(w, http.StatusForbidden, "需要管理员权限")
				return
			}
			a.handleCreateUser(w, r, u)
			return
		}
		a.handleGetUsers(w, r, u)
		return
	}
	id := seg[1]
	if len(seg) == 2 {
		switch r.Method {
		case http.MethodDelete:
			if !u.IsAdmin {
				writeErr(w, http.StatusForbidden, "需要管理员权限")
				return
			}
			a.handleDeleteUser(w, r, u, id)
		default:
			a.handleGetUser(w, r, u, id)
		}
		return
	}
	sub := strings.ToLower(seg[2])
	switch sub {
	case "views":
		a.handleUserViews(w, r, u)
	case "items":
		if len(seg) >= 4 && strings.EqualFold(seg[3], "Latest") {
			a.handleLatest(w, r, u, id)
			return
		}
		a.queryItems(w, r, u, id)
	case "images":
		imageType := ""
		if len(seg) >= 4 {
			imageType = seg[3]
		}
		a.handleItemImage(w, r, u, id, imageType)
	case "policy":
		if !u.IsAdmin {
			writeErr(w, http.StatusForbidden, "需要管理员权限")
			return
		}
		a.handleUpdatePolicy(w, r, u, id)
	default:
		writeJSON(w, http.StatusOK, map[string]any{})
	}
}

func (a *App) routeItems(w http.ResponseWriter, r *http.Request, seg []string) {
	u := a.store.userByToken(r)
	if u == nil {
		writeErr(w, http.StatusUnauthorized, "认证失败")
		return
	}
	if len(seg) < 2 {
		writeErr(w, http.StatusNotFound, "缺少条目 id")
		return
	}
	id := seg[1]
	if len(seg) == 2 {
		switch r.Method {
		case http.MethodPut:
			a.handleItemPut(w, r, u, id)
		case http.MethodDelete:
			a.handleItemDelete(w, r, u, id)
		default:
			a.handleItem(w, r, u, id)
		}
		return
	}
	switch strings.ToLower(seg[2]) {
	case "images":
		imageType := ""
		if len(seg) >= 4 {
			imageType = seg[3]
		}
		if r.Method == http.MethodPost || r.Method == http.MethodPut {
			a.handleItemImageUpload(w, r, u, id, imageType)
		} else {
			a.handleItemImage(w, r, u, id, imageType)
		}
	case "playbackinfo":
		a.handlePlaybackInfo(w, r, u, id)
	case "similar":
		a.handleSimilar(w, r, u, id)
	case "download":
		if !a.authorizeDownload(w, r, u) {
			return
		}
		a.handleStream(w, r, u, id)
	case "refresh":
		a.handleRefresh(w, r, u)
	case "specialfeatures", "intros", "localtrailers", "thememedia", "themesongs", "themevideos":
		writeJSON(w, http.StatusOK, map[string]any{"Items": []any{}, "TotalRecordCount": 0})
	default:
		writeJSON(w, http.StatusOK, map[string]any{})
	}
}

// routeShows 处理 Infuse 等客户端的 /Shows/{id}/Episodes 与 /Shows/{id}/Seasons。
func (a *App) routeShows(w http.ResponseWriter, r *http.Request, seg []string) {
	u := a.store.userByToken(r)
	if u == nil {
		writeErr(w, http.StatusUnauthorized, "认证失败")
		return
	}
	if len(seg) < 3 {
		writeErr(w, http.StatusNotFound, "缺少参数")
		return
	}
	id := seg[1]
	switch strings.ToLower(seg[2]) {
	case "episodes":
		a.returnItemsByParent(w, r, u, id, "Episode")
	case "seasons":
		a.returnItemsByParent(w, r, u, id, "Season")
	default:
		writeErr(w, http.StatusNotFound, "未知子资源")
	}
}

// returnItemsByParent 返回某父级下的所有指定类型子项（递归）。
func (a *App) returnItemsByParent(w http.ResponseWriter, r *http.Request, u *User, parentID, itemType string) {
	all := a.descendants(parentID)
	out := make([]any, 0, len(all))
	for _, it := range all {
		if it.Type == itemType {
			out = append(out, a.itemWithUserData(it, u))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"Items": out, "TotalRecordCount": len(out)})
}

func (a *App) routeStream(w http.ResponseWriter, r *http.Request, seg []string) {
	u := a.store.userByToken(r)
	if u == nil {
		writeErr(w, http.StatusUnauthorized, "认证失败")
		return
	}
	// 外挂字幕：/Videos/{id}/{srcId}/Subtitles/{index}/Stream.{ext}
	if itemID, idx, ext, ok := parseSubtitlePath(seg); ok {
		if u.PlaybackDisabled {
			writeErr(w, http.StatusForbidden, "该用户已被禁止播放")
			return
		}
		a.handleSubtitle(w, r, u, itemID, idx, ext)
		return
	}
	if !a.authorizePlayback(w, r, u) {
		return
	}
	if len(seg) < 2 {
		writeErr(w, http.StatusNotFound, "缺少条目 id")
		return
	}
	id := seg[1]
	if it := a.getItem(id); it != nil {
		a.recordPlayback(u, it)
	}
	a.handleStream(w, r, u, id)
}

func (a *App) getItem(id string) *Item {
	a.store.mu.RLock()
	defer a.store.mu.RUnlock()
	return a.store.Items[id]
}
