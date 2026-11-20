package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ---------- 刮削任务控制 ----------

type scrapeJob struct {
	mu      sync.Mutex
	state   string // idle / running / paused
	total   int
	done    int
	started time.Time
	message string
	cancel  context.CancelFunc
}

func (j *scrapeJob) snapshot() map[string]any {
	j.mu.Lock()
	defer j.mu.Unlock()
	return map[string]any{
		"State": j.state, "Total": j.total, "Done": j.done,
		"StartedAt": j.started.Unix(), "Message": j.message,
	}
}

func (a *App) scrapeSelectedSet() map[string]bool {
	raw := strings.TrimSpace(a.store.setting("scrape_providers"))
	if raw == "" {
		return nil
	}
	m := map[string]bool{}
	for _, p := range strings.Split(raw, ",") {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			m[p] = true
		}
	}
	return m
}

func (a *App) scrapeProvidersFiltered() []scraperProvider {
	all := a.scraperProviders()
	sel := a.scrapeSelectedSet()
	if sel == nil {
		return all
	}
	out := make([]scraperProvider, 0, len(all))
	for _, p := range all {
		if sel[p.name] {
			out = append(out, p)
		}
	}
	return out
}

func (a *App) scrapeOverwrite() string {
	switch strings.ToLower(strings.TrimSpace(a.store.setting("scrape_overwrite"))) {
	case "overwrite":
		return "overwrite"
	case "missing":
		return "missing"
	default:
		return "skip"
	}
}

func (a *App) scrapeAutoEnabled() bool {
	return a.store.settingBool("scrape_enabled", true) && a.store.settingBool("scrape_auto", true)
}

// startScrapeJob 启动刮削任务。manual=true 表示手动触发（需允许手动刮削）。
func (a *App) startScrapeJob(manual bool) (bool, string) {
	if !a.store.settingBool("scrape_enabled", true) {
		return false, "刮削未开启"
	}
	if manual && !a.store.settingBool("scrape_manual", true) {
		return false, "手动刮削未开启"
	}
	if len(a.scrapeProvidersFiltered()) == 0 {
		return false, "未配置可用的刮削器（TMDB/豆瓣）"
	}
	a.scrapeMu.Lock()
	if a.scrapeJob != nil && (a.scrapeJob.state == "running" || a.scrapeJob.state == "paused") {
		a.scrapeMu.Unlock()
		return false, "已有刮削任务在进行"
	}
	ctx, cancel := context.WithCancel(context.Background())
	j := &scrapeJob{state: "running", started: time.Now(), cancel: cancel, message: "运行中"}
	a.scrapeJob = j
	a.scrapeMu.Unlock()
	go a.runScrapeJob(ctx, j)
	return true, "已开始刮削"
}

func (a *App) runScrapeJob(ctx context.Context, j *scrapeJob) {
	policy := a.scrapeOverwrite()
	providers := a.scrapeProvidersFiltered()

	a.store.mu.RLock()
	items := make([]*Item, 0)
	for _, it := range a.store.Items {
		if it.Type == "Movie" {
			if !a.scrapeCategoryEnabled("Movie") {
				continue
			}
		} else if it.Type == "Series" {
			if !a.scrapeCategoryEnabled("Series") {
				continue
			}
		} else {
			continue
		}
		items = append(items, it)
	}
	a.store.mu.RUnlock()

	j.mu.Lock()
	j.total = len(items)
	j.mu.Unlock()

	enriched := 0
	for _, it := range items {
		// 暂停/停止检查
		for {
			j.mu.Lock()
			st, paused := j.state, j.state == "paused"
			j.mu.Unlock()
			if st == "idle" {
				return
			}
			if !paused {
				break
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(300 * time.Millisecond):
			}
		}
		select {
		case <-ctx.Done():
			return
		default:
		}

		need := false
		switch policy {
		case "overwrite":
			need = true
		case "missing":
			need = it.Overview == "" || !it.HasPoster || it.ProductionYear == 0
		default: // skip
			need = !a.scraperSatisfied(it)
		}
		if need {
			changed := false
			for _, p := range providers {
				if policy != "overwrite" && a.scraperSatisfied(it) {
					break
				}
				if p.enrich(a, it) {
					changed = true
				}
				select {
				case <-ctx.Done():
					return
				default:
				}
				time.Sleep(120 * time.Millisecond)
			}
			if changed {
				enriched++
			}
		}
		j.mu.Lock()
		j.done++
		j.mu.Unlock()
	}
	a.store.Save()
	j.mu.Lock()
	j.state = "idle"
	j.message = "完成：更新 " + itoa(enriched) + " 项"
	j.cancel = nil
	j.mu.Unlock()
	a.store.addActivity("刮削任务", "刮削完成，更新 "+itoa(enriched)+" 项", "MetadataScrape", "Information")
}

