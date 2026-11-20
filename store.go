package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"sort"
	"sync"
	"time"
)

// ---------- 通用小工具 ----------

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// h16 依据稳定输入生成短 id（扫描时用于给条目/库生成固定 Id）。
func h16(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:16]
}

// ---------- 存储层 ----------

// Store 是进程内的数据中心。用户/令牌/媒体库/活动日志/媒体条目
// 通过 Persister 持久化（JSON 文件或 PostgreSQL）。
type Store struct {
	mu        sync.RWMutex
	cfg       Config
	ServerId  string
	Users     map[string]*User
	Tokens    map[string]string // token -> userId
	Libraries map[string]*Library
	Items     map[string]*Item
	Order     []string
	Sessions  map[string]*Session
	Devices   map[string]map[string]int64 // userId -> deviceId -> lastSeen(unix)
	Activity  []Activity
	// 显示名覆盖（itemId -> 自定义名称），跨扫描持久化
	DisplayNames map[string]string
	// 增强设置（key -> value），持久化
	Settings map[string]string
	p        Persister
	backend  string

	scanPauseMu sync.Mutex
	scanResume  chan struct{}
}

type dbUser struct {
	Id               string `json:"id"`
	Name             string `json:"name"`
	PassHash         string `json:"passHash"`
	Salt             string `json:"salt"`
	IsAdmin          bool   `json:"isAdmin"`
	HasPassword      bool   `json:"hasPassword"`
	PlaybackDisabled bool   `json:"playbackDisabled"`
	DownloadDisabled bool   `json:"downloadDisabled"`
	MaxDevices       int    `json:"maxDevices"`
}

// itemRecord 用于持久化，额外带上 Item 的内部索引字段。
type itemRecord struct {
	Item
	LibraryIdX    string          `json:"__libraryId"`
	HasPosterX    bool            `json:"__hasPoster"`
	PosterPathX   string          `json:"__posterPath"`
	HasBackdropX  bool            `json:"__hasBackdrop"`
	BackdropPathX string          `json:"__backdropPath"`
	SourcePathX   string          `json:"__sourcePath"`
	StreamURLX    string          `json:"__streamUrl"`
	SubtitlesX    []SubtitleTrack `json:"__subtitles"`
	ProbedX       bool            `json:"__probed"`
}

func toRecord(it *Item) itemRecord {
	return itemRecord{Item: *it, LibraryIdX: it.LibraryId, HasPosterX: it.HasPoster,
		PosterPathX: it.PosterPath, HasBackdropX: it.HasBackdrop, BackdropPathX: it.BackdropPath,
		SourcePathX: it.SourcePath, StreamURLX: it.StreamURL,
		SubtitlesX: it.Subtitles, ProbedX: it.Probed}
}

func (r itemRecord) toItem() *Item {
	it := r.Item
	it.LibraryId = r.LibraryIdX
	it.HasPoster = r.HasPosterX
	it.PosterPath = r.PosterPathX
	it.HasBackdrop = r.HasBackdropX
	it.BackdropPath = r.BackdropPathX
	it.SourcePath = r.SourcePathX
	it.StreamURL = r.StreamURLX
	it.Subtitles = r.SubtitlesX
	it.Probed = r.ProbedX
	return &it
}

type dbSnapshot struct {
	ServerId     string            `json:"serverId"`
	Users        []dbUser          `json:"users"`
	Tokens       map[string]string `json:"tokens"`
	Libraries    []*Library        `json:"libraries"`
	Activity     []Activity        `json:"activity"`
	Items        []itemRecord      `json:"items"`
	DisplayNames map[string]string `json:"displayNames"`
	Settings     map[string]string `json:"settings"`
}

func NewStore(cfg Config) *Store {
	s := &Store{
		cfg:          cfg,
		ServerId:     newID(),
		Users:        map[string]*User{},
		Tokens:       map[string]string{},
		Libraries:    map[string]*Library{},
		Items:        map[string]*Item{},
		Sessions:     map[string]*Session{},
		Devices:      map[string]map[string]int64{},
		DisplayNames: map[string]string{},
		Settings:     map[string]string{},
	}
	// 选择持久化后端
	if cfg.DatabaseURL != "" {
		if pg, err := newPGPersister(cfg.DatabaseURL); err == nil {
			s.p = pg
			s.backend = "postgres"
			log.Printf("存储后端: PostgreSQL")
		} else {
			log.Printf("PostgreSQL 连接失败(%v)，回退 JSON 文件存储", err)
		}
	}
	if s.p == nil {
		if err := ensureDir(cfg.DataDir); err != nil {
			log.Printf("数据目录创建失败: %v", err)
		}
		s.p = newJSONPersister(cfg.DataDir)
		s.backend = "json"
		log.Printf("存储后端: JSON 文件 (%s)", cfg.DataDir)
	}
	s.load()
	s.bootstrapAdmin()
	return s
}

