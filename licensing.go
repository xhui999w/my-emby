package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const licenseMaxLease = 43260

// licenseClient 是授权/设备绑定的客户端实现（对标上游 internal/licensesdk）。
type licenseClient struct {
	mu      sync.RWMutex
	url     string
	key     string
	device  string
	until   time.Time
	mode    string
	reason  string
	http    *http.Client
	enabled bool
}

// deviceFingerprint 优先读取机器标识文件，否则以主机名派生稳定设备指纹。
func deviceFingerprint() string {
	f := os.Getenv("LICENSE_MACHINE_ID_FILE")
	if f == "" {
		f = "/run/license-machine-id"
	}
	if raw, err := os.ReadFile(f); err == nil {
		id := strings.TrimSpace(string(raw))
		if len(id) == 32 {
			if _, e := hex.DecodeString(id); e == nil {
				sum := sha256.Sum256([]byte("go-emby-device-v1:" + strings.ToLower(id)))
				return hex.EncodeToString(sum[:])
			}
		}
	}
	host, _ := os.Hostname()
	sum := sha256.Sum256([]byte("go-emby-device-v1:" + strings.ToLower(host)))
	return hex.EncodeToString(sum[:])
}

func newLicenseClient(cfg Config) *licenseClient {
	return &licenseClient{
		url:     strings.TrimRight(strings.TrimSpace(cfg.LicenseServerURL), "/"),
		key:     strings.TrimSpace(cfg.LicenseKey),
		device:  deviceFingerprint(),
		reason:  "not_verified",
		http:    &http.Client{Timeout: 8 * time.Second},
		enabled: cfg.LicenseEnforce && strings.TrimSpace(cfg.LicenseKey) != "",
	}
}

func (c *licenseClient) refresh(ctx context.Context) {
	if c.url == "" {
		return
	}
	payload, _ := json.Marshal(map[string]any{"license": c.key, "device": c.device, "lease_seconds": licenseMaxLease})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url+"/v1/check", bytes.NewReader(payload))
	if err != nil {
		c.setErr("request: " + err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		c.setErr("connect: " + err.Error())
		return
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		c.setErr(fmt.Sprintf("HTTP %d", res.StatusCode))
		return
	}
	var r struct {
		Valid  bool   `json:"valid"`
		Mode   string `json:"mode"`
		Reason string `json:"reason"`
		Lease  int    `json:"lease_seconds"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&r); err != nil {
		c.setErr("json: " + err.Error())
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !r.Valid {
		c.until = time.Time{}
		c.mode = ""
		c.reason = r.Reason
		if c.reason == "" {
			c.reason = "denied"
		}
		return
	}
	if r.Lease <= 0 || r.Lease > licenseMaxLease {
		r.Lease = licenseMaxLease
	}
	c.until = time.Now().Add(time.Duration(r.Lease) * time.Second)
	c.mode = r.Mode
	c.reason = ""
}

func (c *licenseClient) setErr(detail string) {
	log.Printf("license: %s", detail)
	c.mu.Lock()
	if !time.Now().Before(c.until) {
		c.reason = detail
	}
	c.mu.Unlock()
}

func (c *licenseClient) status() (bool, string, string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if time.Now().Before(c.until) {
		return true, c.mode, ""
	}
	reason := c.reason
	if reason == "" {
		reason = "lease_expired"
	}
	return false, c.mode, reason
}

func (c *licenseClient) run(ctx context.Context) {
	if !c.enabled {
		return
	}
	c.refresh(ctx)
	t := time.NewTicker(12 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.refresh(ctx)
		}
	}
}

// licenseBlocked 在启用授权校验时拦截未授权请求。
func (a *App) licenseBlocked(w http.ResponseWriter, r *http.Request) bool {
	if a.license == nil || !a.license.enabled {
		return false
	}
	switch r.URL.Path {
	case "/health", "/license/status", "/license/refresh":
		return false
	}
	valid, mode, reason := a.license.status()
	if !valid {
		writeJSON(w, http.StatusPaymentRequired, map[string]any{
			"error": "license_required", "reason": reason, "mode": mode,
			"message": "请在 LICENSE_KEY 配置有效授权码",
		})
		return true
	}
	return false
}

func (a *App) handleLicenseStatus(w http.ResponseWriter, r *http.Request) {
	if a.license == nil {
		writeJSON(w, http.StatusOK, map[string]any{"valid": true, "mode": "disabled", "reason": ""})
		return
	}
	valid, mode, reason := a.license.status()
	writeJSON(w, http.StatusOK, map[string]any{
		"valid": valid, "mode": mode, "reason": reason,
		"enabled": a.license.enabled, "device": a.license.device[:12] + "…",
	})
}

func (a *App) handleLicenseRefresh(w http.ResponseWriter, r *http.Request) {
	if a.license != nil {
		a.license.refresh(r.Context())
	}
	a.handleLicenseStatus(w, r)
}