func (a *App) stopScrapeJob() {
	a.scrapeMu.Lock()
	j := a.scrapeJob
	a.scrapeMu.Unlock()
	if j == nil {
		return
	}
	j.mu.Lock()
	j.state = "idle"
	j.message = "已停止"
	if j.cancel != nil {
		j.cancel()
		j.cancel = nil
	}
	j.mu.Unlock()
}

func (a *App) pauseScrapeJob() {
	a.scrapeMu.Lock()
	j := a.scrapeJob
	a.scrapeMu.Unlock()
	if j == nil {
		return
	}
	j.mu.Lock()
	if j.state == "running" {
		j.state = "paused"
	}
	j.mu.Unlock()
}

func (a *App) resumeScrapeJob() {
	a.scrapeMu.Lock()
	j := a.scrapeJob
	a.scrapeMu.Unlock()
	if j == nil {
		return
	}
	j.mu.Lock()
	if j.state == "paused" {
		j.state = "running"
	}
	j.mu.Unlock()
}

// ---------- HTTP 处理 ----------

// handleScrapeAPI 处理 /admin/scrape 与 /admin/scrape/*。
func (a *App) handleScrapeAPI(w http.ResponseWriter, r *http.Request, u *User) {
	seg := segs(r.URL.Path)
	sub := ""
	if len(seg) > 2 {
		sub = seg[2]
	}
	switch sub {
	case "config":
		a.scrapeConfig(w, r)
	case "start":
		ok, msg := a.startScrapeJob(true)
		if !ok {
			writeErr(w, http.StatusBadRequest, msg)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": msg})
	case "stop":
		a.stopScrapeJob()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case "pause":
		a.pauseScrapeJob()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case "resume":
		a.resumeScrapeJob()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case "retry":
		a.stopScrapeJob()
		time.Sleep(100 * time.Millisecond)
		ok, msg := a.startScrapeJob(true)
		if !ok {
			writeErr(w, http.StatusBadRequest, msg)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": msg})
	case "status":
		a.scrapeStatus(w)
	case "media-config":
		a.scrapeMediaConfigAPI(w, r)
	default:
		// 兼容旧的 /admin/scrape 直接触发
		ok, msg := a.startScrapeJob(true)
		if !ok {
			writeErr(w, http.StatusBadRequest, msg)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": msg})
	}
}

func (a *App) scrapeConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		avail := a.scraperStatus()
		sel := a.scrapeSelectedSet()
		providers := []string{}
		if sel == nil {
			for name, on := range avail {
				if on {
					providers = append(providers, name)
				}
			}
		} else {
			for name := range sel {
				providers = append(providers, name)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"Enabled":   a.store.settingBool("scrape_enabled", true),
			"Auto":      a.store.settingBool("scrape_auto", true),
			"Manual":    a.store.settingBool("scrape_manual", true),
			"Providers": providers,
			"Available": avail,
			"Overwrite": a.scrapeOverwrite(),
		})
		return
	}
	if r.Method != http.MethodPut {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET/PUT")
		return
	}
	var b struct {
		Enabled   *bool
		Auto      *bool
		Manual    *bool
		Providers *[]string
		Overwrite *string
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体无效")
		return
	}
	if b.Enabled != nil {
		a.store.setSetting("scrape_enabled", boolStr(*b.Enabled))
	}
	if b.Auto != nil {
		a.store.setSetting("scrape_auto", boolStr(*b.Auto))
	}
	if b.Manual != nil {
		a.store.setSetting("scrape_manual", boolStr(*b.Manual))
	}
	if b.Providers != nil {
		a.store.setSetting("scrape_providers", strings.Join(*b.Providers, ","))
	}
	if b.Overwrite != nil {
		ov := strings.ToLower(strings.TrimSpace(*b.Overwrite))
		if ov != "overwrite" && ov != "missing" {
			ov = "skip"
		}
		a.store.setSetting("scrape_overwrite", ov)
	}
	go a.store.Save()
	a.store.addActivity("刮削设置", "更新刮削配置", "ScrapeConfig", "Information")
	a.scrapeConfig(w, &http.Request{Method: http.MethodGet})
}

func (a *App) scrapeStatus(w http.ResponseWriter) {
	a.scrapeMu.Lock()
	j := a.scrapeJob
	a.scrapeMu.Unlock()
	snap := map[string]any{"State": "idle", "Total": 0, "Done": 0, "StartedAt": 0, "Message": ""}
	if j != nil {
		snap = j.snapshot()
	}
	// 任务日志：最近刮削相关活动
	a.store.mu.RLock()
	logs := make([]Activity, 0, 50)
	for _, e := range a.store.Activity {
		if e.Type == "MetadataScrape" || e.Type == "ScrapeConfig" {
			logs = append(logs, e)
			if len(logs) >= 50 {
				break
			}
		}
	}
	a.store.mu.RUnlock()
	snap["Logs"] = logs
	writeJSON(w, http.StatusOK, snap)
}

func defaultScrapeMediaConfig() map[string]any {
	return map[string]any{
		"FanartKey":       "",
		"SubtitleChinese": true,
		"PosterChinese":   true,
		"Categories": map[string]any{
			"Series":  map[string]any{"Enabled": true, "NFO": true, "Poster": true, "Backdrop": false, "Logo": false, "Thumb": false, "Landscape": false},
			"Movie":   map[string]any{"Enabled": true, "NFO": true, "Poster": true, "Backdrop": false, "Logo": false, "Disc": false, "Thumb": false, "Landscape": false},
			"Season":  map[string]any{"Enabled": true, "NFO": true, "Poster": true, "Thumb": false, "Landscape": false, "Backdrop": false, "Logo": false},
			"Episode": map[string]any{"Enabled": true, "NFO": true, "Thumb": false},
		},
	}
}

func (a *App) scrapeMediaConfig() map[string]any {
	raw := a.store.setting("scrape_media_config")
	if strings.TrimSpace(raw) == "" {
		return defaultScrapeMediaConfig()
	}
	var v map[string]any
	if json.Unmarshal([]byte(raw), &v) == nil {
		return v
	}
	return defaultScrapeMediaConfig()
}

func (a *App) scrapeCategoryEnabled(kind string) bool {
	cfg := a.scrapeMediaConfig()
	cats, _ := cfg["Categories"].(map[string]any)
	if cats == nil {
		return true
	}
	c, _ := cats[kind].(map[string]any)
	if c == nil {
		return true
	}
	if b, ok := c["Enabled"].(bool); ok {
		return b
	}
	return true
}

func (a *App) scrapeMediaConfigAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, a.scrapeMediaConfig())
		return
	}
	if r.Method != http.MethodPut {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET/PUT")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil || !json.Valid(raw) {
		writeErr(w, http.StatusBadRequest, "请求体无效")
		return
	}
	a.store.setSetting("scrape_media_config", string(raw))
	go a.store.Save()
	a.store.addActivity("刮削设置", "更新分类刮削明细", "ScrapeConfig", "Information")
	writeJSON(w, http.StatusOK, a.scrapeMediaConfig())
}
