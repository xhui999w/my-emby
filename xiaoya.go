package main

import (
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ---------- 极简 WebDAV PROPFIND 客户端（stdlib） ----------

type davEntry struct {
	Path         string
	IsCollection bool
}

type davMultistatus struct {
	Responses []davResponse `xml:"response"`
}
type davResponse struct {
	Href      string        `xml:"href"`
	Propstats []davPropstat `xml:"propstat"`
}
type davPropstat struct {
	Prop davProp `xml:"prop"`
}
type davProp struct {
	ResourceType  davResourceType `xml:"resourcetype"`
	ContentLength string          `xml:"getcontentlength"`
}
type davResourceType struct {
	Collection *davCollection `xml:"collection"`
}
type davCollection struct{}

const davPropfindBody = `<?xml version="1.0" encoding="utf-8"?>
<d:propfind xmlns:d="DAV:"><d:prop><d:resourcetype/><d:getcontentlength/></d:prop></d:propfind>`

func (a *App) davListRaw(baseURL *url.URL, dirPath string) ([]davEntry, error) {
	u := &url.URL{Scheme: baseURL.Scheme, Host: baseURL.Host, Path: dirPath}
	req, err := http.NewRequest("PROPFIND", u.String(), strings.NewReader(davPropfindBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Depth", "1")
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	if a.cfg.XiaoyaUser != "" {
		req.SetBasicAuth(a.cfg.XiaoyaUser, a.cfg.XiaoyaPass)
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 207 && resp.StatusCode != 200 {
		return nil, fmt.Errorf("webdav PROPFIND 返回 %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var ms davMultistatus
	if err := xml.Unmarshal(body, &ms); err != nil {
		return nil, err
	}
	out := make([]davEntry, 0, len(ms.Responses))
	for _, r := range ms.Responses {
		decoded := r.Href
		if pu, err := url.Parse(r.Href); err == nil {
			decoded = pu.Path
		}
		if up, err := url.PathUnescape(decoded); err == nil {
			decoded = up
		}
		isCol := false
		for _, ps := range r.Propstats {
			if ps.Prop.ResourceType.Collection != nil {
				isCol = true
			}
		}
		out = append(out, davEntry{Path: decoded, IsCollection: isCol})
	}
	return out, nil
}

// ---------- Xiaoya / OpenList STRM 同步 ----------

// syncXiaoya 递归遍历远端 WebDAV 目录，为视频生成 .strm 文件，随后触发扫描。
func (a *App) syncXiaoya() (int, error) {
	src := strings.TrimRight(a.cfg.XiaoyaSource, "/")
	if src == "" {
		return 0, fmt.Errorf("未配置 XIAOYA_SOURCE")
	}
	baseURL, err := url.Parse(src)
	if err != nil {
		return 0, err
	}
	basePath := strings.TrimRight(baseURL.Path, "/")
	count := 0
	if err := a.davWalk(baseURL, basePath, basePath, 0, &count); err != nil {
		return count, err
	}
	a.store.Scan()
	a.store.Save()
	log.Printf("Xiaoya STRM 同步完成：生成 %d 个 .strm 到 %s", count, a.cfg.XiaoyaTarget)
	return count, nil
}

func (a *App) davWalk(baseURL *url.URL, basePath, dirPath string, depth int, count *int) error {
	if depth > 10 {
		return nil
	}
	entries, err := a.davListRaw(baseURL, dirPath)
	if err != nil {
		return err
	}
	for _, e := range entries {
		rel := strings.TrimPrefix(e.Path, basePath)
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			continue // 自身
		}
		if e.IsCollection {
			if err := a.davWalk(baseURL, basePath, e.Path, depth+1, count); err != nil {
				return err
			}
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Path))
		if !videoExts[ext] && ext != ".strm" {
			continue
		}
		pub := a.strmPublicURL(rel)
		local := filepath.Join(a.cfg.XiaoyaTarget, filepath.FromSlash(rel))
		local = strings.TrimSuffix(local, filepath.Ext(local)) + ".strm"
		if err := ensureDir(filepath.Dir(local)); err != nil {
			continue
		}
		if err := os.WriteFile(local, []byte(pub+"\n"), 0o644); err != nil {
			continue
		}
		*count++
	}
	return nil
}

// strmPublicURL 依据相对路径构造写进 .strm 的直链。
func (a *App) strmPublicURL(rel string) string {
	base := strings.TrimRight(a.cfg.XiaoyaPublicBase, "/")
	if base == "" {
		base = strings.Replace(strings.TrimRight(a.cfg.XiaoyaSource, "/"), "/dav", "/d", 1)
	}
	segs := strings.Split(rel, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return base + "/" + strings.Join(segs, "/")
}

// startXiaoyaScheduler 按分钟间隔定时同步。
func (a *App) startXiaoyaScheduler() {
	if a.cfg.XiaoyaSource == "" || a.cfg.XiaoyaInterval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(time.Duration(a.cfg.XiaoyaInterval) * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			if _, err := a.syncXiaoya(); err != nil {
				log.Printf("Xiaoya 定时同步失败: %v", err)
			}
		}
	}()
	log.Printf("Xiaoya 定时同步已启用：每 %d 分钟", a.cfg.XiaoyaInterval)
}
