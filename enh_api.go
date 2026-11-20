package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ---------- 通用小工具 ----------

func clearDirContents(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, nil
	}
	n := 0
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err == nil {
			n++
		}
	}
	return n, nil
}

// ---------- 代理 ----------

func (a *App) proxyScopeOn(scope string) bool {
	list := splitCSV(a.store.setting("proxy_scope"))
	if len(list) == 0 {
		return true
	}
	for _, s := range list {
		if s == scope {
			return true
		}
	}
	return false
}

// clientFor 返回执行外部请求用的 HTTP 客户端；代理开启且范围匹配时走代理。
func (a *App) clientFor(scope string) *http.Client {
	if !a.store.settingBool("proxy_enabled", false) {
		return a.httpClient
	}
	if !a.proxyScopeOn(scope) {
		return a.httpClient
	}
	addr := strings.TrimSpace(a.store.setting("proxy_addr"))
	if addr == "" {
		return a.httpClient
	}
	t := strings.ToLower(strings.TrimSpace(a.store.setting("proxy_type")))
	if t == "" {
		t = "http"
	}
	pu, err := url.Parse(addr)
	if err != nil || pu.Host == "" {
		pu = &url.URL{Host: addr}
	}
	if t == "socks5" || t == "socks" {
		pu.Scheme = "socks5"
	} else {
		pu.Scheme = "http"
	}
	if u := strings.TrimSpace(a.store.setting("proxy_user")); u != "" {
		pu.User = url.UserPassword(u, a.store.setting("proxy_pass"))
	}
	tr := &http.Transport{Proxy: http.ProxyURL(pu)}
	return &http.Client{Timeout: 60 * time.Second, Transport: tr}
}

func (a *App) handleProxyConfigAPI(w http.ResponseWriter, r *http.Request, u *User) {
	seg := segs(r.URL.Path)
	sub := ""
	if len(seg) > 2 {
		sub = seg[2]
	}
	if sub == "test" && r.Method == http.MethodPost {
		c := a.clientFor("other")
		if !a.store.settingBool("proxy_enabled", false) {
			writeErr(w, http.StatusBadRequest, "代理未启用")
			return
		}
		resp, err := c.Get("https://api.themoviedb.org/3/configuration")
		if err != nil {
			writeErr(w, http.StatusBadRequest, "连接失败: "+err.Error())
			return
		}
		_ = resp.Body.Close()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": resp.StatusCode})
		return
	}
	if r.Method == http.MethodGet {
		a.proxyConfigRespond(w)
		return
	}
	if r.Method != http.MethodPut {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET/PUT")
		return
	}
	var b struct {
		Enabled *bool
		Type    *string
		Addr    *string
		User    *string
		Pass    *string
		Scope   map[string]bool
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体无效")
		return
	}
	if b.Enabled != nil {
		a.store.setSetting("proxy_enabled", boolStr(*b.Enabled))
	}
	if b.Type != nil {
		a.store.setSetting("proxy_type", strings.TrimSpace(*b.Type))
	}
	if b.Addr != nil {
		a.store.setSetting("proxy_addr", strings.TrimSpace(*b.Addr))
	}
	if b.User != nil {
		a.store.setSetting("proxy_user", strings.TrimSpace(*b.User))
	}
	if b.Pass != nil {
		a.store.setSetting("proxy_pass", *b.Pass)
	}
	if b.Scope != nil {
		var on []string
		for _, k := range []string{"tmdb", "tg", "subtitle", "update", "license", "other"} {
			if b.Scope[k] {
				on = append(on, k)
			}
		}
		a.store.setSetting("proxy_scope", strings.Join(on, ","))
	}
	go a.store.Save()
	a.store.addActivity("代理设置", "更新代理配置", "ProxyConfig", "Information")
	a.proxyConfigRespond(w)
}

func (a *App) proxyConfigRespond(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]any{
		"Enabled": a.store.settingBool("proxy_enabled", false),
		"Type":    firstNonEmpty(a.store.setting("proxy_type"), "http"),
		"Addr":    a.store.setting("proxy_addr"),
		"User":    a.store.setting("proxy_user"),
		"Pass":    a.store.setting("proxy_pass"),
		"Scope": map[string]bool{
			"Tmdb":     a.proxyScopeOn("tmdb"),
			"Tg":       a.proxyScopeOn("tg"),
			"Subtitle": a.proxyScopeOn("subtitle"),
			"Update":   a.proxyScopeOn("update"),
			"License":  a.proxyScopeOn("license"),
			"Other":    a.proxyScopeOn("other"),
		},
	})
}

// ---------- Telegram Bot ----------

