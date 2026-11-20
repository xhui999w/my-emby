package main

import (
	"encoding/json"
	"mime"
	"net/http"
	"strings"
	"time"
)

func (a *App) handleAuthenticate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	var username, pw string
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if strings.EqualFold(mt, "application/x-www-form-urlencoded") {
		// 部分客户端（如 AfuseKt）用表单提交登录
		_ = r.ParseForm()
		for k, vals := range r.PostForm {
			if len(vals) == 0 {
				continue
			}
			switch strings.ToLower(k) {
			case "username":
				username = vals[0]
			case "pw", "password":
				pw = vals[0]
			}
		}
	} else {
		var body struct {
			Username string `json:"Username"`
			Pw       string `json:"Pw"`
			Password string `json:"Password"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		username = body.Username
		pw = firstNonEmpty(body.Pw, body.Password)
	}
	u := a.store.authenticate(username, pw)
	if u == nil {
		writeErr(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	tok := a.store.issueToken(u)
	a.store.addActivity("用户登录", "用户 "+u.Name+" 已登录", "AuthenticationSuccess", "Information")
	client := firstNonEmpty(r.Header.Get("X-Emby-Client"), r.URL.Query().Get("X-Emby-Client"))
	version := firstNonEmpty(r.Header.Get("X-Emby-Client-Version"), r.URL.Query().Get("X-Emby-Client-Version"))
	deviceName := firstNonEmpty(r.Header.Get("X-Emby-Device-Name"), r.URL.Query().Get("X-Emby-Device-Name"))
	deviceIDv := deviceID(r, u.Id)
	writeJSON(w, http.StatusOK, map[string]any{
		"User":        u.Dto(a.store.ServerId),
		"AccessToken": tok,
		"ServerId":    a.store.ServerId,
		"SessionInfo": map[string]any{
			"Id": newID(), "UserId": u.Id, "UserName": u.Name, "ServerId": a.store.ServerId,
			"Client": client, "ApplicationVersion": version, "DeviceId": deviceIDv, "DeviceName": deviceName,
			"IsActive": true, "SupportsRemoteControl": false, "SupportsMediaControl": false,
			"PlayableMediaTypes": []string{"Video"}, "SupportedCommands": []any{},
			"LastActivityDate": time.Now().UTC().Format(time.RFC3339Nano),
		},
	})
}

func (a *App) handleUsersMe(w http.ResponseWriter, r *http.Request, u *User) {
	writeJSON(w, http.StatusOK, u.Dto(a.store.ServerId))
}

func (a *App) handleGetUsers(w http.ResponseWriter, r *http.Request, u *User) {
	a.store.mu.RLock()
	defer a.store.mu.RUnlock()
	out := make([]UserDto, 0, len(a.store.Users))
	for _, x := range a.store.Users {
		out = append(out, x.Dto(a.store.ServerId))
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *App) handleCreateUser(w http.ResponseWriter, r *http.Request, u *User) {
	var body struct {
		Name             string `json:"Name"`
		Password         string `json:"Password"`
		IsAdmin          bool   `json:"IsAdmin"`
		PlaybackDisabled bool   `json:"PlaybackDisabled"`
		DownloadDisabled bool   `json:"DownloadDisabled"`
		MaxDevices       int    `json:"MaxDevices"`
		MaxActiveDevices int    `json:"MaxActiveDevices"`
		CanPlayback      *bool  `json:"CanPlayback"`
		CanDownload      *bool  `json:"CanDownload"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		writeErr(w, http.StatusBadRequest, "用户名不能为空")
		return
	}
	a.store.mu.Lock()
	for _, x := range a.store.Users {
		if strings.EqualFold(x.Name, body.Name) {
			a.store.mu.Unlock()
			writeErr(w, http.StatusConflict, "用户名已存在")
			return
		}
	}
	maxDev := body.MaxDevices
	if body.MaxActiveDevices > 0 {
		maxDev = body.MaxActiveDevices
	}
	playbackDisabled := body.PlaybackDisabled
	if body.CanPlayback != nil {
		playbackDisabled = !*body.CanPlayback
	}
	downloadDisabled := body.DownloadDisabled
	if body.CanDownload != nil {
		downloadDisabled = !*body.CanDownload
	}
	salt := newID()
	nu := &User{Id: newID(), Name: body.Name, ServerId: a.store.ServerId, Salt: salt,
		IsAdmin: body.IsAdmin, HasPassword: body.Password != "", PassHash: hashPassword(body.Password, salt),
		PlaybackDisabled: playbackDisabled, DownloadDisabled: downloadDisabled, MaxDevices: maxDev}
	a.store.Users[nu.Id] = nu
	a.store.mu.Unlock()
	go a.store.Save()
	a.store.addActivity("新建用户", "创建用户 "+nu.Name, "UserCreated", "Information")
	writeJSON(w, http.StatusOK, nu.Dto(a.store.ServerId))
}

