package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// fileStamp 用于监听变化：文件大小 + 修改时间。
type fileStamp struct {
	Size int64
	Mod  int64
}

// collectStamps 采集所有媒体根目录下相关文件（视频/strm/字幕/NFO/图片）的指纹。
func (a *App) collectStamps() map[string]fileStamp {
	out := map[string]fileStamp{}
	for _, root := range a.cfg.MediaRoots {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				if skipDirs[info.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			if !videoExts[ext] && ext != ".strm" && !subtitleExts[ext] &&
				ext != ".nfo" && ext != ".jpg" && ext != ".jpeg" && ext != ".png" && ext != ".webp" {
				return nil
			}
			out[path] = fileStamp{Size: info.Size(), Mod: info.ModTime().UnixNano()}
			return nil
		})
	}
	return out
}

// libForPath 判断某文件属于哪个媒体库（按库配置的多路径匹配）。
func (a *App) libForPath(path string) string {
	a.store.mu.RLock()
	defer a.store.mu.RUnlock()
	for _, l := range a.store.Libraries {
		for _, root := range l.Paths {
			if path == root ||
				strings.HasPrefix(path, root+string(os.PathSeparator)) ||
				strings.HasPrefix(path, root+"/") {
				return l.Id
			}
		}
	}
	return ""
}

// startWatcher 轮询式实时监听：检测到增/删/改后按库触发增量扫描。
func (a *App) startWatcher() {
	if !a.cfg.WatchEnabled {
		log.Printf("文件实时监听未启用")
		return
	}
	a.watchMu.Lock()
	a.watchSnap = a.collectStamps()
	a.watchMu.Unlock()
	interval := a.cfg.WatchIntervalSec
	if interval < 2 {
		interval = 5
	}
	go func() {
		ticker := time.NewTicker(time.Duration(interval) * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			// 扫描进行中跳过本轮轮询：避免与全量扫描抢 CPU/IO，也防止半成品快照触发误报
			if a.store.scanActive.Load() {
				continue
			}
			cur := a.collectStamps()
			a.watchMu.Lock()
			old := a.watchSnap
			changed := map[string]bool{}
			for p, st := range cur {
				if o, ok := old[p]; !ok || o != st {
					if lib := a.libForPath(p); lib != "" {
						changed[lib] = true
					}
				}
			}
			for p := range old {
				if _, ok := cur[p]; !ok {
					if lib := a.libForPath(p); lib != "" {
						changed[lib] = true
					}
				}
			}
			a.watchSnap = cur
			a.watchMu.Unlock()
			if !a.store.settingBool("watch_enabled", a.cfg.WatchEnabled) {
				continue
			}
			for lib := range changed {
				log.Printf("检测到文件变化，触发增量扫描：%s", lib)
				a.enqueueScan(lib)
			}
		}
	}()
	log.Printf("文件实时监听已启用：每 %d 秒轮询", interval)
}