func (a *App) handleTgConfigAPI(w http.ResponseWriter, r *http.Request, u *User) {
	seg := segs(r.URL.Path)
	sub := ""
	if len(seg) > 2 {
		sub = seg[2]
	}
	if sub == "test" && r.Method == http.MethodPost {
		token := strings.TrimSpace(a.store.setting("tg_token"))
		chat := strings.TrimSpace(a.store.setting("tg_chat_id"))
		if token == "" || chat == "" {
			writeErr(w, http.StatusBadRequest, "请先填写 Bot Token 和 User/Chat ID")
			return
		}
		api := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
		payload, _ := json.Marshal(map[string]string{"chat_id": chat, "text": "go-emby 测试消息 ✅"})
		req, _ := http.NewRequest(http.MethodPost, api, strings.NewReader(string(payload)))
		req.Header.Set("Content-Type", "application/json")
		resp, err := a.clientFor("tg").Do(req)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "发送失败: "+err.Error())
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("Telegram 返回 %d", resp.StatusCode))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "测试消息已发送"})
		return
	}
	if r.Method == http.MethodGet {
		a.tgConfigRespond(w)
		return
	}
	if r.Method != http.MethodPut {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET/PUT")
		return
	}
	var b struct {
		Enabled *bool
		Token   *string
		ChatID  *string
		Notify  *bool
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体无效")
		return
	}
	if b.Enabled != nil {
		a.store.setSetting("tg_enabled", boolStr(*b.Enabled))
	}
	if b.Token != nil {
		a.store.setSetting("tg_token", strings.TrimSpace(*b.Token))
	}
	if b.ChatID != nil {
		a.store.setSetting("tg_chat_id", strings.TrimSpace(*b.ChatID))
	}
	if b.Notify != nil {
		a.store.setSetting("tg_notify", boolStr(*b.Notify))
	}
	go a.store.Save()
	a.store.addActivity("Telegram Bot", "更新 Telegram 配置", "TgConfig", "Information")
	a.tgConfigRespond(w)
}

func (a *App) tgConfigRespond(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]any{
		"Enabled": a.store.settingBool("tg_enabled", false),
		"Token":   a.store.setting("tg_token"),
		"ChatID":  a.store.setting("tg_chat_id"),
		"Notify":  a.store.settingBool("tg_notify", false),
	})
}

// ---------- TMDB 配置 ----------

func (a *App) tmdbCacheDir() string {
	if v := strings.TrimSpace(a.store.setting("tmdb_cache_dir")); v != "" {
		return v
	}
	return filepath.Join(a.cfg.DataDir, "tmdb")
}

func (a *App) handleTmdbConfigAPI(w http.ResponseWriter, r *http.Request, u *User) {
	seg := segs(r.URL.Path)
	sub := ""
	if len(seg) > 2 {
		sub = seg[2]
	}
	if sub == "clear" && r.Method == http.MethodPost {
		n, _ := clearDirContents(a.tmdbCacheDir())
		a.store.addActivity("TMDB", "清除缓存 "+itoa(n)+" 项", "TmdbConfig", "Information")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "cleared": n})
		return
	}
	if r.Method == http.MethodGet {
		a.tmdbConfigRespond(w)
		return
	}
	if r.Method != http.MethodPut {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET/PUT")
		return
	}
	var b struct {
		Base     *string
		Key      *string
		CacheDir *string
		Rate     *int
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体无效")
		return
	}
	if b.Base != nil {
		a.store.setSetting("tmdb_base", strings.TrimSpace(*b.Base))
	}
	if b.Key != nil {
		a.store.setSetting("tmdb_api_key", strings.TrimSpace(*b.Key))
	}
	if b.CacheDir != nil && strings.TrimSpace(*b.CacheDir) != "" {
		d := strings.TrimSpace(*b.CacheDir)
		if abs, err := filepath.Abs(d); err == nil {
			d = abs
		}
		a.store.setSetting("tmdb_cache_dir", d)
		_ = ensureDir(d)
	}
	if b.Rate != nil {
		n := *b.Rate
		if n < 1 {
			n = 1
		}
		if n > 60 {
			n = 60
		}
		a.store.setSetting("tmdb_rate", itoa(n))
	}
	go a.store.Save()
	a.store.addActivity("TMDB", "更新 TMDB 配置", "TmdbConfig", "Information")
	a.tmdbConfigRespond(w)
}

func (a *App) tmdbConfigRespond(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]any{
		"Base":     a.effectiveTMDBBase(),
		"Key":      a.effectiveTMDBKey(),
		"CacheDir": a.tmdbCacheDir(),
		"Rate":     a.tmdbRate(),
	})
}

// ---------- 字幕增强 ----------

func (a *App) subtitleCacheDir() string {
	if v := strings.TrimSpace(a.store.setting("subtitle_cache_dir")); v != "" {
		return v
	}
	return filepath.Join(a.cfg.DataDir, "subtitles")
}

