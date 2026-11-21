# Go-Emby · 复刻版（完整形态）

用 Go 从零重写的 **Emby 兼容媒体服务器**，对标开源项目 [`sd87671067/go-emby`](https://github.com/sd87671067/go-emby)
（"go 语言自研的 emby 服务端"）。提供 Emby 兼容 HTTP API、Web 海报墙/管理后台、媒体库扫描、
**直连播放**（跳转媒体源，不转码），并已补齐上游的 **PostgreSQL 持久化、TMDB 刮削、STRM/Xiaoya 同步、免费模式授权**。

- 除 PostgreSQL 驱动 `lib/pq` 外全部使用 Go 标准库；`lib/pq` 已置于 `third_party/`，`go build` 离线可编译。
- 不配置数据库也能跑：自动回退 JSON 文件存储。

## 功能

- **Emby 兼容接口**：`/System/Info/Public`、`/Users/AuthenticateByName`、`/Users/{id}/Views`、
  `/Users/{id}/Items`、`/Items/{id}`、`/Items/{id}/PlaybackInfo`、`/Videos/{id}/stream` 等，可连接 Infuse/Yamby/SenPlayer 等客户端。
- **媒体库**：扫描 `MEDIA_ROOTS` 视频文件，自动区分电影/剧集（`SxxExx`/`NxM`），建立 剧→季→集 层级。
  **首次扫描按一级子目录自动拆分媒体库**（`MEDIA_AUTO_SPLIT`，默认开）：`/media/国产剧`、`/media/华语电影` … 各建一库，
  类型按名字识别（剧/动漫/动画/综艺→电视剧）；根目录直属影片归入以根命名的库；库路径互斥扫描不重复导入。
- **实时监听 + 增量扫描**：轮询监听媒体目录，新增/删除/改名/字幕或海报变化自动触发**按库增量重扫**（无需手动刷新）；
  扫描**队列**串行化、`GET/PUT /admin/scan` 可**暂停/继续**扫描。
- **ffprobe 媒体信息探测**：扫描后并发调用 ffprobe，填充真实分辨率/编码/音轨/字幕/时长/码率/容器到 `MediaStreams` 并持久化。
- **文件管理**：`/api/files` 浏览/搜索/重命名文件夹/批量删除，路径穿越与媒体库根保护，变更后自动增量重扫。
- **媒体编辑/删除**：改自定义显示名（跨重扫持久化），删除媒体源/STRM/目录（安全校验：限媒体目录、禁删库根）。
- **图片上传**：`POST /Items/{id}/Images/{Primary|Backdrop}`（raw body）自定义海报/背景。
- **缩略图生成**：`GET /Items/{id}/Images/Thumb` 用 ffmpeg 懒生成并缓存（`THUMBNAIL_ENABLED`/`FFMPEG_PATH`）。
- **日志体系**：HTTP 4xx/5xx 错误日志（含状态码+响应体片段）、播放重定向日志、活动日志；
  `GET /admin/logs?type=error|redirect|activity|all&limit=` 按类型过滤。
- **增强设置**：`GET/PUT /admin/enhancements`（ServerName、FastCDN、ProxyDebug、首字母搜索、扫描/更新并发、
  监听延时、第三方反代端口、隐藏缺失演员头像、合并版本），持久化到 JSON/PG；`ServerName`/首字母搜索/`ProxyDebug` 已实际生效。
- **客户端兼容层**：NFO 富元数据（Genres/People/Studios/ProviderIds/评分/分级/Taglines/原始标题/首播/Studio 数字 ID）、
  `MediaSource.Path` 依 `X-Forwarded-Proto/Host` 生成绝对地址、`/Videos/{id}/stream.{ext}`、
  `IncludeItemTypes` 规范化 + Lenna quirk、`/Items/{id}/Similar` 相似推荐、parentless/`CollectionFolder` 库发现、
  **`/emby` 前缀 + 重复斜杠 + 大小写不敏感路由**、**表单(x-www-form-urlencoded)登录**、**Authorization 头/查询参数令牌与设备解析**、
  `/Shows/{id}/Episodes|Seasons`（Infuse）、Episode 输出 `Etag/SeriesId/SeasonId/SeasonName`。
- **更多客户端发现接口**：`/Search/Hints`、`/Genres`、`/Persons`、`/Studios`、`/Years`、`/Items/Filters`、
  `/System/Endpoint`、`/DisplayPreferences/{id}`，返回 Emby 兼容结构。
- **Web 后台**：中英双语切换（右上角语言选择器，标签级 i18n）；增强设置面板含 MergeVersions/文件监听/FastCDN/代理调试等开关。
- **多版本合并**：`merge_versions_folder` / `merge_versions_libraries` 开关，按 名称+年份(电影) / 剧+季+集(剧集) 分组，
  列表去重、`PlaybackInfo` 合并多版本 `MediaSources`。
- **授权/设备绑定**：`LICENSE_ENFORCE=true` 时按设备指纹 `POST {LICENSE_SERVER_URL}/v1/check` 取租约，
  未授权对所有接口返回 **402**；提供 `/license/status`、`/license/refresh`。默认关闭=免费模式。
- **元数据**：读取同名 `.nfo`；支持 **TMDB 刮削**（简介/年份/海报自动获取与本地缓存）。
- **多源刮削**：TMDB 优先、**豆瓣（subject_suggest）兜底**，`DOUBAN_ENABLED=true` 即启用。
- **外挂字幕增强**：自动识别同名外挂字幕（srt/ass/ssa/vtt/sub）、语言识别与默认轨、
  `MediaStreams` 暴露字幕轨、`/Videos/{id}/{src}/Subtitles/{index}/Stream.{ext}` 端点，
  支持 **SRT↔VTT 转换** 与 **UTF-16/BOM 自动转 UTF-8**。
- **用户播放权限与设备限制**：按用户禁用播放/下载、**最大并发设备数**（`MaxActiveDevices`），
  PlaybackInfo/stream/download 强制校验；`PUT /Users/{id}/Policy` 管理，后台可编辑。
- **STRM 直链**：`.strm` 文件读取一行 URL，播放直接 302 跳转；支持 **Xiaoya/OpenList(WebDAV) 定时同步**，自动生成 `.strm` 树。
- **持久化**：**PostgreSQL**（`users/auth_tokens/libraries/media_items/activity_log`，启动自动建表）；
  未配置则 **JSON 文件**。媒体条目也入库，重启免重扫；NAS 掉线（媒体根不存在）时保留缓存条目不丢库。
- **用户/权限/后台**：登录令牌、管理员/普通用户、用户增删、播放进度、活动日志、一键刷新、手动刮削/同步、统计。
- **授权**：免费模式（`LICENSE_KEY` 留空即放行），保留授权入口。
- **部署**：Dockerfile + `compose.yaml`（含 PostgreSQL）+ `run.cmd`。

## 快速开始

### 本地运行（JSON 存储，最简单）

```bash
cd my-emby
go build -o go-emby .
MEDIA_ROOTS=/path/to/media ADMIN_PASSWORD=admin ./go-emby
```

Windows：双击 `run.cmd`，或

```powershell
$env:MEDIA_ROOTS="D:\media"; $env:ADMIN_PASSWORD="admin"; .\go-emby.exe
```

访问 `http://127.0.0.1:8097/`，账号 `admin / admin`。

### 本地运行（PostgreSQL）

```bash
export DATABASE_URL="postgres://postgres:postgres@127.0.0.1:5432/goemby?sslmode=disable"
./go-emby
```

### Docker Compose（含 PostgreSQL）

```bash
cp .env.example .env
# 编辑 .env：MEDIA_PATH 指向影视目录；可选填 TMDB_API_KEY / XIAOYA_*
docker compose up -d --build
```

## 环境变量

| 变量 | 说明 | 默认值 |
| --- | --- | --- |
| `HTTP_PORT` | 监听端口 | `8097` |
| `MEDIA_ROOTS` | 媒体根目录（`:`/`;` 分隔，容器内路径） | `./media` |
| `MEDIA_AUTO_SPLIT` | 首次扫描按一级子目录自动拆分媒体库 | `true` |
| `APP_DATA_PATH` | 数据目录（JSON 库 / 海报缓存 / strm） | `./data` |
| `ADMIN_PASSWORD` | 首次初始化管理员密码 | `admin` |
| `DATABASE_URL` | PostgreSQL DSN；留空用 JSON | 空 |
| `TMDB_API_KEY` | TMDB v3 Key，填入即启用刮削 | 空 |
| `TMDB_LANGUAGE` | 刮削语言 | `zh-CN` |
| `TMDB_BASE` / `TMDB_IMAGE_BASE` | API/图片地址（可反代或测试） | 官方 |
| `XIAOYA_SOURCE` | 远端 WebDAV 根（如 `http://host:5244/dav/电影`） | 空 |
| `XIAOYA_USERNAME` / `XIAOYA_PASSWORD` | WebDAV 账号 | `dav` / 空 |
| `XIAOYA_INTERVAL_MIN` | 定时同步间隔（分钟，0=不定时） | `0` |
| `WATCH_ENABLED` / `WATCH_INTERVAL_SEC` | 是否轮询监听媒体目录 / 轮询秒数 | `true` / `8` |
| `PROBE_ENABLED` / `FFPROBE_PATH` / `PROBE_CONCURRENCY` | 是否 ffprobe 探测 / ffprobe 路径 / 并发 | `true` / `ffprobe` / `2` |
| `FILE_MANAGER_ROOT` | 文件管理根目录（默认第一个媒体根） | 空 |
| `THUMBNAIL_ENABLED` / `FFMPEG_PATH` | 是否生成缩略图 / ffmpeg 路径 | `true` / `ffmpeg` |
| `XIAOYA_PUBLIC_BASE` | `.strm` 直链前缀（留空则把 `/dav` 换 `/d`） | 空 |
| `LICENSE_KEY` | 授权码（免费模式留空） | 空 |
| `LICENSE_ENFORCE` | 是否强制授权校验（未授权返回 402） | `false` |

## 项目结构

```
my-emby/
├── main.go            # 路由与入口，启动扫描/刮削/同步
├── config.go          # 环境变量
├── models.go          # API 模型（Emby 字段）
├── store.go           # 内存中心 + 快照持久化
├── persistence.go     # Persister 接口 + JSON 后端
├── pg.go              # PostgreSQL 后端（自动建表）
├── auth.go            # 登录/令牌/鉴权
├── media.go           # 媒体扫描、NFO/海报、STRM 解析、层级建模
├── tmdb.go            # TMDB 刮削客户端
├── xiaoya.go          # WebDAV PROPFIND + STRM 同步
├── license.go         # 免费模式授权
├── api_*.go           # System/Users/Items/Library/Admin 接口
├── images.go          # 海报/占位图
├── stream.go          # 直连播放（HTTP Range / STRM 302）
├── web.go / web/      # 内嵌 Web 后台与海报墙
├── third_party/libpq  # 内置 lib/pq（go.mod replace，离线可编译）
├── Dockerfile / compose.yaml / .env.example / run.cmd
└── testdata/mocktmdb.py  # TMDB 本地 mock（测试用）
```

## 已验证
- **实时监听/增量**：向媒体目录加入新视频约 1s 内自动出现在库中；删除后约 2s 自动消失（免手动刷新）。
- **扫描暂停**：`GET /admin/scan` 返回 `Paused=false`，`PUT {Paused:true}` 后变 `true`，恢复后 `false`。
- **ffprobe 探测**：真实生成的 320x240/h264/2s 测试视频，探测出 Video(320x240,h264)+Audio(aac,1ch)，`RunTimeTicks=20000000`、`Size` 正确。
- **文件管理**：列出/重命名 `Show→Show2` 生效；`../` 越权与删除根目录均 400；删除文件生效。
- **媒体改名/删除**：改名跨全量重扫仍保留（UTF-8 中文正确）；删除媒体后磁盘文件与条目均消失。
- **图片上传/缩略图**：上传 70B PNG 后 `Images/Primary` 原样取回；`Images/Thumb` 用 ffmpeg 生成 6657B JPEG（`FF D8`）。
- **错误日志**：请求不存在条目 → 产生 `error` 日志（`GET /Items/... :: 404 {...}`）；`type=activity` 过滤不含错误/重定向。
- **重定向日志**：STRM 播放 302 → 产生 `PlaybackRedirect` 日志（含目标 URL）。
- **增强设置**：PUT 后 `/System/Info/Public` 的 ServerName 即时变更；首字母搜索开关生效；重启后全部设置持久化。
- **客户端兼容**：NFO 富元数据（Genres/Studios/People 演员导演/ProviderIds/评分 8.8/PG-13/Taglines/原始标题/首播）正确输出；
  `X-Forwarded-Proto/Host` 生成绝对 `Path`；`/Videos/{id}/stream.mp4` 返回 200；`IncludeItemTypes=MOVIE` 规范化命中；Similar 命中同题材。
- **客户端 quirk（对齐 AfuseKt/Infuse/SenPlayer/Lenna）**：`//emby//emby/Users//authenticatebyname/` 表单登录成功；`/emby/system/ping` GET/POST 200；
  OPTIONS 204+CORS；`Authorization: MediaBrowser ..., Token=..., DeviceId=...` 头与查询参数均可鉴权；`/Shows/{id}/Episodes` 返回 2 集且含 `Etag/SeasonId/SeasonName`、`SupportsTranscoding=false`；库发现三种入口均返回 `CollectionType`；Studio 数字 ID 为正且唯一。
- **发现接口**：`/Search/Hints?SearchTerm=ince` 命中 Inception；`/Genres`=Action,Science Fiction；`/Persons`=3 人（演员+导演）；`/Studios` 数字 ID；`/Years`=2010；`/Items/Filters` 返回题材/年份；`/System/Endpoint`={IsLocal,IsInNetwork}；`/DisplayPreferences/testuser` 存取生效（theme=dark）。
- **后台多语言/设置**：语言选择器中文/English 切换生效；增强设置面板写 `MergeVersionsInFolder/WatchEnabled/FastCDN` 后回读为 True。
- **多版本合并**：`MergeVersionsInFolder=true` 时 Inception 两个版本合并为 1 条、`MediaSources=2`；关闭后恢复 3 条。
- **授权（mock）**：`LICENSE_ENFORCE=true` + `key=good` → 状态 valid、login 200；`key=bad` → `/System/Info/Public` 402 `license_required`、`/health` 200。

- `gofmt` 干净、`go vet` 通过、`go build` 通过。
- JSON 模式：登录、库、条目、NFO 简介/时长、海报、Range 206、活动日志、401。
- **PostgreSQL 模式**：`users/tokens/libraries/media_items` 落库；换空数据目录 + 媒体离线重启后仍从 PG 恢复条目并正常登录。
- **TMDB 刮削**：本地 mock 验证简介/年份/海报下载并缓存。
- **STRM**：`.strm` 条目 `DirectStreamUrl` 为远端直链，`/Videos/{id}/stream` 返回 302 跳转。
- **字幕增强**：多语种字幕轨暴露、SRT 原样返回、SRT→VTT 转换、UTF-16 字幕正确解码。
- **权限/设备**：非管理员用户 `MaxActiveDevices=1` 时第二设备 PlaybackInfo 返回 403；禁用播放/下载分别返回 403；管理员不受限。
- **豆瓣刮削**：本地 mock 验证年份/海报补齐（`DOUBAN_ENABLED=true`）。
- **PostgreSQL 策略列**：`users` 表新增 `playback_disabled/download_disabled/max_devices`（含 ALTER 迁移），重启后策略保持。

## 与上游差异

- 上游使用独立的授权服务与 Xiaoya 专用逻辑；本复刻保留免费模式授权入口与标准 WebDAV STRM 同步。
- 本复刻面向可运行、可读、可二次开发，SQL 采用「快照式全量 upsert」而非复杂增量迁移；生产超大媒体库可再优化。

## 说明与致谢

本项目为**独立实现的 Emby 兼容媒体服务器**，接口与功能对齐参考开源项目 [`sd87671067/go-emby`](https://github.com/sd87671067/go-emby)，
但**不包含其任何源码**，均为从零编写。
第三方依赖：`third_party/libpq` 为 [github.com/lib/pq](https://github.com/lib/pq)（MIT 许可），其 LICENSE 随源码一并保留。

## License

本项目采用 MIT 许可证，详见 [LICENSE](LICENSE)。

