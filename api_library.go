package main

import (
	"net/http"
)

func (a *App) handleMediaFolders(w http.ResponseWriter, r *http.Request, u *User) {
	libs := a.store.orderedLibraries()
	items := make([]map[string]any, 0, len(libs))
	for _, l := range libs {
		items = append(items, map[string]any{
			"Id": l.Id, "Name": l.Name, "Type": "CollectionFolder",
			"IsFolder": true, "CollectionType": l.CollectionType, "ServerId": a.store.ServerId,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"Items": items, "TotalRecordCount": len(items)})
}

func (a *App) handleVirtualFolders(w http.ResponseWriter, r *http.Request, u *User) {
	libs := a.store.orderedLibraries()
	out := make([]map[string]any, 0, len(libs))
	for _, l := range libs {
		out = append(out, map[string]any{
			"Name": l.Name, "ItemId": l.Id, "CollectionType": l.CollectionType,
			"Locations": l.Paths, "LibraryOptions": map[string]any{},
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleRefresh 触发一次全库扫描（入队，异步执行）。
func (a *App) handleRefresh(w http.ResponseWriter, r *http.Request, u *User) {
	a.enqueueScan("")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "扫描已开始"})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
