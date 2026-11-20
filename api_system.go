package main

import (
	"net/http"
	"runtime"
)

const serverVersion = "4.8.0.0"

func (a *App) publicSystemInfo() ServerInfo {
	return ServerInfo{
		ServerName:             a.effectiveServerName(),
		Id:                     a.store.ServerId,
		Version:                serverVersion,
		ProductName:            "Emby Server",
		OperatingSystem:        runtime.GOOS,
		StartupWizardCompleted: true,
	}
}

func (a *App) handleSystemInfoPublic(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.publicSystemInfo())
}

func (a *App) handleSystemPing(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Emby Server"))
}

func (a *App) handleSystemInfo(w http.ResponseWriter, r *http.Request, u *User) {
	info := map[string]any{
		"ServerName":                 a.effectiveServerName(),
		"Id":                         a.store.ServerId,
		"Version":                    serverVersion,
		"ProductName":                "Emby Server",
		"OperatingSystem":            runtime.GOOS,
		"StartupWizardCompleted":     true,
		"HasPendingRestart":          false,
		"HasUpdateAvailable":         false,
		"SupportsLibraryMonitor":     true,
		"OperatingSystemDisplayName": runtime.GOOS,
		"SystemUpdateLevel":          "Release",
		"WebSocketPortNumber":        0,
	}
	writeJSON(w, http.StatusOK, info)
}

func (a *App) handleSystemConfiguration(w http.ResponseWriter, r *http.Request, u *User) {
	writeJSON(w, http.StatusOK, map[string]any{
		"EnableRemoteControlOfOtherUsers": true,
		"EnableSharedDeviceControl":       true,
		"EnableRemoteAccess":              true,
		"EnableLiveTvManagement":          false,
		"EnableLiveTvAccess":              false,
		"IsStartupWizardCompleted":        true,
	})
}

func (a *App) handleBranding(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"LoginDisclaimer":     "",
		"SplashscreenEnabled": false,
		"CustomCss":           "",
	})
}

func (a *App) handleLocalizationOptions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, []any{
		map[string]any{"Name": "简体中文", "Value": "zh-CN"},
		map[string]any{"Name": "English", "Value": "en-US"},
	})
}

func (a *App) handleScheduledTasks(w http.ResponseWriter, r *http.Request, u *User) {
	writeJSON(w, http.StatusOK, []any{})
}
