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
)

// doubanClient 使用豆瓣 subject_suggest 接口做轻量刮削（扩展数据源）。
type doubanClient struct {
	base string
	http *http.Client
}

func (a *App) newDouban() *doubanClient {
	if !a.cfg.DoubanEnabled || strings.TrimSpace(a.cfg.DoubanBase) == "" {
		return nil
	}
	return &doubanClient{base: strings.TrimRight(a.cfg.DoubanBase, "/"), http: a.clientFor("other")}
}

type doubanSuggestion struct {
	Title    string `json:"title"`
	Year     string `json:"year"`
	Img      string `json:"img"`
	SubTitle string `json:"sub_title"`
	Id       string `json:"id"`
	Type     string `json:"type"`
}

func (d *doubanClient) suggest(q string) ([]doubanSuggestion, error) {
	u := d.base + "/j/subject_suggest?q=" + url.QueryEscape(q)
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Referer", d.base+"/")
	resp, err := d.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("douban HTTP %d", resp.StatusCode)
	}
	var out []doubanSuggestion
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func (d *doubanClient) downloadCover(imgURL, dir string) (string, error) {
	if imgURL == "" {
		return "", fmt.Errorf("空封面")
	}
	req, _ := http.NewRequest(http.MethodGet, imgURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := d.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("封面 HTTP %d", resp.StatusCode)
	}
	if err := ensureDir(dir); err != nil {
		return "", err
	}
	p := filepath.Join(dir, h16(imgURL)+".jpg")
	f, err := os.Create(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", err
	}
	return p, nil
}

// doubanEnrich 用豆瓣建议接口补齐年份/海报（不提供简介）。
func (a *App) doubanEnrich(d *doubanClient, it *Item) bool {
	if it.Type != "Movie" && it.Type != "Series" {
		return false
	}
	q := it.Name
	if it.Type == "Series" && it.SeriesName != "" {
		q = it.SeriesName
	}
	res, err := d.suggest(q)
	if err != nil || len(res) == 0 {
		return false
	}
	best := res[0]
	changed := false
	if it.ProductionYear == 0 {
		if y := atoiDefault(strings.TrimSpace(best.Year), 0); y > 0 {
			a.store.mu.Lock()
			it.ProductionYear = y
			a.store.mu.Unlock()
			changed = true
		}
	}
	if !it.HasPoster && best.Img != "" {
		if p, err := d.downloadCover(best.Img, filepath.Join(a.cfg.DataDir, "images")); err == nil {
			a.store.mu.Lock()
			it.PosterPath = p
			it.HasPoster = true
			it.ImageTags = map[string]string{"Primary": h16(p)}
			a.store.mu.Unlock()
			changed = true
		}
	}
	return changed
}
