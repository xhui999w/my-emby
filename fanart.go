package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// fanartClient 通过 Fanart.tv API 下载电影的多类型图片（背景图/Logo/缩略图/光盘图）。
// 剧集端点需要 TVDB ID，当前未接入。

type fanartClient struct {
	key  string
	base string
	http *http.Client
}

func (a *App) fanartKey() string {
	cfg := a.scrapeMediaConfig()
	if v, ok := cfg["FanartKey"].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func (a *App) fanartBase() string {
	cfg := a.scrapeMediaConfig()
	if v, ok := cfg["FanartBase"].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimRight(strings.TrimSpace(v), "/")
	}
	if v := strings.TrimSpace(a.store.setting("fanart_base")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "https://webservice.fanart.tv"
}

func (a *App) newFanart() *fanartClient {
	key := a.fanartKey()
	if key == "" {
		return nil
	}
	return &fanartClient{key: key, base: a.fanartBase(), http: a.clientFor("other")}
}

type fanartItem struct {
	URL string `json:"url"`
}

type fanartResp struct {
	MovieBackground []fanartItem `json:"moviebackground"`
	HDMovieLogo     []fanartItem `json:"hdmovielogo"`
	MovieLogo       []fanartItem `json:"movielogo"`
	MovieDisc       []fanartItem `json:"moviedisc"`
	MovieThumb      []fanartItem `json:"moviethumb"`
}

func (f *fanartClient) fetchMovie(tmdbID string) (*fanartResp, error) {
	u := f.base + "/v3/movies/" + tmdbID + "?api_key=" + f.key
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	resp, err := f.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fanart HTTP %d", resp.StatusCode)
	}
	var out fanartResp
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (f *fanartClient) download(url, dir, name string) (string, error) {
	if url == "" {
		return "", fmt.Errorf("空图片地址")
	}
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	resp, err := f.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("图片 HTTP %d", resp.StatusCode)
	}
	if err := ensureDir(dir); err != nil {
		return "", err
	}
	ext := ".jpg"
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "png") {
		ext = ".png"
	}
	p := filepath.Join(dir, name+ext)
	tmp := p + ".tmp"
	fh, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(fh, resp.Body); err != nil {
		fh.Close()
		os.Remove(tmp)
		return "", err
	}
	fh.Close()
	if err := os.Rename(tmp, p); err != nil {
		return "", err
	}
	return p, nil
}

// fanartEnrich 按「分类明细」勾选下载 Fanart.tv 图片（电影）。
func (a *App) fanartEnrich(f *fanartClient, it *Item) bool {
	if it.Type != "Movie" {
		return false
	}
	tmdbID := ""
	if it.ProviderIds != nil {
		tmdbID = strings.TrimSpace(it.ProviderIds["Tmdb"])
	}
	if tmdbID == "" {
		return false
	}

	// 分类明细开关（Movie 资源类型）
	mc := a.scrapeMediaConfig()
	cats, _ := mc["Categories"].(map[string]any)
	mcat, _ := cats["Movie"].(map[string]any)
	flag := func(k string) bool {
		if mcat == nil {
			return false
		}
		b, _ := mcat[k].(bool)
		return b
	}
	if !flag("Backdrop") && !flag("Logo") && !flag("Thumb") && !flag("Disc") {
		return false
	}

	policy := a.scrapeOverwrite()
	dir := filepath.Join(a.cfg.DataDir, "images")
	resp, err := f.fetchMovie(tmdbID)
	if err != nil || resp == nil {
		return false
	}
	changed := false
	dl := func(items []fanartItem, name string) string {
		if len(items) == 0 {
			return ""
		}
		p, err := f.download(items[0].URL, dir, name)
		if err != nil {
			return ""
		}
		return p
	}

	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	if flag("Backdrop") && (policy == "overwrite" || !it.HasBackdrop) {
		if p := dl(resp.MovieBackground, "fanart-"+it.Id+"-backdrop"); p != "" {
			it.BackdropPath = p
			it.HasBackdrop = true
			it.BackdropImageTags = []string{h16(p)}
			changed = true
		}
	}
	if flag("Logo") && (policy == "overwrite" || it.LogoPath == "") {
		src := resp.HDMovieLogo
		if len(src) == 0 {
			src = resp.MovieLogo
		}
		if p := dl(src, "fanart-"+it.Id+"-logo"); p != "" {
			it.LogoPath = p
			changed = true
		}
	}
	if flag("Thumb") && (policy == "overwrite" || it.ThumbPath == "") {
		if p := dl(resp.MovieThumb, "fanart-"+it.Id+"-thumb"); p != "" {
			it.ThumbPath = p
			changed = true
		}
	}
	return changed
}

var _ = time.Second