func (a *App) handleSubConfigAPI(w http.ResponseWriter, r *http.Request, u *User) {
	seg := segs(r.URL.Path)
	sub := ""
	if len(seg) > 2 {
		sub = seg[2]
	}
	if sub == "clear" && r.Method == http.MethodPost {
		n, _ := clearDirContents(a.subtitleCacheDir())
		a.store.addActivity("字幕", "清除字幕缓存 "+itoa(n)+" 项", "SubtitleConfig", "Information")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "cleared": n})
		return
	}
	if r.Method == http.MethodGet {
		a.subConfigRespond(w)
		return
	}
	if r.Method != http.MethodPut {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET/PUT")
		return
	}
	var b struct {
		AutoMatch   *bool
		AutoClean   *bool
		SaveToVideo *bool
		Token       *string
		CacheDir    *string
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体无效")
		return
	}
	if b.AutoMatch != nil {
		a.store.setSetting("subtitle_auto_match", boolStr(*b.AutoMatch))
	}
	if b.AutoClean != nil {
		a.store.setSetting("subtitle_auto_clean", boolStr(*b.AutoClean))
	}
	if b.SaveToVideo != nil {
		a.store.setSetting("subtitle_save_video", boolStr(*b.SaveToVideo))
	}
	if b.Token != nil {
		a.store.setSetting("assrt_token", strings.TrimSpace(*b.Token))
	}
	if b.CacheDir != nil && strings.TrimSpace(*b.CacheDir) != "" {
		d := strings.TrimSpace(*b.CacheDir)
		if abs, err := filepath.Abs(d); err == nil {
			d = abs
		}
		a.store.setSetting("subtitle_cache_dir", d)
		_ = ensureDir(d)
	}
	go a.store.Save()
	a.store.addActivity("字幕", "更新字幕增强配置", "SubtitleConfig", "Information")
	a.subConfigRespond(w)
}

func (a *App) subConfigRespond(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]any{
		"AutoMatch":   a.store.settingBool("subtitle_auto_match", false),
		"AutoClean":   a.store.settingBool("subtitle_auto_clean", false),
		"SaveToVideo": a.store.settingBool("subtitle_save_video", false),
		"Token":       a.store.setting("assrt_token"),
		"CacheDir":    a.subtitleCacheDir(),
	})
}

// ---------- 片头片尾 ----------

func (a *App) introDir() string {
	if v := strings.TrimSpace(a.store.setting("intro_dir")); v != "" {
		return v
	}
	return filepath.Join(a.cfg.DataDir, "intro-credits")
}

func (a *App) handleIntroConfigAPI(w http.ResponseWriter, r *http.Request, u *User) {
	seg := segs(r.URL.Path)
	sub := ""
	if len(seg) > 2 {
		sub = seg[2]
	}
	if sub == "clear" && r.Method == http.MethodPost {
		n, _ := clearDirContents(a.introDir())
		a.store.addActivity("片头片尾", "清除学习记录 "+itoa(n)+" 项", "IntroConfig", "Information")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "cleared": n})
		return
	}
	if r.Method == http.MethodGet {
		a.introConfigRespond(w)
		return
	}
	if r.Method != http.MethodPut {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET/PUT")
		return
	}
	var b struct {
		Enabled      *bool
		LearnIntro   *bool
		LearnCredits *bool
		SkipIntro    *bool
		SkipCredits  *bool
		Window       *int
		MinSamples   *int
		Tolerance    *int
		Dir          *string
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
	setB("intro_enabled", b.Enabled)
	setB("intro_learn_intro", b.LearnIntro)
	setB("intro_learn_credits", b.LearnCredits)
	setB("intro_skip_intro", b.SkipIntro)
	setB("intro_skip_credits", b.SkipCredits)
	if b.Window != nil {
		a.store.setSetting("intro_window", itoa(*b.Window))
	}
	if b.MinSamples != nil {
		a.store.setSetting("intro_min_samples", itoa(*b.MinSamples))
	}
	if b.Tolerance != nil {
		a.store.setSetting("intro_tolerance", itoa(*b.Tolerance))
	}
	if b.Dir != nil && strings.TrimSpace(*b.Dir) != "" {
		d := strings.TrimSpace(*b.Dir)
		if abs, err := filepath.Abs(d); err == nil {
			d = abs
		}
		a.store.setSetting("intro_dir", d)
		_ = ensureDir(d)
	}
	go a.store.Save()
	a.store.addActivity("片头片尾", "更新片头片尾配置", "IntroConfig", "Information")
	a.introConfigRespond(w)
}

func (a *App) introConfigRespond(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]any{
		"Enabled":      a.store.settingBool("intro_enabled", false),
		"LearnIntro":   a.store.settingBool("intro_learn_intro", true),
		"LearnCredits": a.store.settingBool("intro_learn_credits", true),
		"SkipIntro":    a.store.settingBool("intro_skip_intro", false),
		"SkipCredits":  a.store.settingBool("intro_skip_credits", false),
		"Window":       a.settingInt("intro_window", 10),
		"MinSamples":   a.settingInt("intro_min_samples", 3),
		"Tolerance":    a.settingInt("intro_tolerance", 15),
		"Dir":          a.introDir(),
	})
}

var _ = io.Discard
