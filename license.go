package main

import "strings"

// LicenseInfo 描述当前授权状态。本项目默认免费模式；保留 LICENSE_KEY 入口，
// 便于后续接入真实授权服务（上游默认 https://tl.macacaaca.top）。
type LicenseInfo struct {
	Mode   string `json:"mode"` // free / licensed
	Valid  bool   `json:"valid"`
	Key    string `json:"key,omitempty"`
	Server string `json:"server"`
}

func (a *App) licenseStatus() LicenseInfo {
	key := strings.TrimSpace(a.cfg.LicenseKey)
	if key == "" {
		return LicenseInfo{Mode: "free", Valid: true, Server: a.cfg.LicenseServerURL}
	}
	// 免费模式：配置了 key 也直接放行；真实校验逻辑预留在此。
	return LicenseInfo{Mode: "licensed", Valid: true, Key: maskKey(key), Server: a.cfg.LicenseServerURL}
}

func maskKey(k string) string {
	if len(k) <= 4 {
		return "****"
	}
	return k[:2] + "****" + k[len(k)-2:]
}
