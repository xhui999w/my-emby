package main

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type probeJob struct {
	mu      sync.Mutex
	state   string // idle / running
	total   int
	done    int
	skipped int
	failed  int
	started time.Time
	cancel  context.CancelFunc
}

func (j *probeJob) snapshot() map[string]any {
	j.mu.Lock()
	defer j.mu.Unlock()
	return map[string]any{
		"State": j.state, "Total": j.total, "Done": j.done,
		"Skipped": j.skipped, "Failed": j.failed, "StartedAt": j.started.Unix(),
	}
}

func (a *App) probeInfoDir() string {
	if v := strings.TrimSpace(a.store.setting("media_info_dir")); v != "" {
		return v
	}
	return filepath.Join(a.cfg.DataDir, "media-info")
}

func (a *App) probeConcurrency() int {
	n := a.settingInt("probe_concurrency", a.cfg.ProbeConcurrency)
	if n < 1 {
		n = 1
	}
	if n > 16 {
		n = 16
	}
	return n
}

func (a *App) startProbeJob() (bool, string) {
	if !a.hasProbe() {
		return false, "未检测到 ffprobe（或已禁用）"
	}
	a.probeMu.Lock()
	if a.probeJob != nil && a.probeJob.state == "running" {
		a.probeMu.Unlock()
		return false, "已有探测任务在进行"
	}
	ctx, cancel := context.WithCancel(context.Background())
	j := &probeJob{state: "running", started: time.Now(), cancel: cancel}
	a.probeJob = j
	a.probeMu.Unlock()
	go a.runProbeJob(ctx, j)
	return true, "已开始探测"
}

func (a *App) runProbeJob(ctx context.Context, j *probeJob) {
	_ = ensureDir(a.probeInfoDir())
	conc := a.probeConcurrency()

	a.store.mu.RLock()
	var items []*Item
	for _, it := range a.store.Items {
		if (it.Type == "Movie" || it.Type == "Episode") &&
			it.SourcePath != "" && it.StreamURL == "" && !it.Probed {
			items = append(items, it)
		}
	}
	a.store.mu.RUnlock()

	j.mu.Lock()
	j.total = len(items)
	j.mu.Unlock()

	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	stopped := false
	for _, it := range items {
		select {
		case <-ctx.Done():
			stopped = true
		default:
		}
		if stopped {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(it *Item) {
			defer wg.Done()
			defer func() { <-sem }()
			select {
			case <-ctx.Done():
				return
			default:
			}
			pr, err := a.probeFile(it.SourcePath)
			if err != nil || pr == nil {
				j.mu.Lock()
				j.failed++
				j.mu.Unlock()
				return
			}
			a.applyProbe(it, pr)
			j.mu.Lock()
			j.done++
			j.mu.Unlock()
		}(it)
	}
	wg.Wait()
	a.store.Save()
	j.mu.Lock()
	j.state = "idle"
	j.cancel = nil
	done, failed := j.done, j.failed
	j.mu.Unlock()
	a.store.addActivity("媒体信息探测", "探测完成，成功 "+itoa(done)+"，失败 "+itoa(failed), "MediaProbe", "Information")
}

func (a *App) stopProbeJob() {
	a.probeMu.Lock()
	j := a.probeJob
	a.probeMu.Unlock()
	if j == nil {
		return
	}
	j.mu.Lock()
	if j.cancel != nil {
		j.cancel()
		j.cancel = nil
	}
	j.state = "idle"
	j.mu.Unlock()
}

// handleProbeAPI 处理 /admin/probe 与 /admin/probe/*。
func (a *App) handleProbeAPI(w http.ResponseWriter, r *http.Request, u *User) {
	seg := segs(r.URL.Path)
	sub := ""
	if len(seg) > 2 {
		sub = seg[2]
	}
	switch sub {
	case "config":
		a.probeConfig(w, r)
	case "start":
		ok, msg := a.startProbeJob()
		if !ok {
			writeErr(w, http.StatusBadRequest, msg)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": msg})
	case "stop":
		a.stopProbeJob()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case "status":
		a.probeStatus(w)
	default:
		ok, msg := a.startProbeJob()
		if !ok {
			writeErr(w, http.StatusBadRequest, msg)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": msg})
	}
}

func (a *App) probeConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]any{
			"Browse":       a.store.settingBool("extract_browse", true),
			"Playback":     a.store.settingBool("extract_playback", true),
			"Preload":      a.store.settingBool("preload_next", true),
			"SeriesReuse":  a.store.settingBool("series_reuse", true),
			"CDNSpeedtest": a.store.settingBool("cdn_speedtest", false),
			"Persist":      a.store.settingBool("persist_media_info", false),
			"Dir":          a.probeInfoDir(),
			"Concurrency":  a.probeConcurrency(),
			"Realtime":     a.store.settingBool("watch_enabled", a.cfg.WatchEnabled),
			"Available":    a.hasProbe(),
		})
		return
	}
	if r.Method != http.MethodPut {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET/PUT")
		return
	}
	var b struct {
		Browse       *bool
		Playback     *bool
		Preload      *bool
		SeriesReuse  *bool
		CDNSpeedtest *bool
		Persist      *bool
		Dir          *string
		Concurrency  *int
		Realtime     *bool
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
	setB("extract_browse", b.Browse)
	setB("extract_playback", b.Playback)
	setB("preload_next", b.Preload)
	setB("series_reuse", b.SeriesReuse)
	setB("cdn_speedtest", b.CDNSpeedtest)
	setB("persist_media_info", b.Persist)
	setB("watch_enabled", b.Realtime)
	if b.Concurrency != nil {
		n := *b.Concurrency
		if n < 1 {
			n = 1
		}
		if n > 16 {
			n = 16
		}
		a.store.setSetting("probe_concurrency", itoa(n))
	}
	if b.Dir != nil && strings.TrimSpace(*b.Dir) != "" {
		d := strings.TrimSpace(*b.Dir)
		if abs, err := filepath.Abs(d); err == nil {
			d = abs
		}
		a.store.setSetting("media_info_dir", d)
		_ = ensureDir(d)
	}
	go a.store.Save()
	a.store.addActivity("媒体信息设置", "更新媒体信息提取设置", "ProbeConfig", "Information")
	a.probeConfig(w, &http.Request{Method: http.MethodGet})
}

func (a *App) probeStatus(w http.ResponseWriter) {
	a.probeMu.Lock()
	j := a.probeJob
	a.probeMu.Unlock()
	snap := map[string]any{"State": "idle", "Total": 0, "Done": 0, "Skipped": 0, "Failed": 0, "StartedAt": 0}
	if j != nil {
		snap = j.snapshot()
	}
	snap["Concurrency"] = a.probeConcurrency()
	snap["Available"] = a.hasProbe()
	// 等待/进行中近似
	total := toInt(snap["Total"])
	done := toInt(snap["Done"])
	failed := toInt(snap["Failed"])
	waiting := total - done - failed
	if waiting < 0 {
		waiting = 0
	}
	snap["Waiting"] = waiting
	running := a.probeConcurrency()
	if waiting < running {
		running = waiting
	}
	snap["Running"] = running
	writeJSON(w, http.StatusOK, snap)
}

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}
