package main

import (
	"encoding/xml"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var videoExts = map[string]bool{
	".mp4": true, ".mkv": true, ".avi": true, ".mov": true, ".wmv": true,
	".flv": true, ".ts": true, ".m4v": true, ".webm": true, ".mpg": true,
	".mpeg": true, ".rmvb": true, ".rm": true, ".m2ts": true,
}

var skipDirs = map[string]bool{
	"@eaDir": true, "#recycle": true, "$RECYCLE.BIN": true, "System Volume Information": true,
	".git": true, ".trash": true, "lost+found": true,
}

var (
	reEpisode  = regexp.MustCompile(`(?i)[Ss](\d{1,2})[\s._-]?[Ee](\d{1,3})`)
	reEpisode2 = regexp.MustCompile(`(?i)\b(\d{1,2})x(\d{1,3})\b`)
	reYear     = regexp.MustCompile(`\b(19\d{2}|20\d{2})\b`)
	reBracket  = regexp.MustCompile(`[\[\(\{][^\]\)\}]*[\]\)\}]`)
)

type nfoData struct {
	Title         string   `xml:"title"`
	ShowTitle     string   `xml:"showtitle"`
	Plot          string   `xml:"plot"`
	Year          int      `xml:"year"`
	Runtime       int      `xml:"runtime"`
	Season        int      `xml:"season"`
	Episode       int      `xml:"episode"`
	Rating        float64  `xml:"rating"`
	MPAA          string   `xml:"mpaa"`
	Tagline       string   `xml:"tagline"`
	OriginalTitle string   `xml:"originaltitle"`
	Premiered     string   `xml:"premiered"`
	TMDB          string   `xml:"tmdbid"`
	IMDB          string   `xml:"imdbid"`
	Genres        []string `xml:"genre"`
	Studios       []string `xml:"studio"`
	Directors     []string `xml:"director"`
	Actors        []struct {
		Name string `xml:"name"`
		Role string `xml:"role"`
	} `xml:"actor"`
	UniqueIDs []struct {
		Type  string `xml:"type,attr"`
		Value string `xml:",chardata"`
	} `xml:"uniqueid"`
}

// applyNFO 把 NFO 富元数据写入条目。
func applyNFO(it *Item, n nfoData) {
	if n.Plot != "" {
		it.Overview = n.Plot
	}
	if n.Runtime > 0 && it.RunTimeTicks == 0 {
		it.RunTimeTicks = int64(n.Runtime) * 60 * 10_000_000
	}
	if len(n.Genres) > 0 {
		it.Genres = append([]string{}, n.Genres...)
	}
	if len(n.Studios) > 0 {
		for _, s := range n.Studios {
			name := strings.TrimSpace(s)
			if name != "" {
				id := int64(fnv64("studio:"+name) & 0x7fffffffffffffff)
				if id <= 0 {
					id = 1
				}
				it.Studios = append(it.Studios, Studio{Name: name, Id: id})
			}
		}
	}
	if len(n.Actors) > 0 || len(n.Directors) > 0 {
		for _, a := range n.Actors {
			if strings.TrimSpace(a.Name) != "" {
				it.People = append(it.People, Person{Id: h16("person:" + a.Name), Name: a.Name, Role: a.Role, Type: "Actor"})
			}
		}
		for _, d := range n.Directors {
			if strings.TrimSpace(d) != "" {
				it.People = append(it.People, Person{Id: h16("person:" + d), Name: d, Type: "Director"})
			}
		}
	}
	if n.Rating > 0 {
		it.CommunityRating = n.Rating
	}
	if n.MPAA != "" {
		it.OfficialRating = n.MPAA
	}
	if n.Tagline != "" {
		it.Taglines = []string{n.Tagline}
	}
	if n.OriginalTitle != "" {
		it.OriginalTitle = n.OriginalTitle
	}
	if n.Premiered != "" && it.PremiereDate == "" {
		it.PremiereDate = n.Premiered + "T00:00:00.0000000Z"
	}
	ids := map[string]string{}
	if n.TMDB != "" {
		ids["Tmdb"] = n.TMDB
	}
	if n.IMDB != "" {
		ids["Imdb"] = n.IMDB
	}
	for _, u := range n.UniqueIDs {
		if strings.EqualFold(u.Type, "tmdb") {
			ids["Tmdb"] = strings.TrimSpace(u.Value)
		}
		if strings.EqualFold(u.Type, "imdb") {
			ids["Imdb"] = strings.TrimSpace(u.Value)
		}
	}
	if len(ids) > 0 {
		it.ProviderIds = ids
	}
}

func fnv64(s string) uint64 {
	var h uint64 = 1469598103934665603
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}

// ScanResult 汇总一次扫描结果。
type ScanResult struct {
	Movies   int
	Episodes int
	Series   int
	Libs     int
}

// scanAll 扫描媒体库。onlyLib 为空表示全量；否则只重建该库（保留其它库条目）。
func (s *Store) scanAll(onlyLib string) ScanResult {
	var res ScanResult
	s.mu.Lock()
	for _, root := range s.cfg.MediaRoots {
		libID := "lib-" + h16(root)
		if _, ok := s.Libraries[libID]; !ok {
			name := filepath.Base(root)
			if name == "" || name == "." || name == string(filepath.Separator) {
				name = "媒体库"
			}
			ct := "movies"
			low := strings.ToLower(name)
			if strings.Contains(low, "tv") || strings.Contains(low, "show") ||
				strings.Contains(low, "series") || strings.Contains(name, "剧") {
				ct = "tvshows"
			}
			s.Libraries[libID] = &Library{Id: libID, Name: name, Paths: []string{root}, CollectionType: ct}
		}
	}
	s.mu.Unlock()

	// 记录扫描前的旧条目（用于媒体根目录离线时保留缓存）
	s.mu.RLock()
	oldItems := make([]*Item, 0, len(s.Items))
	for _, it := range s.Items {
		oldItems = append(oldItems, it)
	}
	s.mu.RUnlock()

	items := map[string]*Item{}
	if onlyLib != "" {
		// 增量：保留其它库的条目
		for _, it := range oldItems {
			if it.LibraryId != onlyLib {
				items[it.Id] = it
			}
		}
	}

	add := func(it *Item) {
		if _, exists := items[it.Id]; !exists {
			items[it.Id] = it
		}
	}

	for _, root := range s.cfg.MediaRoots {
		libID := "lib-" + h16(root)
		s.mu.RLock()
		lib := s.Libraries[libID]
		s.mu.RUnlock()
		if lib == nil {
			continue
		}
		if onlyLib != "" && libID != onlyLib {
			continue
		}
		res.Libs++
		ct := lib.CollectionType

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
			s.scanWait()
			ext := strings.ToLower(filepath.Ext(path))
			isStrm := ext == ".strm"
			if !videoExts[ext] && !isStrm {
				return nil
			}
			dir := filepath.Dir(path)
			base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

			// .strm：文件内容是一行远端直链
			var streamURL string
			if isStrm {
				raw, _ := os.ReadFile(path)
				streamURL = strings.TrimSpace(string(raw))
				if i := strings.IndexAny(streamURL, "\r\n"); i >= 0 {
					streamURL = strings.TrimSpace(streamURL[:i])
				}
				if streamURL == "" {
					return nil
				}
			}
			container := strings.TrimPrefix(ext, ".")
			if isStrm {
				container = streamContainer(streamURL)
			}

			season, episode, seriesName, ok := parseEpisode(base)
			if ct == "tvshows" || ok {
				if !ok {
					// 剧集库里没识别出季集的，按电影处理也可，但保持分类一致：跳过
					return nil
				}
				seriesKey := libID + "|" + strings.ToLower(seriesName)
				seriesID := "series-" + h16(seriesKey)
				series, exists := items[seriesID]
				if !exists {
					series = &Item{
						Id: seriesID, Name: seriesName, Type: "Series", MediaType: "Video",
						IsFolder: true, LibraryId: libID, ParentId: libID, ServerId: s.ServerId,
					}
					add(series)
					res.Series++
				}
				seasonID := "season-" + h16(seriesKey+"#"+strconv.Itoa(season))
				if _, exists := items[seasonID]; !exists {
					add(&Item{
						Id: seasonID, Name: "第 " + strconv.Itoa(season) + " 季", Type: "Season",
						MediaType: "Video", IsFolder: true, LibraryId: libID, ParentId: seriesID,
						SeriesId: seriesID, SeriesName: seriesName, IndexNumber: season, ServerId: s.ServerId,
					})
				}
				epID := "ep-" + h16(path)
				epName := seriesName + " S" + pad2(season) + "E" + pad2(episode)
				nfo := readNFO(dir, base)
				if nfo.Title != "" {
					epName = nfo.Title
				}
				poster, hasPoster := findPoster(dir, base)
				if !hasPoster && series.PosterPath == "" {
					// 无法找到海报则留空
				}
				if series.PosterPath == "" && hasPoster {
					series.PosterPath = poster
					series.HasPoster = true
					series.ImageTags = map[string]string{"Primary": h16(poster)}
				}
				ep := &Item{
					Id: epID, Name: epName, Type: "Episode", MediaType: "Video",
					LibraryId: libID, ParentId: seasonID, SeriesId: seriesID, SeasonId: seasonID,
					SeriesName: seriesName, SeasonName: "第 " + strconv.Itoa(season) + " 季",
					IndexNumber: episode, ParentIndexNumber: season,
					Container: container, SourcePath: path, Path: path,
					ServerId: s.ServerId, PosterPath: poster, HasPoster: hasPoster, StreamURL: streamURL,
				}
				if nfo.Plot != "" {
					ep.Overview = nfo.Plot
				}
				if nfo.Runtime > 0 {
					ep.RunTimeTicks = int64(nfo.Runtime) * 60 * 10_000_000
				}
				applyNFO(ep, nfo)
				if hasPoster {
					ep.ImageTags = map[string]string{"Primary": h16(poster)}
				}
				ep.Subtitles = findSubtitles(dir, base)
				if isStrm {
					ep.MediaSources = []MediaSource{strmSource(ep)}
				} else {
					ep.MediaSources = []MediaSource{makeSource(ep)}
				}
				if nm := s.displayNameFor(ep.Id); nm != "" {
					ep.Name = nm
				}
				add(ep)
				res.Episodes++
				return nil
			}

			// 电影
			title, year := parseMovieName(base)
			nfo := readNFO(dir, base)
			if nfo.Title != "" {
				title = nfo.Title
			}
			if nfo.Year > 0 {
				year = nfo.Year
			}
			if title == "" {
				title = base
			}
			poster, hasPoster := findPoster(dir, base)
			id := "movie-" + h16(path)
			it := &Item{
				Id: id, Name: title, Type: "Movie", MediaType: "Video",
				LibraryId: libID, ParentId: libID, ProductionYear: year,
				Container: container, SourcePath: path, Path: path,
				ServerId: s.ServerId, PosterPath: poster, HasPoster: hasPoster,
				IsFolder: false, StreamURL: streamURL,
			}
			if nfo.Plot != "" {
				it.Overview = nfo.Plot
			}
			if nfo.Runtime > 0 {
				it.RunTimeTicks = int64(nfo.Runtime) * 60 * 10_000_000
			}
			applyNFO(it, nfo)
			if hasPoster {
				it.ImageTags = map[string]string{"Primary": h16(poster)}
			}
			it.Subtitles = findSubtitles(dir, base)
			if isStrm {
				it.MediaSources = []MediaSource{strmSource(it)}
			} else {
				it.MediaSources = []MediaSource{makeSource(it)}
			}
			if nm := s.displayNameFor(it.Id); nm != "" {
				it.Name = nm
			}
			add(it)
			res.Movies++
			return nil
		})
	}

	// 媒体根目录离线（不存在）时，保留该库的旧条目，避免 NAS 掉线清空媒体库。
	s.mu.RLock()
	for _, it := range oldItems {
		if _, ok := items[it.Id]; ok {
			continue
		}
		lib := s.Libraries[it.LibraryId]
		if lib == nil {
			continue
		}
		offline := false
		for _, p := range lib.Paths {
			if _, err := os.Stat(p); err != nil {
				offline = true
				break
			}
		}
		if offline {
			items[it.Id] = it
		}
	}
	s.mu.RUnlock()

	s.mu.Lock()
	s.Items = items
	s.mu.Unlock()
	s.rebuildOrder()
	log.Printf("扫描完成：电影 %d，剧集 %d，剧 %d，库 %d", res.Movies, res.Episodes, res.Series, res.Libs)
	return res
}

