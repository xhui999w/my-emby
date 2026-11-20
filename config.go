package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config 汇总所有运行期环境变量。设计上与 go-emby 的 .env 兼容。
type Config struct {
	Port            string
	MediaRoots      []string
	DataDir         string
	AdminPassword   string
	PublicURL       string
	BootstrapAPIKey string
	ServerName      string

	// PostgreSQL（设置即启用；为空则回退到 JSON 文件存储）
	DatabaseURL string

	// TMDB 元数据刮削
	TMDBAPIKey    string
	TMDBLanguage  string
	TMDBImageBase string
	TMDBBase      string

	// 豆瓣刮削（扩展数据源）
	DoubanEnabled bool
	DoubanBase    string

	// 文件实时监听（轮询式增量扫描）
	WatchEnabled     bool
	WatchIntervalSec int

	// ffprobe 媒体信息探测
	ProbeEnabled     bool
	FFprobePath      string
	ProbeConcurrency int

	// 文件管理根目录
	FileRoot string

	// ffmpeg 缩略图
	FFmpegPath       string
	ThumbnailEnabled bool

	// Xiaoya / OpenList(WebDAV) STRM 同步
	XiaoyaSource     string // 远端 WebDAV 根，如 http://host:5244/dav/电影
	XiaoyaTarget     string // 本地落盘目录（生成 .strm），默认 <data>/strm
	XiaoyaUser       string
	XiaoyaPass       string
	XiaoyaInterval   int    // 分钟；0 代表不定时
	XiaoyaPublicBase string // 写入 .strm 的直链前缀，默认取 XiaoyaSource

	// 授权（免费模式留空）
	LicenseKey       string
	LicenseServerURL string
	LicenseEnforce   bool
}

func loadConfig() Config {
	c := Config{
		Port:            env("HTTP_PORT", "8097"),
		MediaRoots:      splitRoots(env("MEDIA_ROOTS", "./media")),
		DataDir:         env("APP_DATA_PATH", "./data"),
		AdminPassword:   env("ADMIN_PASSWORD", ""),
		PublicURL:       env("PUBLIC_URL", ""),
		BootstrapAPIKey: env("BOOTSTRAP_API_KEY", ""),
		ServerName:      env("SERVER_NAME", "Go-Emby"),

		DatabaseURL:   firstEnv("DATABASE_URL", "POSTGRES_DSN"),
		TMDBAPIKey:    env("TMDB_API_KEY", ""),
		TMDBLanguage:  env("TMDB_LANGUAGE", "zh-CN"),
		TMDBImageBase: env("TMDB_IMAGE_BASE", "https://image.tmdb.org/t/p"),
		TMDBBase:      env("TMDB_BASE", "https://api.themoviedb.org/3"),
		DoubanEnabled: env("DOUBAN_ENABLED", "false") == "true",
		DoubanBase:    env("DOUBAN_BASE", "https://movie.douban.com"),

		WatchEnabled:     env("WATCH_ENABLED", "true") == "true",
		WatchIntervalSec: atoiEnv("WATCH_INTERVAL_SEC", 8),
		ProbeEnabled:     env("PROBE_ENABLED", "true") == "true",
		FFprobePath:      env("FFPROBE_PATH", "ffprobe"),
		ProbeConcurrency: atoiEnv("PROBE_CONCURRENCY", 2),

		FFmpegPath:       env("FFMPEG_PATH", "ffmpeg"),
		ThumbnailEnabled: env("THUMBNAIL_ENABLED", "true") == "true",

		XiaoyaSource:     env("XIAOYA_SOURCE", ""),
		XiaoyaTarget:     env("XIAOYA_TARGET", ""),
		XiaoyaUser:       env("XIAOYA_USERNAME", "dav"),
		XiaoyaPass:       env("XIAOYA_PASSWORD", ""),
		XiaoyaInterval:   atoiEnv("XIAOYA_INTERVAL_MIN", 0),
		XiaoyaPublicBase: env("XIAOYA_PUBLIC_BASE", ""),

		LicenseKey:       env("LICENSE_KEY", ""),
		LicenseServerURL: env("LICENSE_SERVER_URL", "https://tl.macacaaca.top"),
		LicenseEnforce:   env("LICENSE_ENFORCE", "false") == "true",
	}
	if abs, err := filepath.Abs(c.DataDir); err == nil {
		c.DataDir = abs
	}
	if c.XiaoyaTarget == "" {
		c.XiaoyaTarget = filepath.Join(c.DataDir, "strm")
	} else if abs, err := filepath.Abs(c.XiaoyaTarget); err == nil {
		c.XiaoyaTarget = abs
	}
	for i, p := range c.MediaRoots {
		if abs, err := filepath.Abs(p); err == nil {
			c.MediaRoots[i] = abs
		}
	}
	// 文件管理根目录：默认取第一个媒体根
	fr := env("FILE_MANAGER_ROOT", "")
	if fr == "" && len(c.MediaRoots) > 0 {
		fr = c.MediaRoots[0]
	}
	if fr != "" {
		if abs, err := filepath.Abs(fr); err == nil {
			fr = abs
		}
	}
	c.FileRoot = fr
	return c
}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

func atoiEnv(k string, def int) int {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// splitRoots 用 ':' 或 ';' 分隔多个媒体根目录（容器内路径）。
// 在 Windows 上要注意不要把盘符 "C:\" 里的冒号当成分隔符。
func splitRoots(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var parts []string
	if strings.Contains(s, ";") {
		parts = strings.Split(s, ";")
	} else {
		var cur strings.Builder
		for i := 0; i < len(s); i++ {
			c := s[i]
			if c == ':' {
				isDrive := i == 1 && len(s) > 2 && (s[2] == '\\' || s[2] == '/') &&
					((s[0] >= 'a' && s[0] <= 'z') || (s[0] >= 'A' && s[0] <= 'Z'))
				if isDrive {
					cur.WriteByte(c)
					continue
				}
				parts = append(parts, cur.String())
				cur.Reset()
				continue
			}
			cur.WriteByte(c)
		}
		parts = append(parts, cur.String())
	}
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
