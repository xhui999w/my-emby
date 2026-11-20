package main

// ---------- 对外 API 数据模型（字段名与 Emby 客户端约定保持一致） ----------

type User struct {
	Id          string `json:"Id"`
	Name        string `json:"Name"`
	ServerId    string `json:"ServerId,omitempty"`
	HasPassword bool   `json:"HasPassword"`
	IsAdmin     bool   `json:"IsAdmin"`
	// 播放权限与设备限制（零值=允许/不限）
	PlaybackDisabled bool `json:"-"`
	DownloadDisabled bool `json:"-"`
	MaxDevices       int  `json:"-"`
	// 仅内部使用，不进入 API JSON
	PassHash string `json:"-"`
	Salt     string `json:"-"`
}

// UserPolicy / Configuration 让部分客户端在登录后能拿到配置而不报错。
type UserPolicy struct {
	IsAdministrator                 bool `json:"IsAdministrator"`
	EnableAllFolders                bool `json:"EnableAllFolders"`
	EnableRemoteControlOfOtherUsers bool `json:"EnableRemoteControlOfOtherUsers"`
	EnableMediaPlayback             bool `json:"EnableMediaPlayback"`
	EnableAudioPlaybackTranscoding  bool `json:"EnableAudioPlaybackTranscoding"`
	EnableVideoPlaybackTranscoding  bool `json:"EnableVideoPlaybackTranscoding"`
	EnablePlaybackRemuxing          bool `json:"EnablePlaybackRemuxing"`
	EnableContentDownloading        bool `json:"EnableContentDownloading"`
	MaxActiveDevices                int  `json:"MaxActiveDevices"`
}

type UserDto struct {
	Id          string     `json:"Id"`
	Name        string     `json:"Name"`
	ServerId    string     `json:"ServerId,omitempty"`
	HasPassword bool       `json:"HasPassword"`
	IsAdmin     bool       `json:"IsAdmin"`
	Policy      UserPolicy `json:"Policy"`
}

func (u *User) Dto(serverId string) UserDto {
	return UserDto{
		Id: u.Id, Name: u.Name, ServerId: serverId, HasPassword: u.HasPassword, IsAdmin: u.IsAdmin,
		Policy: UserPolicy{
			IsAdministrator:                u.IsAdmin,
			EnableAllFolders:               true,
			EnableMediaPlayback:            !u.PlaybackDisabled,
			EnableAudioPlaybackTranscoding: true,
			EnableVideoPlaybackTranscoding: true,
			EnablePlaybackRemuxing:         true,
			EnableContentDownloading:       !u.DownloadDisabled,
			MaxActiveDevices:               u.MaxDevices,
		},
	}
}

type MediaStream struct {
	Type                 string `json:"Type"` // Video / Audio / Subtitle
	Index                int    `json:"Index"`
	Codec                string `json:"Codec,omitempty"`
	Width                int    `json:"Width,omitempty"`
	Height               int    `json:"Height,omitempty"`
	Language             string `json:"Language,omitempty"`
	IsExternal           bool   `json:"IsExternal,omitempty"`
	DisplayTitle         string `json:"DisplayTitle,omitempty"`
	IsDefault            bool   `json:"IsDefault,omitempty"`
	IsForced             bool   `json:"IsForced,omitempty"`
	IsTextSubtitleStream bool   `json:"IsTextSubtitleStream,omitempty"`
	Channels             int    `json:"Channels,omitempty"`
	BitRate              int64  `json:"BitRate,omitempty"`
	Profile              string `json:"Profile,omitempty"`
}

// SubtitleTrack 描述一个外挂字幕轨。
type SubtitleTrack struct {
	Index     int    `json:"Index"`
	Language  string `json:"Language"`
	Title     string `json:"Title"`
	Path      string `json:"Path"`
	Format    string `json:"Format"` // srt / ass / ssa / vtt / sub
	IsDefault bool   `json:"IsDefault"`
	IsForced  bool   `json:"IsForced"`
}

type MediaSource struct {
	Id                   string        `json:"Id"`
	Name                 string        `json:"Name"`
	Path                 string        `json:"Path"`
	Container            string        `json:"Container"`
	Size                 int64         `json:"Size,omitempty"`
	RunTimeTicks         int64         `json:"RunTimeTicks,omitempty"`
	SupportsDirectPlay   bool          `json:"SupportsDirectPlay"`
	SupportsDirectStream bool          `json:"SupportsDirectStream"`
	SupportsTranscoding  bool          `json:"SupportsTranscoding"`
	DirectStreamUrl      string        `json:"DirectStreamUrl"`
	MediaStreams         []MediaStream `json:"MediaStreams,omitempty"`
}