// Scan 全量扫描所有媒体库。
func (s *Store) Scan() ScanResult { return s.scanAll("") }

// ScanLibrary 仅重扫指定库（增量），保留其它库条目。
func (s *Store) ScanLibrary(libID string) ScanResult { return s.scanAll(libID) }

func makeSource(it *Item) MediaSource {
	srcID := "src-" + h16(it.Id)
	streams := []MediaStream{
		{Type: "Video", Index: 0, Codec: codecOf(it.Container)},
		{Type: "Audio", Index: 1, Codec: "aac", Language: "und"},
	}
	streams = append(streams, subtitleStreams(it)...)
	return MediaSource{
		Id: srcID, Name: it.Name, Path: it.Path, Container: it.Container,
		SupportsDirectPlay: true, SupportsDirectStream: true, SupportsTranscoding: false,
		DirectStreamUrl: "/Videos/" + it.Id + "/stream?Static=true&MediaSourceId=" + srcID,
		MediaStreams:    streams,
	}
}

// subtitleStreams 把外挂字幕轨转成 Emby MediaStream（Type=Subtitle）。
func subtitleStreams(it *Item) []MediaStream {
	out := make([]MediaStream, 0, len(it.Subtitles))
	for _, st := range it.Subtitles {
		out = append(out, MediaStream{
			Type: "Subtitle", Index: st.Index, Codec: st.Format, Language: st.Language,
			IsExternal: true, DisplayTitle: st.Title, IsDefault: st.IsDefault,
			IsForced: st.IsForced, IsTextSubtitleStream: true,
		})
	}
	return out
}

