package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (a *App) ffmpegExe() string {
	if strings.TrimSpace(a.cfg.FFmpegPath) != "" {
		return a.cfg.FFmpegPath
	}
	return "ffmpeg"
}

func (a *App) hasFFmpeg() bool {
	if !a.cfg.ThumbnailEnabled {
		return false
	}
	exe := a.ffmpegExe()
	low := strings.ToLower(exe)
	if strings.HasSuffix(low, ".cmd") || strings.HasSuffix(low, ".bat") {
		return true
	}
	_, err := exec.LookPath(exe)
	return err == nil
}

func (a *App) thumbPath(id string) string {
	return filepath.Join(a.cfg.DataDir, "images", "thumb-"+id+".jpg")
}

// ensureThumb 懒生成缩略图（ffmpeg 截取一帧并缓存）。
func (a *App) ensureThumb(it *Item) (string, bool) {
	p := a.thumbPath(it.Id)
	if fi, err := os.Stat(p); err == nil && fi.Size() > 0 {
		return p, true
	}
	if !a.hasFFmpeg() || it.SourcePath == "" || it.StreamURL != "" {
		return "", false
	}
	if err := ensureDir(filepath.Join(a.cfg.DataDir, "images")); err != nil {
		return "", false
	}
	ss := "00:00:05"
	if it.RunTimeTicks > 0 {
		sec := (it.RunTimeTicks / 10_000_000) / 10
		if sec < 1 {
			sec = 1
		}
		ss = strconv.FormatInt(sec, 10)
	}
	exe := a.ffmpegExe()
	args := []string{"-ss", ss, "-i", it.SourcePath, "-frames:v", "1", "-vf", "scale=320:-1", "-y", p}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	low := strings.ToLower(exe)
	if strings.HasSuffix(low, ".cmd") || strings.HasSuffix(low, ".bat") {
		cmd = exec.CommandContext(ctx, "cmd", append([]string{"/c", exe}, args...)...)
	} else {
		cmd = exec.CommandContext(ctx, exe, args...)
	}
	if err := cmd.Run(); err != nil {
		return "", false
	}
	if fi, err := os.Stat(p); err != nil || fi.Size() == 0 {
		return "", false
	}
	return p, true
}
