package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (a *App) handleItemImage(w http.ResponseWriter, r *http.Request, u *User, itemID, imageType string) {
	a.store.mu.RLock()
	it, ok := a.store.Items[itemID]
	a.store.mu.RUnlock()
	if !ok {
		writeErr(w, http.StatusNotFound, "条目不存在")
		return
	}

	lower := r.URL.Query().Get("_lower")
	_ = lower

	wantType := imageType
	if wantType == "" || strings.EqualFold(wantType, "Primary") {
		if it.HasPoster && it.PosterPath != "" {
			if serveLocalImage(w, r, it.PosterPath) {
				return
			}
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_ = writePlaceholderPNG(w, it.Id, 400, 600, false)
		return
	}
	if strings.EqualFold(wantType, "Backdrop") || strings.EqualFold(wantType, "Background") {
		if it.HasBackdrop && it.BackdropPath != "" {
			if serveLocalImage(w, r, it.BackdropPath) {
				return
			}
		}
	}
	if strings.EqualFold(wantType, "Thumb") {
		if it.ThumbPath != "" {
			if serveLocalImage(w, r, it.ThumbPath) {
				return
			}
		}
		if p, ok := a.ensureThumb(it); ok {
			if serveLocalImage(w, r, p) {
				return
			}
		}
	}
	if strings.EqualFold(wantType, "Logo") {
		if it.LogoPath != "" {
			if serveLocalImage(w, r, it.LogoPath) {
				return
			}
		}
	}

	// 其它类型 / 回退占位图
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	wide := strings.EqualFold(wantType, "Backdrop") || strings.EqualFold(wantType, "Thumb")
	wid, hei := 400, 600
	if wide {
		wid, hei = 1280, 720
	}
	_ = writePlaceholderPNG(w, it.Id+"-"+wantType, wid, hei, wide)
}

func serveLocalImage(w http.ResponseWriter, r *http.Request, path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	ctype := mime.TypeByExtension(filepath.Ext(path))
	if ctype == "" {
		ctype = "image/jpeg"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeContent(w, r, filepath.Base(path), fi.ModTime(), f)
	return true
}

// writePlaceholderPNG 依据种子生成确定性的彩色占位海报/背景图。
func writePlaceholderPNG(w http.ResponseWriter, seed string, width, height int, wide bool) error {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	h := []byte(h16(seed))
	base := color.RGBA{R: 40 + h[0]%120, G: 40 + h[1]%120, B: 40 + h[2]%140, A: 255}
	accent := color.RGBA{R: 20 + h[3]%80, G: 20 + h[4]%80, B: 20 + h[5]%80, A: 255}
	for y := 0; y < height; y++ {
		t := float64(y) / float64(height)
		c := color.RGBA{
			R: uint8(float64(base.R)*(1-t) + float64(accent.R)*t),
			G: uint8(float64(base.G)*(1-t) + float64(accent.G)*t),
			B: uint8(float64(base.B)*(1-t) + float64(accent.B)*t),
			A: 255,
		}
		for x := 0; x < width; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	// 中央加一条半透明色带，视觉上更像海报
	bandTop := height/3 + int(h[6])%50
	bandBot := bandTop + height/10
	for y := bandTop; y < bandBot && y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 235, G: 235, B: 235, A: 255})
		}
	}
	_ = wide
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return err
	}
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	_, err := w.Write(buf.Bytes())
	return err
}