// strmSource 用于 .strm 条目：直接指向远端直链。
func strmSource(it *Item) MediaSource {
	srcID := "src-" + h16(it.Id)
	streams := []MediaStream{
		{Type: "Video", Index: 0, Codec: codecOf(it.Container)},
		{Type: "Audio", Index: 1, Codec: "aac", Language: "und"},
	}
	streams = append(streams, subtitleStreams(it)...)
	return MediaSource{
		Id: srcID, Name: it.Name, Path: it.StreamURL, Container: it.Container,
		SupportsDirectPlay: true, SupportsDirectStream: true, SupportsTranscoding: false,
		DirectStreamUrl: it.StreamURL,
		MediaStreams:    streams,
	}
}

// streamContainer 从直链推断容器类型。
func streamContainer(u string) string {
	p := u
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	ext := strings.ToLower(filepath.Ext(p))
	if ext == "" {
		return "strm"
	}
	return strings.TrimPrefix(ext, ".")
}

func codecOf(container string) string {
	switch container {
	case "mkv":
		return "h264"
	case "mp4", "m4v":
		return "h264"
	case "webm":
		return "vp9"
	case "ts", "m2ts":
		return "h264"
	default:
		return ""
	}
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// parseEpisode 从文件名中解析 SxxExx / NxM。
func parseEpisode(base string) (season, episode int, series string, ok bool) {
	if m := reEpisode.FindStringSubmatch(base); m != nil {
		season, _ = strconv.Atoi(m[1])
		episode, _ = strconv.Atoi(m[2])
		series = cleanTitle(base[:strings.Index(base, m[0])])
		if series == "" {
			series = cleanTitle(m[0])
		}
		return season, episode, series, true
	}
	if m := reEpisode2.FindStringSubmatch(base); m != nil {
		season, _ = strconv.Atoi(m[1])
		episode, _ = strconv.Atoi(m[2])
		series = cleanTitle(base[:strings.Index(base, m[0])])
		if series == "" {
			series = cleanTitle(m[0])
		}
		return season, episode, series, true
	}
	return 0, 0, "", false
}

// parseMovieName 从文件名解析片名与年份。
func parseMovieName(base string) (title string, year int) {
	cleaned := reBracket.ReplaceAllString(base, " ")
	if y := reYear.FindString(cleaned); y != "" {
		year, _ = strconv.Atoi(y)
		// 取年份之前的部分作为标题，避免把分辨率等带进标题
		idx := strings.Index(cleaned, y)
		if idx > 0 {
			cleaned = cleaned[:idx]
		}
	}
	title = cleanTitle(cleaned)
	if title == "" {
		title = cleanTitle(base)
	}
	return title, year
}

func cleanTitle(s string) string {
	s = strings.ReplaceAll(s, ".", " ")
	s = strings.ReplaceAll(s, "_", " ")
	s = strings.ReplaceAll(s, "-", " ")
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(s)
}

// findPoster 在目录中寻找本地海报。
func findPoster(dir, base string) (string, bool) {
	names := []string{
		base + "-poster", base + ".poster", "poster", "folder", "cover",
	}
	exts := []string{".jpg", ".jpeg", ".png", ".webp"}
	for _, n := range names {
		for _, e := range exts {
			p := filepath.Join(dir, n+e)
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				return p, true
			}
		}
	}
	return "", false
}