// handleUpdatePolicy 更新用户播放策略（PUT /Users/{id}/Policy）。
func (a *App) handleUpdatePolicy(w http.ResponseWriter, r *http.Request, admin *User, id string) {
	var raw map[string]any
	_ = json.NewDecoder(r.Body).Decode(&raw)
	if p, ok := raw["Policy"].(map[string]any); ok {
		raw = p
	}
	getBool := func(k string) *bool {
		if v, ok := raw[k].(bool); ok {
			return &v
		}
		return nil
	}
	getInt := func(k string) *int {
		if v, ok := raw[k].(float64); ok {
			n := int(v)
			return &n
		}
		return nil
	}
	a.store.mu.Lock()
	x, ok := a.store.Users[id]
	if !ok {
		a.store.mu.Unlock()
		writeErr(w, http.StatusNotFound, "用户不存在")
		return
	}
	if v := getBool("EnableMediaPlayback"); v != nil {
		x.PlaybackDisabled = !*v
	}
	if v := getBool("EnableContentDownloading"); v != nil {
		x.DownloadDisabled = !*v
	}
	if v := getBool("IsAdministrator"); v != nil {
		x.IsAdmin = *v
	}
	if v := getInt("MaxActiveDevices"); v != nil {
		if *v < 0 {
			*v = 0
		}
		x.MaxDevices = *v
	}
	dto := x.Dto(a.store.ServerId)
	a.store.mu.Unlock()
	go a.store.Save()
	a.store.addActivity("更新策略", "更新用户 "+dto.Name+" 的播放策略", "UserPolicyUpdated", "Information")
	writeJSON(w, http.StatusOK, dto)
}

func (a *App) handleDeleteUser(w http.ResponseWriter, r *http.Request, u *User, id string) {
	if id == u.Id {
		writeErr(w, http.StatusBadRequest, "不能删除当前登录用户")
		return
	}
	a.store.mu.Lock()
	delete(a.store.Users, id)
	for t, uid := range a.store.Tokens {
		if uid == id {
			delete(a.store.Tokens, t)
		}
	}
	a.store.mu.Unlock()
	go a.store.Save()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) handleUserViews(w http.ResponseWriter, r *http.Request, u *User) {
	a.store.mu.RLock()
	libs := make([]*Library, 0, len(a.store.Libraries))
	for _, l := range a.store.Libraries {
		libs = append(libs, l)
	}
	a.store.mu.RUnlock()
	items := make([]map[string]any, 0, len(libs))
	for _, l := range libs {
		items = append(items, map[string]any{
			"Id": l.Id, "Name": l.Name, "Type": "CollectionFolder",
			"IsFolder": true, "CollectionType": l.CollectionType,
			"ServerId": a.store.ServerId,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"Items": items, "TotalRecordCount": len(items)})
}

func (a *App) handleGetUser(w http.ResponseWriter, r *http.Request, u *User, id string) {
	a.store.mu.RLock()
	x, ok := a.store.Users[id]
	a.store.mu.RUnlock()
	if !ok {
		writeErr(w, http.StatusNotFound, "用户不存在")
		return
	}
	writeJSON(w, http.StatusOK, x.Dto(a.store.ServerId))
}
