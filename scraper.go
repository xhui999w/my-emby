package main

import (
	"log"
	"time"
)

type scraperProvider struct {
	name   string
	enrich func(a *App, it *Item) bool
}

// scraperProviders 依据配置返回可用刮削源（TMDB 优先，豆瓣兜底）。
func (a *App) scraperProviders() []scraperProvider {
	var ps []scraperProvider
	if t := newTMDB(a.cfg); t != nil {
		ps = append(ps, scraperProvider{"tmdb", func(app *App, it *Item) bool { return app.tmdbEnrich(t, it) }})
	}
	if d := newDouban(a.cfg); d != nil {
		ps = append(ps, scraperProvider{"douban", func(app *App, it *Item) bool { return app.doubanEnrich(d, it) }})
	}
	return ps
}

func (a *App) hasScrapers() bool { return len(a.scraperProviders()) > 0 }

func (a *App) scraperSatisfied(it *Item) bool {
	return it.Overview != "" && it.HasPoster && it.ProductionYear != 0
}

// scrapeAll 多源刮削：对电影/剧集依次尝试各 provider，直到元数据齐全。
func (a *App) scrapeAll() (int, int) {
	providers := a.scraperProviders()
	if len(providers) == 0 {
		return 0, 0
	}
	a.store.mu.RLock()
	items := make([]*Item, 0)
	for _, it := range a.store.Items {
		if it.Type == "Movie" || it.Type == "Series" {
			items = append(items, it)
		}
	}
	a.store.mu.RUnlock()

	enriched := 0
	for _, it := range items {
		if a.scraperSatisfied(it) {
			continue
		}
		changed := false
		for _, p := range providers {
			if a.scraperSatisfied(it) {
				break
			}
			if p.enrich(a, it) {
				changed = true
			}
			time.Sleep(120 * time.Millisecond)
		}
		if changed {
			enriched++
		}
	}
	if enriched > 0 {
		a.store.Save()
		log.Printf("多源刮削完成：检查 %d，更新 %d", len(items), enriched)
	}
	return len(items), enriched
}

// scraperStatus 供后台展示当前可用刮削源。
func (a *App) scraperStatus() map[string]bool {
	return map[string]bool{"tmdb": a.cfg.TMDBAPIKey != "", "douban": a.cfg.DoubanEnabled}
}