func readNFO(dir, base string) nfoData {
	for _, name := range []string{base + ".nfo", "movie.nfo", "tvshow.nfo", "episode.nfo"} {
		p := filepath.Join(dir, name)
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var d nfoData
		if xml.Unmarshal(raw, &d) == nil {
			return d
		}
	}
	return nfoData{}
}

// ---------- 外挂字幕识别 ----------

var subtitleExts = map[string]bool{".srt": true, ".ass": true, ".ssa": true, ".vtt": true, ".sub": true}

type subLang struct{ code, name string }

func detectSubLang(stem string) subLang {
	s := strings.ToLower(stem)
	has := func(ws ...string) bool {
		for _, w := range ws {
			if strings.Contains(s, w) {
				return true
			}
		}
		return false
	}
	switch {
	case has("zh-hans", "chs", ".sc", "简体", "zh_cn", "zh-cn", "chi-hans"):
		return subLang{"chi", "简体中文"}
	case has("zh-hant", "cht", ".tc", "繁体", "zh_tw", "zh-tw", "chi-hant"):
		return subLang{"chi", "繁体中文"}
	case has("zh", "chi", "中文", "chinese"):
		return subLang{"chi", "中文"}
	case has("jpn", ".jp", ".ja", "japanese", "日语", "日文"):
		return subLang{"jpn", "日本語"}
	case has("kor", ".ko", "korean", "韩语", "韩文"):
		return subLang{"kor", "한국어"}
	case has("eng", ".en", "english", "英文", "英语"):
		return subLang{"eng", "English"}
	default:
		return subLang{"und", "字幕"}
	}
}

// findSubtitles 找出与视频同名（base / base.lang / base_xxx）的外挂字幕。
func findSubtitles(dir, base string) []SubtitleTrack {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	lb := strings.ToLower(base)
	var out []SubtitleTrack
	idx := 2 // 0=Video 1=Audio，字幕从 2 开始
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if !subtitleExts[ext] {
			continue
		}
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		lstem := strings.ToLower(stem)
		match := lstem == lb ||
			strings.HasPrefix(lstem, lb+".") ||
			strings.HasPrefix(lstem, lb+"_") ||
			strings.HasPrefix(lstem, lb+" ") ||
			strings.HasPrefix(lstem, lb+"-")
		if !match {
			continue
		}
		lang := detectSubLang(lstem)
		out = append(out, SubtitleTrack{
			Index: idx, Language: lang.code,
			Title: lang.name + " (" + strings.ToUpper(strings.TrimPrefix(ext, ".")) + ")",
			Path:  filepath.Join(dir, name), Format: strings.TrimPrefix(ext, "."),
			IsForced: strings.Contains(lstem, "forced"),
		})
		idx++
	}
	if len(out) > 0 {
		out[0].IsDefault = true
	}
	return out
}