func (s *Store) load() {
	snap, err := s.p.Load()
	if err != nil || snap == nil {
		return
	}
	if snap.ServerId != "" {
		s.ServerId = snap.ServerId
	}
	for _, u := range snap.Users {
		s.Users[u.Id] = &User{Id: u.Id, Name: u.Name, PassHash: u.PassHash, Salt: u.Salt,
			IsAdmin: u.IsAdmin, HasPassword: u.HasPassword, ServerId: s.ServerId,
			PlaybackDisabled: u.PlaybackDisabled, DownloadDisabled: u.DownloadDisabled,
			MaxDevices: u.MaxDevices}
	}
	if snap.Tokens != nil {
		s.Tokens = snap.Tokens
	}
	for _, l := range snap.Libraries {
		s.Libraries[l.Id] = l
	}
	s.Activity = snap.Activity
	if snap.DisplayNames != nil {
		s.DisplayNames = snap.DisplayNames
	}
	if snap.Settings != nil {
		s.Settings = snap.Settings
	}
	for _, rec := range snap.Items {
		it := rec.toItem()
		s.Items[it.Id] = it
	}
	if len(s.Items) > 0 {
		s.rebuildOrder()
		log.Printf("已从存储加载 %d 个媒体条目", len(s.Items))
	}
}

func (s *Store) snapshot() *dbSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap := &dbSnapshot{ServerId: s.ServerId, Tokens: s.Tokens, Activity: s.Activity, DisplayNames: s.DisplayNames, Settings: s.Settings}
	for _, u := range s.Users {
		snap.Users = append(snap.Users, dbUser{Id: u.Id, Name: u.Name, PassHash: u.PassHash,
			Salt: u.Salt, IsAdmin: u.IsAdmin, HasPassword: u.HasPassword,
			PlaybackDisabled: u.PlaybackDisabled, DownloadDisabled: u.DownloadDisabled,
			MaxDevices: u.MaxDevices})
	}
	for _, l := range s.Libraries {
		snap.Libraries = append(snap.Libraries, l)
	}
	for _, it := range s.Items {
		snap.Items = append(snap.Items, toRecord(it))
	}
	return snap
}

func (s *Store) Save() {
	snap := s.snapshot()
	if err := s.p.Save(snap); err != nil {
		log.Printf("持久化失败: %v", err)
	}
}

func (s *Store) bootstrapAdmin() {
	s.mu.Lock()
	for _, u := range s.Users {
		if u.IsAdmin {
			s.mu.Unlock()
			return
		}
	}
	pass := s.cfg.AdminPassword
	if pass == "" {
		pass = "admin"
	}
	salt := newID()
	u := &User{Id: newID(), Name: "admin", ServerId: s.ServerId, IsAdmin: true,
		HasPassword: true, Salt: salt, PassHash: hashPassword(pass, salt)}
	s.Users[u.Id] = u
	if s.cfg.BootstrapAPIKey != "" {
		s.Tokens[s.cfg.BootstrapAPIKey] = u.Id
	}
	s.mu.Unlock()
	go s.Save()
	if s.cfg.AdminPassword == "" {
		log.Printf("已创建默认管理员 admin / admin（请尽快修改）")
	} else {
		log.Printf("已创建管理员 admin（密码来自 ADMIN_PASSWORD）")
	}
}

func (s *Store) addActivity(name, overview, typ, sev string) {
	s.mu.Lock()
	s.Activity = append([]Activity{{
		Id: newID(), Name: name, ShortOverview: overview, Type: typ,
		Date: time.Now().Format(time.RFC3339), Severity: sev,
	}}, s.Activity...)
	if len(s.Activity) > 500 {
		s.Activity = s.Activity[:500]
	}
	s.mu.Unlock()
}

// rebuildOrder 在扫描完成后重建 Order（按名称排序）。
func (s *Store) rebuildOrder() {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.Items))
	for id := range s.Items {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		return s.Items[ids[i]].Name < s.Items[ids[j]].Name
	})
	s.Order = ids
}

// touchDevice 记录用户设备活动并返回是否放行。max<=0 表示不限设备数。
func (s *Store) touchDevice(userID, deviceID string, ttlSeconds int64, max int) (bool, int) {
	now := time.Now().Unix()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Devices == nil {
		s.Devices = map[string]map[string]int64{}
	}
	m := s.Devices[userID]
	if m == nil {
		m = map[string]int64{}
		s.Devices[userID] = m
	}
	for d, ts := range m {
		if now-ts > ttlSeconds {
			delete(m, d)
		}
	}
	if _, ok := m[deviceID]; ok {
		m[deviceID] = now
		return true, len(m)
	}
	if max > 0 && len(m) >= max {
		return false, len(m)
	}
	m[deviceID] = now
	return true, len(m)
}

// dropDevice 移除某设备（停止播放/退出时调用）。
func (s *Store) dropDevice(userID, deviceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.Devices[userID]; m != nil {
		delete(m, deviceID)
	}
}

// displayNameFor 返回条目的自定义显示名（无则空）。
func (s *Store) displayNameFor(id string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.DisplayNames[id]
}

func (s *Store) setting(k string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Settings[k]
}

func (s *Store) settingBool(k string, def bool) bool {
	v := s.setting(k)
	if v == "" {
		return def
	}
	return v == "true"
}

func (s *Store) setSetting(k, v string) {
	s.mu.Lock()
	if v == "" {
		delete(s.Settings, k)
	} else {
		s.Settings[k] = v
	}
	s.mu.Unlock()
}
