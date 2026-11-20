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

// tmdbClient 是 TMDB v3 API 的最小客户端。
type tmdbClient struct {
	apiKey    string
	lang      string
	base      string
	imageBase string
	http      *http.Client
}

func newTMDB(cfg Config) *tmdbClient {
	if strings.TrimSpace(cfg.TMDBAPIKey) == "" {
		return nil
	}
	return &tmdbClient{
		apiKey:    cfg.TMDBAPIKey,
		lang:      cfg.TMDBLanguage,
		base:      strings.TrimRight(cfg.TMDBBase, "/"),
		imageBase: strings.TrimRight(cfg.TMDBImageBase, "/"),
		http:      &http.Client{Timeout: 20 * time.Second},
	}
}

type tmdbResult struct {
	Id           int     `json:"id"`
	Title        string  `json:"title"`
	Name         string  `json:"name"`
	Overview     string  `json:"overview"`
	PosterPath   string  `json:"poster_path"`
	BackdropPath string  `json:"backdrop_path"`
	ReleaseDate  string  `json:"release_date"`
	FirstAirDate string  `json:"first_air_date"`
	VoteAverage  float64 `json:"vote_average"`
}

type tmdbSearchResp struct {
	Results []tmdbResult `json:"results"`
}

func (t *tmdbClient) get(path string, q url.Values, out any) error {
	if q == nil {
		q = url.Values{}
	}
	q.Set("api_key", t.apiKey)
	if t.lang != "" {
		q.Set("language", t.lang)
	}
	full := t.base + path + "?" + q.Encode()
	req, err := http.NewRequest(http.MethodGet, full, nil)
	if err != nil {
		return err
	}
	resp, err := t.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("tmdb HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (t *tmdbClient) search(kind, query string, year int) ([]tmdbResult, error) {
	q := url.Values{}
	q.Set("query", query)
	if year > 0 {
		if kind == "movie" {
			q.Set("year", itoa(year))
		} else {
			q.Set("first_air_date_year", itoa(year))
		}
	}
	var resp tmdbSearchResp
	if err := t.get("/search/"+kind, q, &resp); err != nil {
		return nil, err
	}
	return resp.Results, nil
}

// downloadPoster 下载 w500 尺寸海报到本地图片目录，返回文件路径。
func (t *tmdbClient) downloadPoster(relPath, dir string) (string, error) {
	if relPath == "" {
		return "", fmt.Errorf("空海报路径")
	}
	u := t.imageBase + "/w500" + relPath
	resp, err := t.http.Get(u)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("海报下载 HTTP %d", resp.StatusCode)
	}
	if err := ensureDir(dir); err != nil {
		return "", err
	}
	p := filepath.Join(dir, h16(relPath)+".jpg")
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

// tmdbEnrich 用 TMDB 补齐单个条目。返回是否发生变更。
func (a *App) tmdbEnrich(t *tmdbClient, it *Item) bool {
	kind := "movie"
	query := it.Name
	year := it.ProductionYear
	if it.Type == "Series" {
		kind = "tv"
		query = it.SeriesName
		if query == "" {
			query = it.Name
		}
		year = 0
	}
	if strings.TrimSpace(query) == "" {
		return false
	}
	results, err := t.search(kind, query, year)
	if err != nil || len(results) == 0 {
		return false
	}
	best := results[0]
	changed := false

	a.store.mu.Lock()
	if it.Overview == "" && best.Overview != "" {
		it.Overview = best.Overview
		changed = true
	}
	if it.ProductionYear == 0 {
		d := best.ReleaseDate
		if d == "" {
			d = best.FirstAirDate
		}
		if len(d) >= 4 {
			if y := atoiDefault(d[:4], 0); y > 0 {
				it.ProductionYear = y
				changed = true
			}
		}
	}
	a.store.mu.Unlock()

	if !it.HasPoster && best.PosterPath != "" {
		if p, err := t.downloadPoster(best.PosterPath, filepath.Join(a.cfg.DataDir, "images")); err == nil {
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
