package main

import (
	"encoding/json"
	"net/http"
)

// ---------- 扫描暂停 / 继续 ----------

// scanWait 若扫描处于暂停状态，则阻塞直到恢复。
func (s *Store) scanWait() {
	s.scanPauseMu.Lock()
	ch := s.scanResume
	s.scanPauseMu.Unlock()
	if ch != nil {
		<-ch
	}
}

func (s *Store) setScanPaused(p bool) bool {
	s.scanPauseMu.Lock()
	defer s.scanPauseMu.Unlock()
	if p && s.scanResume == nil {
		s.scanResume = make(chan struct{})
	}
	if !p && s.scanResume != nil {
		close(s.scanResume)
		s.scanResume = nil
	}
	return s.scanResume != nil
}

func (s *Store) scanPaused() bool {
	s.scanPauseMu.Lock()
	defer s.scanPauseMu.Unlock()
	return s.scanResume != nil
}

// ---------- 扫描队列 worker ----------

// startScanWorker 启动单消费者扫描队列。libID 为空表示全量扫描。
func (a *App) startScanWorker() {
	a.scanQueue = make(chan string, 64)
	a.scanPending = map[string]bool{}
	go func() {
		for libID := range a.scanQueue {
			a.store.scanWait()
			var res ScanResult
			if libID == "" {
				res = a.store.Scan()
			} else {
				res = a.store.ScanLibrary(libID)
			}
			a.store.Save()
			a.store.addActivity("媒体库扫描",
				"扫描完成：电影 "+itoa(res.Movies)+"，剧集 "+itoa(res.Episodes)+"，剧 "+itoa(res.Series),
				"LibraryScan", "Information")
			// 全量扫描后顺带做刮削与探测
			if libID == "" {
				if a.scrapeAutoEnabled() && a.hasScrapers() {
					a.startScrapeJob(false)
				}
				if a.hasProbe() {
					a.startProbeJob()
				}
				a.store.Save()
			}
			a.scanQMu.Lock()
			delete(a.scanPending, libID)
			a.scanQMu.Unlock()
		}
	}()
}

// enqueueScan 将一次扫描任务入队（去重）。libID 为空=全量。
func (a *App) enqueueScan(libID string) {
	a.scanQMu.Lock()
	if a.scanPending[libID] {
		a.scanQMu.Unlock()
		return
	}
	a.scanPending[libID] = true
	a.scanQMu.Unlock()
	select {
	case a.scanQueue <- libID:
	default:
		// 队列满，丢弃本次（清 pending，允许后续重试）
		a.scanQMu.Lock()
		delete(a.scanPending, libID)
		a.scanQMu.Unlock()
	}
}

// handleScanControl GET 返回暂停状态；PUT {Paused:true/false} 设置暂停。
func (a *App) handleScanControl(w http.ResponseWriter, r *http.Request, u *User) {
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 GET/PUT")
		return
	}
	if r.Method == http.MethodPut {
		var b struct {
			Paused *bool `json:"Paused"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b.Paused == nil {
			writeErr(w, http.StatusBadRequest, "缺少 Paused")
			return
		}
		a.store.setScanPaused(*b.Paused)
	}
	writeJSON(w, http.StatusOK, map[string]any{"Paused": a.store.scanPaused()})
}
