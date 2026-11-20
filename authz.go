package main

import "net/http"

// deviceID 从请求头取设备标识；缺失时回退为用户维度模糊标识。
func deviceID(r *http.Request, userID string) string {
	for _, h := range []string{"X-Emby-Device-Id", "Emby-Device-Id", "X-Emby-DeviceId"} {
		if v := r.Header.Get(h); v != "" {
			return v
		}
	}
	if v := r.URL.Query().Get("DeviceId"); v != "" {
		return v
	}
	for _, s := range []string{
		r.Header.Get("X-Emby-Authorization"),
		r.Header.Get("Authorization"),
		r.URL.Query().Get("X-Emby-Authorization"),
	} {
		if s == "" {
			continue
		}
		if _, d := parseAuthString(s); d != "" {
			return d
		}
	}
	if ua := r.Header.Get("User-Agent"); ua != "" {
		return "ua-" + h16(ua)
	}
	return "unknown-" + userID
}

// authorizePlayback 校验用户播放权限与并发设备上限。不通过时写错误并返回 false。
func (a *App) authorizePlayback(w http.ResponseWriter, r *http.Request, u *User) bool {
	if u.PlaybackDisabled {
		writeErr(w, http.StatusForbidden, "该用户已被禁止播放")
		return false
	}
	dev := deviceID(r, u.Id)
	allowed, _ := a.store.touchDevice(u.Id, dev, 300, u.MaxDevices)
	if !allowed {
		writeErr(w, http.StatusForbidden, "已达到该用户的最大并发设备数限制")
		return false
	}
	return true
}

// authorizeDownload 校验下载权限。
func (a *App) authorizeDownload(w http.ResponseWriter, r *http.Request, u *User) bool {
	if u.DownloadDisabled {
		writeErr(w, http.StatusForbidden, "该用户已被禁止下载")
		return false
	}
	return true
}