type UserData struct {
	PlaybackPositionTicks int64 `json:"PlaybackPositionTicks"`
	PlayCount             int   `json:"PlayCount"`
	IsFavorite            bool  `json:"IsFavorite"`
	Played                bool  `json:"Played"`
}

type Item struct {
	Id                string            `json:"Id"`
	Name              string            `json:"Name"`
	ServerId          string            `json:"ServerId"`
	Type              string            `json:"Type"` // Movie / Episode / Series / Season / Folder / CollectionFolder
	MediaType         string            `json:"MediaType,omitempty"`
	ParentId          string            `json:"ParentId,omitempty"`
	SeriesId          string            `json:"SeriesId,omitempty"`
	SeriesName        string            `json:"SeriesName,omitempty"`
	SeasonId          string            `json:"SeasonId,omitempty"`
	SeasonName        string            `json:"SeasonName,omitempty"`
	IndexNumber       int               `json:"IndexNumber,omitempty"`
	ParentIndexNumber int               `json:"ParentIndexNumber,omitempty"`
	ProductionYear    int               `json:"ProductionYear,omitempty"`
	Overview          string            `json:"Overview,omitempty"`
	RunTimeTicks      int64             `json:"RunTimeTicks,omitempty"`
	Path              string            `json:"Path,omitempty"`
	Container         string            `json:"Container,omitempty"`
	PremiereDate      string            `json:"PremiereDate,omitempty"`
	Etag              string            `json:"Etag,omitempty"`
	IsFolder          bool              `json:"IsFolder"`
	ImageTags         map[string]string `json:"ImageTags,omitempty"`
	BackdropImageTags []string          `json:"BackdropImageTags,omitempty"`
	MediaSources      []MediaSource     `json:"MediaSources,omitempty"`
	UserData          *UserData         `json:"UserData,omitempty"`
	// 客户端元数据（来自 NFO / 刮削）
	Genres          []string          `json:"Genres,omitempty"`
	People          []Person          `json:"People,omitempty"`
	Studios         []Studio          `json:"Studios,omitempty"`
	ProviderIds     map[string]string `json:"ProviderIds,omitempty"`
	CommunityRating float64           `json:"CommunityRating,omitempty"`
	OfficialRating  string            `json:"OfficialRating,omitempty"`
	Taglines        []string          `json:"Taglines,omitempty"`
	OriginalTitle   string            `json:"OriginalTitle,omitempty"`
	// 内部字段（扫描索引，不进入 API JSON）
	LibraryId    string          `json:"-"`
	HasPoster    bool            `json:"-"`
	PosterPath   string          `json:"-"`
	HasBackdrop  bool            `json:"-"`
	BackdropPath string          `json:"-"`
	SourcePath   string          `json:"-"`
	StreamURL    string          `json:"-"` // .strm 指向的远端直链
	Subtitles    []SubtitleTrack `json:"-"` // 外挂字幕轨
	Probed       bool            `json:"-"` // 是否已完成 ffprobe 探测
}

type Library struct {
	Id             string   `json:"Id"`
	Name           string   `json:"Name"`
	Paths          []string `json:"Paths"`
	CollectionType string   `json:"CollectionType"` // movies / tvshows
}

type Session struct {
	Id            string `json:"Id"`
	UserId        string `json:"UserId"`
	DeviceName    string `json:"DeviceName,omitempty"`
	Client        string `json:"Client,omitempty"`
	ItemId        string `json:"ItemId,omitempty"`
	PositionTicks int64  `json:"PositionTicks,omitempty"`
	LastActivity  int64  `json:"LastActivityDate"`
}

type Activity struct {
	Id            string `json:"Id"`
	Name          string `json:"Name"`
	ShortOverview string `json:"ShortOverview,omitempty"`
	Type          string `json:"Type"`
	Date          string `json:"Date"`
	Severity      string `json:"Severity"`
}

type Person struct {
	Id   string `json:"Id,omitempty"`
	Name string `json:"Name"`
	Role string `json:"Role,omitempty"`
	Type string `json:"Type"` // Actor / Director
}

type Studio struct {
	Name string `json:"Name"`
	Id   int64  `json:"Id,omitempty"`
}

type ServerInfo struct {
	ServerName             string `json:"ServerName"`
	Id                     string `json:"Id"`
	Version                string `json:"Version"`
	ProductName            string `json:"ProductName"`
	OperatingSystem        string `json:"OperatingSystem"`
	StartupWizardCompleted bool   `json:"StartupWizardCompleted"`
}
