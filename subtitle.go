package main

import (
	"encoding/binary"
	"net/http"
	"os"
	"strconv"
	"strings"
	"unicode/utf16"
)

// handleSubtitle 提供外挂字幕下载/在线播放，支持 SRT↔VTT 与 UTF-16/BOM 规整化。
func (a *App) handleSubtitle(w http.ResponseWriter, r *http.Request, u *User, itemID string, idx int, wantExt string) {
	it := a.getItem(itemID)
	if it == nil {
		writeErr(w, http.StatusNotFound, "条目不存在")
		return
	}
	var track *SubtitleTrack
	for i := range it.Subtitles {
		if it.Subtitles[i].Index == idx {
			track = &it.Subtitles[i]
			break
		}
	}
	if track == nil {
		writeErr(w, http.StatusNotFound, "字幕不存在")
		return
	}
	raw, err := os.ReadFile(track.Path)
	if err != nil {
		writeErr(w, http.StatusNotFound, "字幕文件不可读")
		return
	}
	data := normalizeSubtitleEncoding(raw)
	format := strings.ToLower(strings.TrimPrefix(wantExt, "."))
	if format == "" {
		format = track.Format
	}
	switch format {
	case "vtt":
		if track.Format != "vtt" {
			data = srtToVTT(data)
		}
		w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	case "srt":
		w.Header().Set("Content-Type", "application/x-subrip; charset=utf-8")
	case "ass", "ssa":
		w.Header().Set("Content-Type", "text/x-ssa; charset=utf-8")
	default:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// normalizeSubtitleEncoding 去掉 BOM，并把 UTF-16(LE/BE) 转为 UTF-8。
func normalizeSubtitleEncoding(b []byte) []byte {
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE { // UTF-16 LE
		return convertUTF16(b[2:], binary.LittleEndian)
	}
	if len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF { // UTF-16 BE
		return convertUTF16(b[2:], binary.BigEndian)
	}
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF { // UTF-8 BOM
		return b[3:]
	}
	return b
}

func convertUTF16(b []byte, order binary.ByteOrder) []byte {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, order.Uint16(b[i:i+2]))
	}
	return []byte(string(utf16.Decode(u)))
}

// srtToVTT 把 SRT 文本转成 WebVTT（时间戳逗号→点，加 WEBVTT 头）。
func srtToVTT(b []byte) []byte {
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines)+2)
	out = append(out, "WEBVTT", "")
	for _, ln := range lines {
		if strings.Contains(ln, "-->") {
			ln = strings.ReplaceAll(ln, ",", ".")
		}
		out = append(out, ln)
	}
	return []byte(strings.Join(out, "\n"))
}

// parseSubtitlePath 从 URL 段解析出 itemId / index / 目标扩展名。
// 形如 /Videos/{id}/{srcId}/Subtitles/{index}/Stream.{ext}
func parseSubtitlePath(seg []string) (itemID string, idx int, ext string, ok bool) {
	if len(seg) < 2 {
		return "", 0, "", false
	}
	itemID = seg[1]
	for i := 0; i < len(seg); i++ {
		if strings.EqualFold(seg[i], "Subtitles") && i+1 < len(seg) {
			n, err := strconv.Atoi(seg[i+1])
			if err != nil {
				return "", 0, "", false
			}
			idx = n
			last := seg[len(seg)-1]
			if j := strings.LastIndex(last, "Stream."); j >= 0 {
				ext = last[j+len("Stream."):]
			}
			return itemID, idx, ext, true
		}
	}
	return "", 0, "", false
}
