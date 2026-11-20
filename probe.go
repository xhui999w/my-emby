package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type probeStream struct {
	Index     int    `json:"index"`
	CodecType string `json:"codec_type"`
	CodecName string `json:"codec_name"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Channels  int    `json:"channels"`
	Profile   string `json:"profile"`
	BitRate   string `json:"bit_rate"`
	Tags      struct {
		Language string `json:"language"`
		Title    string `json:"title"`
	} `json:"tags"`
}

type probeFormat struct {
	Duration   string `json:"duration"`
	Size       string `json:"size"`
	FormatName string `json:"format_name"`
}

type probeResult struct {
	Streams []probeStream `json:"streams"`
	Format  probeFormat   `json:"format"`
}

func (a *App) ffprobeExe() string {
	if strings.TrimSpace(a.cfg.FFprobePath) != "" {
		return a.cfg.FFprobePath
	}
	return "ffprobe"
}

// hasProbe 判断探测是否可用（存在 ffprobe，或显式给出的 .cmd/.bat）。
func (a *App) hasProbe() bool {
	if !a.cfg.ProbeEnabled {
		return false
	}
	exe := a.ffprobeExe()
	low := strings.ToLower(exe)
	if strings.HasSuffix(low, ".cmd") || strings.HasSuffix(low, ".bat") {
		return true
	}
	if _, err := exec.LookPath(exe); err != nil {
		return false
	}
	return true
}

func (a *App) probeFile(path string) (*probeResult, error) {
	exe := a.ffprobeExe()
	args := []string{"-v", "quiet", "-print_format", "json", "-show_format", "-show_streams", path}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	low := strings.ToLower(exe)
	if strings.HasSuffix(low, ".cmd") || strings.HasSuffix(low, ".bat") {
		cmd = exec.CommandContext(ctx, "cmd", append([]string{"/c", exe}, args...)...)
	} else {
		cmd = exec.CommandContext(ctx, exe, args...)
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var pr probeResult
	if err := json.Unmarshal(out, &pr); err != nil {
		return nil, err
	}
	return &pr, nil
}

// probeAll 对尚未探测的视频条目并发执行 ffprobe，填充真实 MediaStreams/时长/大小。
func (a *App) probeAll() (int, int) {
	if !a.hasProbe() {
		log.Printf("未检测到 ffprobe（或已禁用），跳过媒体信息探测")
		return 0, 0
	}
	a.store.mu.RLock()
	var items []*Item
	for _, it := range a.store.Items {
		if (it.Type == "Movie" || it.Type == "Episode") &&
			it.SourcePath != "" && it.StreamURL == "" && !it.Probed {
			items = append(items, it)
		}
	}
	a.store.mu.RUnlock()
	total := len(items)
	if total == 0 {
		return 0, 0
	}
	conc := a.cfg.ProbeConcurrency
	if conc < 1 {
		conc = 1
	}
	if conc > 16 {
		conc = 16
	}
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	var mu sync.Mutex
	probed := 0
	for _, it := range items {
		wg.Add(1)
		sem <- struct{}{}
		go func(it *Item) {
			defer wg.Done()
			defer func() { <-sem }()
			pr, err := a.probeFile(it.SourcePath)
			if err != nil || pr == nil {
				return
			}
			a.applyProbe(it, pr)
			mu.Lock()
			probed++
			mu.Unlock()
		}(it)
	}
	wg.Wait()
	if probed > 0 {
		a.store.Save()
	}
	log.Printf("媒体信息探测完成：检查 %d，成功 %d", total, probed)
	return total, probed
}

func (a *App) applyProbe(it *Item, pr *probeResult) {
	var ticks int64
	if d, err := strconv.ParseFloat(pr.Format.Duration, 64); err == nil && d > 0 {
		ticks = int64(d * float64(10_000_000))
	}
	size, _ := strconv.ParseInt(pr.Format.Size, 10, 64)

	streams := make([]MediaStream, 0, len(pr.Streams)+len(it.Subtitles))
	for _, s := range pr.Streams {
		ms := MediaStream{Index: s.Index, Codec: s.CodecName}
		if br, err := strconv.ParseInt(s.BitRate, 10, 64); err == nil {
			ms.BitRate = br
		}
		switch s.CodecType {
		case "video":
			ms.Type, ms.Width, ms.Height, ms.Profile = "Video", s.Width, s.Height, s.Profile
		case "audio":
			ms.Type, ms.Channels, ms.Language = "Audio", s.Channels, s.Tags.Language
			if ms.Language == "" {
				ms.Language = "und"
			}
		case "subtitle":
			ms.Type, ms.Language, ms.DisplayTitle = "Subtitle", s.Tags.Language, s.Tags.Title
			ms.IsTextSubtitleStream = true
		default:
			continue
		}
		streams = append(streams, ms)
	}
	// 外挂字幕重排索引到 100+ 并追加
	for i := range it.Subtitles {
		it.Subtitles[i].Index = 100 + i
	}
	streams = append(streams, subtitleStreams(it)...)

	a.store.mu.Lock()
	it.Probed = true
	if ticks > 0 {
		it.RunTimeTicks = ticks
	}
	if len(it.MediaSources) > 0 {
		it.MediaSources[0].MediaStreams = streams
		it.MediaSources[0].RunTimeTicks = ticks
		if size > 0 {
			it.MediaSources[0].Size = size
		}
	}
	a.store.mu.Unlock()
}

// handleAdminProbe 手动触发媒体信息探测。
func (a *App) handleAdminProbe(w http.ResponseWriter, r *http.Request, u *User) {
	if !a.hasProbe() {
		writeErr(w, http.StatusBadRequest, "未检测到 ffprobe（或已禁用 PROBE_ENABLED）")
		return
	}
	go func() {
		n, hit := a.probeAll()
		a.store.addActivity("媒体信息探测", "检查 "+itoa(n)+" 项，成功 "+itoa(hit)+" 项", "MediaProbe", "Information")
		a.store.Save()
	}()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "探测已开始"})
}
