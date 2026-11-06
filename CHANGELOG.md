# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Security
- 升级受影响的依赖以通过 govulncheck：`github.com/andybalholm/brotli` → v1.2.8（GO-2026-6888）、`github.com/klauspost/compress` → v1.20.3（GO-2026-6865）。

### Fixed
- **批量测速致命失败（`bind: cannot assign requested address`）**：每个节点测试都会起一个临时 sing-box 实例并各自监听一个临时端口，大批量时把本地端口耗尽，导致后续节点全部报 `create instance: start sing-box: start inbound/mixed[test-…]`。现在临时实例并发上限 8 个，且对端口耗尽/占用类错误做 4 次退避重试。
- **运行时监听检查不再误杀节点**：节点尚未分配运行时端口时，跳过该附加检查（直连探测已通过），不再返回 `runtime listener port is not assigned` 判失败。

- **导入/测速仍报 TLS 校验失败**：还剩一处严格校验——走「运行时监听端口」的探测客户端仍用 `skip_cert_verify`。现在与直连探测一致，忽略目标证书（机场重签 TLS 不再误杀节点）。
- **`no valid nodes available` 无法定位**：全节点构建失败时错误里只有数量。现在会带上前 3 条具体原因（例如 `broken: vless uri missing uuid in userinfo`），日志与接口都能直接看到。

- **`manager not started` 导致检测结果无法应用**：运行时未启动（首次启动失败，如端口被占用）时，`Reload` 直接报错并拒绝，导入/检测结果因此永远应用不上。现在未运行时重载会**就地采用新配置并尝试启动**，失败才返回真实原因。
- **站点验收误杀大批节点**：移除内置 ProxySpace（走代理时多为非 2xx 或 JS 壳，造成近乎全量失败）；站点验收本身已改为可选，不勾选时只按探测目标判定。

- **站点验收不再导致大批 TLS 失败**：站点探测（`newConnectivityHTTPClient`）此前用 `RootCAs` 严格校验目标证书，机场/中间设备重签 TLS 时几乎所有节点都被判失败；现在与节点探测一致，只判定可达性（仍记录 TLS 版本）。
- **站点验收改为可选**：不再强制"至少选一项"，全不勾选时仅按探测目标判定。

### Added
- 站点验收区新增「＋ 自定义站点」按钮，可直接在弹窗里加站点（名称/URL/marker），保存后同时出现在站点检测与验收里。

- **设置页探测配置重复**：基础运行区与订阅刷新区各有一份目标/判定控件，现已收敛为订阅刷新处一份（顶栏「探测配置」弹窗仍可随时打开），并精简了重复的指引文案。

- **端口冲突不再导致容器无限重启**：入口端口被占用导致核心启动失败时，进程不再退出，保留管理界面并每 30 秒自动重试，端口释放后自动恢复（此前配合 `restart: unless-stopped` 会形成无限重启）。
- **管理端口被占用时自动换端口**（+1…+20），并在日志中写出实际绑定地址。
- **启动失败原因可见**：`/api/diagnostics` 的 `runtime.start_failure` 与仪表盘横幅展示原因与处理建议。

### Changed
- **回滚「验收可选」**：恢复为「探测目标 / 站点」必须至少启用一项，全部关闭时无法开始检测。
- **自定义验收站点保留并支持删除**：验收区里自定义站点按钮旁新增 `×` 删除键（带确认弹窗），删除后同步清除各处已选中的勾选；内置站点不可删。

- **探测配置入口统一**：导入节点 / 订阅刷新 / 基础运行（以及批量工具）各放**同一个**紧凑组件（目标数 + 判定方式 + 目标列表 + 编辑按钮），样式与位置一致；旧的「节点可用条件」里重复的「探测目标」按钮已删除，站点验收只保留 Google/GitHub/Outlook/ProxySpace 等站点项。
- **静态资源带版本号**：首页不再缓存，`index.html` 注入 `window.__BUILD__` 且 JS/CSS 引用追加 `?v=<sha>`，避免升级后旧 JS 与新 HTML 混用（表现为新旧 UI 同时存在、侧栏无版本号）。

- **探测配置统一**：导入页 / 批量工具 / 设置「基础运行」三处改为同一块的「摘要 + 编辑探测配置」按钮，编辑器只在顶栏「探测配置」弹窗里一处；订阅刷新改为只读引用基础运行的那份配置（不再有自己的控件）。
- **诊断页新增 SNI 探测**：输入目标与 SNI，直接看到「进程直连 / 经面板池口」两跳的成功、耗时、错误与结论，用于定位 RST 发生在哪一跳。

- 前置代理的"测试连接"改为**经该链访问 GitHub**（`https://github.com/`，要求 2xx 且页面含 github 标记），与节点导入里的站点检测口径一致；此前用 generate_204 只能证明通了一个 204，无法说明链能承载真实流量。

### Added
- **订阅拉取适配机场限制**：按源可配 `user_agent` / `headers`（Referer 等）；未配置时自动按 clash-verge → clash.meta → v2rayNG → Shadowrocket → 默认 依次尝试，并记住该源成功的客户端 UA（进程内）。返回内容不是订阅（HTML 错误页/空响应）时同样换 UA 重试。
- **拉取失败可读诊断**：403/401（UA 或登录限制）、404/410（地址失效）、429（限流）、5xx、证书错误、超时分别给出可操作提示，展示在导入来源卡片上。
- **订阅配额与到期**：解析 `Subscription-Userinfo`（upload/download/total/expire），导入来源卡片显示已用/总量与到期日。
- **刷新抖动与退避**：定时刷新在间隔上叠加 ±10% 抖动，连续失败按 2^n 退避（上限 30 分钟），避免整点风暴与限流。
- 说明：拉取回退（`auto` 策略按顺序尝试池节点）此前已具备，本次确认并纳入文档。
- **探测目标支持多目标 + 策略**：`management.probe_targets`（任意 http(s) URL 或 host:port，可自定义，UI 每行一个并带预设）与 `management.probe_policy`（`any` 任一通过即算可用 / `all` 全部通过才算可用）。探测按目标顺序回退，节点屏蔽某个 generate_204 端点时不再被误判为不可用；请求路径也不再写死 `/generate_204`。
- **DNS 解析**：`dns` 段（server/port/fallback_servers/strategy）真正接入 sing-box（udp/tcp/tls 传输 + 主/备服务器 + 解析策略），设置页可编辑；节点只给域名时不再依赖系统解析器。
- **设置页补齐上游缺项**：管理密码（留空不修改、可勾选清除）、共享入口用户名/密码、GeoIP（启用/数据库/监听/端口/自动更新/间隔）、探测并发。`GET /api/settings` 不再回传管理密码明文，只返回 `has_password`；`PUT` 省略 `password` 表示保持不变，`""` 表示清除。
- **补齐上游面板能力到新 UI**：新增「诊断」页（每个节点的成功率/最后错误/时间线，支持立即探测、拉黑、解封）、仪表盘实时流量（`/api/traffic` SSE + 60 秒速率曲线）、入口链接一键复制（HTTP/SOCKS5/curl）、黑名单节点卡片；设置页补上池冷却（瞬时/429）、绑定节点失败即拉黑、探测并发、日志轮转与压缩。
- **手机端适配**：新增 `mobile.css`，导航在窄屏压成一行横向滚动、卡片单列、表格横向滚动、表单与按钮全宽，设置区不再被挤成竖排。

### Added
- `GET /api/probes/sni?target=&sni=`：由面板进程自己打两跳（直连 + 经池口 CONNECT）并给出判定，容器内无需 openssl/curl；用于区分「本地入口被中间层拦截」与「目标不可达」。
- `scripts/sni_diag.sh`：按「本机直连 SNI 矩阵 → 经面板 CONNECT」两段定位目标域名被 RST 的位置，并在部署文档补充判读与规避思路（本仓库不含任何按域名 reject 的规则，可 grep 复核）。
- **自定义站点检测**：`verification.custom_sites`（id/name/url/marker），保存后自动进入「节点可用条件」与「站点检测」页，可在站点检测页增删。
- **订阅源自定义**：`subscription_sources`（每条订阅单独设置客户端 UA / Referer），导入与定时刷新优先使用；仍会保留自动 UA 尝试作为回退。
- **全局探测配置入口**：顶栏新增「探测配置」按钮，任何页面一点即开（目标 + 任一/全部判定）；设置页基础运行区改为摘要 + 编辑按钮，避免重复控件。
- **探测配置在三个测速入口都可编辑**：导入页顶部「探测配置」卡片、批量工具页顶部卡片、设置页「订阅刷新」区块内嵌同一份编辑器（外加基础运行区的字段），保存即生效、无需重启。
- **版本可核对**：`/api/diagnostics` 返回构建 commit，侧栏底部显示 `v<sha>`（Dockerfile 通过 `-ldflags` 注入，workflow 传 `GITHUB_SHA`），用来确认正在跑的是不是新镜像。
- **刷新复用每源成功客户端**：订阅刷新会沿用该源上次成功的 UA/请求头，减少再次触发 UA 限制。

### Fixed
- **测速真正执行多目标/策略**：单节点测速、批量测速、导入验收此前在 `probeBatch` 里直接取单个 `probe_target`，根本不经过策略；现在每次测速都按配置的目标列表执行（`all` 任一失败即判失败，`any` 全部失败才判失败，错误里带具体目标），并且**运行时修改设置立即生效**（不再需要重启）。
- **导入页可直接编辑探测目标**：导入/刷新两个测速入口都内嵌「探测目标 + 判定方式」编辑器，保存即写回设置。
- **节点测速与验收使用同一套多目标/策略**：手动测速、批量测速、导入验收现在都按「设置 → 探测目标 + 判定方式（任一/全部）」执行，不再固定单目标；目标返回 2xx 即算通过（自定义页面也可用），失败会继续尝试下一个目标。
- **探测 URL 归一化修正**：不带 scheme 的目标默认走 **https**（`:80` 例外仍用 http），不再默认 `http://`；也不再给自定义目标强加 `/generate_204` 路径（GitHub 等会 404）。默认目标改为 gstatic/cloudflare/GitHub 三个 https 端点。
- **测速/健康检查不再因目标证书失败**：探测（导入测速、站点检测、健康检查、前置代理测试）现在一律忽略**目标站点**的 TLS 证书，只有节点自身到服务器的 TLS 仍受 `skip_cert_verify` 控制。此前把验收目标换成 GitHub 后，机场/中间设备重签证书会让大批可用节点显示「TLS 证书校验失败」。
- **证书错误给出可操作提示**，并在验收区块说明站点验收容易对受限线路误判。
- **端口占用不再拖垮启动**：`RebuildPortAssignments` 失败或 pool 里存在"还没拿到运行时端口"的节点时，之前会直接退出（容器反复重启，日志 `runtime port missing for pool node`）；现在降级为告警并继续启动，同时新增节点时的端口分配会跳过被其它进程占用的端口。
- **端口冲突**：节点端口分配器（新增节点、重建端口、排序）以前只避开其他节点占用的端口，可能把代理入口、管理 API、粘性入口、Clash 流量端口或 GeoIP 端口分给节点，绑定失败甚至顶掉 WebUI。现在两个分配路径都使用配置层的保留端口集合，并补了回归测试。
- **`config.example.yaml` 顶层 `nodes` 键重复**：插入前置代理段落时误把 `#   min_available_nodes: 1` 截断，并遗留了一个顶层 `nodes: 1`，直接复制示例配置会得到重复键。
- **前置代理无法解码 Base64**：粘贴的 vmess/ss URI 现在会先做归一化——还原 `%2B`/`%2F`/`%3D` 等 URL 编码、去掉换行与零宽字符、剥离客户端追加的 `?remarks=…` 后缀；归一化在校验、保存、拨号三处统一生效，配置里落盘的也是解码后的干净 URI。
- **Every save hung or failed while the process ran**: `AcquireInstanceLock` used `<config>.lock`, the same sidecar the persistence layer flocks per write. The instance lock is held for the whole lifetime, so any settings/node/chain save waited on it until it timed out. The instance lock now uses `<config>.instance.lock`.
- **A runtime that started empty never served imported nodes**: with no pool nodes at boot the core is not started, and the first save/reload returned `manager not started`. It now boots the core from that reload (without blocking on the initial health check) once nodes exist.
- **Saved settings never reached the runtime**: a reload replaced the manager's `*config.Config` instead of updating the shared object, so the WebUI kept editing a detached copy — mode, ports and pool policy changes appeared saved but had no effect, and `node_ports.json` was never written. The manager now adopts configs in place and rollback uses a snapshot.
- Config writes no longer wait forever: the persistence lock now has a 10-second budget for both the in-process mutex and the on-disk lock, so a stuck writer surfaces as a readable error (and a timeout in the WebUI) instead of a request that never answers.
- A read-only `config.yaml.lock` (read-only mount, or a mode set by hand) no longer fails every save: writes fall back to the destination inode lock instead of reporting `open existing persistence lock`.

### Added
- **前置代理（链式路由）**: `chain_profiles` in `config.yaml` describes up to 8 hops; a node references one with `chain_profile_id` and sing-box dials it through the generated detour chain. Exposed in the WebUI (导入节点/设置 页) with `/api/chain-profiles` and `/api/chain-profiles/test`, and every route change re-tests before it is applied. Chained nodes keep a separate port/identity per chain (`NodeKey` includes the profile id), and hop URIs may not point back at this instance's own listener ports.
- **New multi-page WebUI** (dashboard, import, pool, candidates, failed, bulk tools, site connectivity, ports, logs, settings) served from `internal/monitor/assets` and backed by the new endpoints `/api/ui/*`, `/api/import/*`, `/api/connectivity/*`, `/api/ports/status`, `/api/managed-nodes/*`, `/api/nodes/{all,pool,failed,order}`, `/api/job-events` (SSE).
- **Managed node store**: `managed_nodes.json` keeps candidates, the pool and failed nodes with per-route ports; the pool is the single source of truth for sing-box listeners at startup (`internal/importer`, sqlite-backed job/pool state).
- **Backup/restore**: local zip export and WebDAV targets (`/api/backup/*`, `webdav:` config block) with schema versioning and checksums.
- Keep the session-routing feature of this branch: the new UI gains a **会话路由** page driving `/api/proxy-sessions` (random id, HTTP/SOCKS5 link + curl generation, per-session and global reset).

### Changed
- `config.nodes` may now be empty: a fresh install starts with the management UI only and imports its first nodes there.
- The subscription worker delegates each tick to the importer when managed import sources exist, and keeps the classic "load every subscription node" path otherwise; `subscription_refresh.test_204` / `site_targets` control the verification policy.
- `pool.mode: rotate` is accepted as a legacy alias of `sequential`, and `management.pprof_enabled` exposes pprof on loopback listeners only.
- Startup acquires a per-config instance lock and supports `--shutdown-on-stdin-close` for launcher-managed restarts.

## [3.3.0] - 2026-10-25

### Added
- `pool.blacklist_failed_pin` (default `true`, editable in the pool settings panel as「绑定节点拨号失败时立即拉黑」) controls whether a named session's failed pin blacklists its node immediately. Set it to `false` to keep the automatic re-route but fall back to the `pool.failure_threshold` accumulation path.
- Session IDs accept up to 100 characters (`[A-Za-z0-9_-]{1,100}`) instead of 64, in the proxy auth path, the management link API, and the WebUI validation. The listener username plus separator must still fit the SOCKS5 255-byte credential field.

### Changed
- Require Go 1.26.10 and pin the Docker builder to `golang:1.26.10`, closing the reachable stdlib advisory GO-2026-6809 (`crypto/x509` intermediate-certificate distrust, fixed in 1.26.10) that the release gate flagged.

### Fixed
- Named sessions no longer stay pinned to a dead upstream node. When the dial that fails is the one through the bound node, that request now gets one extra selection even if `retry_enabled: false` or the retry budget is already spent, and a successful re-route migrates the binding; if that attempt fails too, the original pin is kept so a single blip does not silently move the session to another exit. This removes the case where one session ID kept answering `502` while every other session kept working, e.g. `wang.abc_1` failing while `wang.abc-1` succeeded — which is never caused by the character itself, since `_` and `-` are both ordinary session-ID characters.
- A named session's pinned node is blacklisted immediately when its own dial fails on a transport fault, instead of waiting for `pool.failure_threshold` to accumulate: the re-routed request no longer leaves a dead node in rotation for every other session. 429 and timeout/connection-reset/503 failures keep their configured cooldowns, and UDP packet failures through a TCP pin never blacklist the node. `POST /api/nodes/{tag}/release` or the WebUI still restores it immediately.
- Reading a session binding no longer renews its idle lifetime. Only a successful dial refreshes `sticky.session_ttl`, so a session whose every attempt fails can expire instead of being kept alive by failed connections, and the LRU order stays aligned with binding expiry.

## [3.2.0] - 2026-10-12

### Added
- **Named sessions on the main HTTP/SOCKS5 pool entry**: authenticate as `username.sessionID` to reuse a TCP upstream node across connections; the bare username retains normal pool scheduling. Session IDs accept 1–64 ASCII letters, digits, underscores, or hyphens. Available in `pool`/`hybrid` mode with a configured listener username; password verification uses the existing listener credentials.
- **Bounded session lifecycle**: `sticky.session_enabled` defaults to true, `sticky.session_ttl` defaults to 24 hours of connection-dial inactivity, and `sticky.max_sessions` defaults to 10,000 bindings with LRU eviction. Manager-owned bindings match stable URI identities across subscription refreshes/core reloads, but are cleared on process restart.
- **Proxy session management API**: list and reset named bindings with `/api/proxy-sessions`, reset an individual full-username binding with `/api/proxy-sessions/{id}`, and generate HTTP/SOCKS5 links with `/api/proxy-sessions/links` without dialing or creating a binding.
- **Session routing UI**: add dashboard connection shortcuts and a dedicated session page with random IDs, masked URL previews, HTTP/SOCKS5 URL/curl copying, binding search, and single/all-session reset controls; expose named-session settings alongside the compatible source-IP entry.
- **Quality gates**: reusable CI checks for formatting, vet, unit/race tests, production feature tags, UI/entrypoint fixtures, reachable vulnerability scanning, and Linux amd64/arm64 builds before Docker publication.
- **Project review report**: document completed fixes, measured performance, upgrade behavior, validation limits, and the next iteration roadmap in `docs/project-review-2026-10-11.md`.

### Changed
- Keep the existing `sticky.enabled`/`sticky.port` source-IP entry compatible and independent of main-port named sessions. Fixed sessions bind upstream nodes, not guaranteed public exit IPs; failover follows pool availability/retry policy without migrating established connections or replaying application requests. SOCKS5 UDP fallback is request-local and does not replace the TCP binding.
- Document main-port session usage, lifecycle limits, compatibility, and management APIs; clarify the default Docker configuration path and replacing placeholder nodes before use. Add the Chinese session routing guide and update the example's supported protocol list.
- Refresh the native HTML management layout with mobile navigation, node search/status filters, labelled controls and keyboard focus support, reduced-motion support, and chart-library offline degradation, while retaining light/dark/system themes without new frontend dependencies.
- Require Go 1.26.9 and update the Docker builder and compatible networking/cryptography/gRPC dependencies; keep sing-box and its coupled gVisor/uTLS versions unchanged.
- Render node configuration/diagnostics views in on-demand 300-row batches, cap probe timelines, and suspend dashboard streams/log polling in hidden tabs. Add cancellable requests, bounded timeouts, and visible errors for busy probe streams.
- Use identity-indexed named-session selection and a fixed-capacity, allocation-free log write ring. Read subscription status through a lightweight snapshot instead of copying every node.
- Apply management password changes immediately, revoke existing login sessions, and expose authentication status/logout through `GET`/`DELETE /api/auth`.

### Fixed
- Canonicalize VMess node identities: URL-format links ignore query-parameter ordering; encoded JSON links ignore the display-name `ps` field, top-level JSON key ordering, and supported base64 encoding variants. Normalize legacy `node_ports.json` keys during migration to preserve existing ports rather than reassigning nodes on upgrade; equivalent identities also keep named-session bindings across reloads.
- Stage only management-owned settings and persist them with one `SaveManagementSettings` operation, retaining `sticky` settings and on-disk node/subscription configuration rather than overwriting it from a stale full-config snapshot. Coordinate settings publication with node CRUD through the manager's optional settings lock.
- Only configure the sing-box Clash API traffic service when `with_clash_api` is compiled in and management is enabled. Default source builds no longer fail startup by requesting an unavailable optional feature; Docker feature-tagged builds retain traffic statistics.
- Do not count caller cancellation during a pool dial as a node failure or unnecessarily switch its session binding.
- Mask proxy passwords in startup entry-point logs, and correctly encode URL credentials and IPv6 hosts. Full credentialed URLs remain available through the management copy/generation flow, using the existing management authentication settings.
- Apply valid host selection and URL encoding to existing proxy exports as well as generated session links: do not export wildcard listen addresses, bracket IPv6 correctly, and percent-encode special characters in credentials. Preserve the existing healthy-node/default, `all=true`, pool, and GeoIP export behavior.
- Preserve management login cookies when a reverse proxy forwards non-Bearer Authorization headers; explicit Bearer credentials retain precedence, including invalid or malformed tokens.
- Derive displayed node health from backend availability/initial-check/blacklist state instead of cumulative failure counts; report non-successful API responses, including failed node releases, rather than displaying false success. Fix the timestamp formatter's undefined-variable reference.
- Disable settings saves after a settings-load error to prevent blank form values from overwriting configuration; clear generated credentialed links on management authentication expiry and safely escape special characters in full session usernames used by display/reset controls.
- Release the manager lock before HTTP shutdown so settings handlers do not deadlock service termination; bound shutdown without an explicit deadline to a 5-second drain and force-close unfinished streams afterward.
- Serialize configuration read-modify-write transactions across writers, write atomically in ordinary directories, and retain locked in-place compatibility for file bind mounts. Preserve file modes and independent node, management, and subscription fields during concurrent saves.
- Serialize runtime lifecycle operations; propagate startup failures/cancellation, prevent reload after close, return independent configuration snapshots, and roll back runtime/GeoIP state when reload or subscription cache persistence fails.
- Keep manual subscription refresh available when periodic refresh is disabled; coalesce concurrent refreshes, cancel/join the worker on stop, preserve inline nodes/settings, and retain the previous subscription set if any provider fails or returns empty content. Runtime URL removal and timer disabling now supersede stale worker settings.
- Preserve active manual node bans across successful probes and all-members-banned fallback; validate health probes against an actual successful HTTP response and fairly rotate equal-load candidates. Closing an old pool no longer unregisters its replacement.
- Preserve GeoIP CONNECT pre-read data, close both tunnel ends on cancellation/EOF, propagate listener failures, strip all hop-by-hop header values, and stream responses. Bound/validate MMDB updates, cancel them on close, and add bounded TTL/singleflight DNS caching.
- Make traffic SSE parsing frame-complete, bounded, and cancellation-aware; eliminate background goroutines created by unused management servers, use bounded probe worker pools, and prevent stale probe results from overwriting newer health state.
- Validate settings without network side effects; reject malformed/multiple JSON objects and invalid values without publishing them. Preserve disabled GeoIP `0s` compatibility without silently enabling automatic updates. Protect management writes with same-origin checks and HttpOnly/SameSite cookies.
- Preserve URL/base64 subscription variants, literal `+` in names, IPv6 endpoints, special credentials, VMess cipher/alterId, and optional SIP002 root paths. Reject invalid upstream ports, use 443 by default for HTTPS proxies, and omit complete credentialed URIs from URL parse errors.
- Avoid reserved listener ports and integer wraparound during node-port allocation; repair duplicated saved assignments and reject explicit port conflicts consistently regardless of node order, retaining existing stable ports. Honor YAML inline manual ports at startup, including intentional changes to their own stale saved assignments.
- Fail fast on invalid entrypoint configuration paths and keep real configuration, node lists, databases, logs, and local build artifacts out of the Docker context.

## [3.1.2] - 2026-10-08

### Fixed
- Preserve `grpc-opts.grpc-service-name` when converting Clash VMess and Trojan nodes to URIs, so their gRPC transports use the configured service name instead of an empty one (#37).

## [3.1.1] - 2026-09-19

### Fixed
- **Failure cooldowns were force-released almost immediately, so `rate_limit_cooldown` barely took effect**: the cooldown added in 3.1.0 is stored in the same state as a threshold blacklist, and two pre-existing paths cleared that state without distinguishing the two.
  - `releaseIfAllBlacklistedLocked` is a last-resort "every member is banned, let them all back in" escape hatch on the request path. In `multi-port`/`hybrid` mode the builder gives each node its own **one-member** pool, so "every member is banned" became trivially true the instant that single member cooled down — the node was released on the very next request and the effective cooldown was ~0
  - A successful health probe called `forceRelease()`. The sweep runs every 5 minutes over all nodes and probes a different host than the failing request used, so a node whose upstream quota was exhausted still passed and was released — capping any cooldown at ~5 minutes
  - Live cooldowns now survive both automatic paths. Permanent blacklists keep their original auto-clear behaviour (the #8/#9 fix is preserved), and manual release from the WebUI or `POST /api/nodes/{tag}/release` still clears everything immediately
  - **Behaviour change**: when every member of a pool is cooling down at once, requests now fail until the earliest cooldown expires instead of being handed back to a node known to be rate-limited. The 60s default keeps this brief; size `rate_limit_cooldown` against your pool accordingly
- **`"429"` matched inside unrelated numbers**: classification used a bare substring test, so `dial tcp 203.0.113.5:44290: i/o timeout` was treated as rate limiting — applying the long rate-limit cooldown instead of the 60s transient one, and, because cooldowns never increment the failure counter, permanently exempting such a node from the blacklist. Status codes are now matched with digit boundaries (`429` and `503` alike), so ports and dotted quads no longer collide
- Blacklist log lines printed the configured `blacklist_duration` even when an already-running longer ban won, contradicting the expiry timestamp on the same line; they now print the real remaining span

## [3.1.0] - 2026-09-19

### Added
- **Stable per-node ports**: in `multi-port`/`hybrid` mode, each node keeps the same local port across subscription refreshes and process restarts
  - Ports are preserved by a stable node identity derived from the URI (ignoring the display name and query-parameter order), so renamed or reordered subscription nodes keep their port
  - Assignments are persisted to `node_ports.json` next to `config.yaml` and restored on startup
- **Shadowsocks-compatible link format**: support for additional Shadowsocks URI variants (#28)
- **WebUI: export all nodes**: new "全部导出" button and `GET /api/export?all=true` that exports every node regardless of health (dead or alive); the default export still returns only healthy/available nodes
- **WebUI: initial health-check progress**: the startup/periodic node sweep now publishes live progress (`done/total`, available, failed) through `GET /api/nodes` and drives the shared progress bar, so the dashboard shows "初始化探测中 N/M" instead of appearing frozen until the sweep finishes
- **Configurable transient and rate-limit cooldowns**: two new `pool` keys replace the previously hardcoded 60s cooldown — `transient_cooldown` (default `60s`) for timeouts, connection resets and 503, and `rate_limit_cooldown` (falls back to `transient_cooldown` when unset) for HTTP 429 / "too many requests". Both accept Go duration strings (`30s`, `10m`, `2h`), are editable live from the WebUI 设置 → 节点池配置 (Pool) panel via the 「暂时性故障冷却」 and 「429 限流冷却」 fields, and are exposed through `GET`/`PUT /api/settings` as `pool.transient_cooldown` / `pool.rate_limit_cooldown`. Setting `rate_limit_cooldown: 2h` keeps a rate-limited node out of rotation for hours instead of a minute; changing either value requires a config reload (#34)

### Changed
- **Transient failures (429 rate-limit, timeouts, connection resets) no longer trigger the 24h blacklist**: such errors are common for shared/free nodes briefly rate-limited by their CDN and usually clear on their own. They now impose only a configurable cooldown — `pool.transient_cooldown`, default `60s`, with rate-limit (429) failures separately tunable via `pool.rate_limit_cooldown` — and do not count toward the 3-strikes permanent-blacklist threshold; permanent faults (handshake/cert/protocol failures, 404, etc.) keep the original 3 → 24h behavior. This stops a burst of concurrent requests from mass-blacklisting otherwise-healthy nodes for a full day
- Improved configuration persistence diagnostics and error handling
- `entrypoint.sh` now detects the "bind-mount of a non-existent file → Docker creates a directory" foot-gun for `config.yaml`/`nodes.txt` and exits with an actionable fix instead of a vague runtime crash
- Removed `start.sh` and `diagnose.sh` helper scripts; `docker compose up -d` (with a directory mount) is now the documented path. README/docs updated to inline the equivalent checks

### Fixed
- **GeoIP region selection now works through a proxy request header**: the region could previously only be given as a path/authority prefix (`jp/example.com:443`), which most HTTP clients mangle — `net/http` parses a `CONNECT jp/example.com:443` authority into `URL.Host=jp` + `URL.Path=/example.com:443`, so the region was silently dropped. Clients can now send `X-Easyproxy-Region: jp`, which works identically for plain HTTP requests and CONNECT tunnels; the header is stripped before the request is forwarded upstream, an unknown region returns `400` instead of falling back to the default pool, and the original prefix forms still work (#32) — thanks @xingpingcn
- **Error messages now match actual mount configuration**: entrypoint.sh error messages previously hardcoded `./data/` paths, causing confusion when using file-mount mode (`-v ./nodes.txt:/etc/easy_proxies/nodes.txt`). Now displays correct fix instructions for both directory-mount and file-mount configurations
- **Initial/periodic health check hangs, leaving 0 nodes available**: `probeAllNodes` (the startup and periodic sweep) called `probe(ctx)` inline with no hard-timeout guard — a separate path from the batch probe. A protocol dial that ignores `ctx` blocked the worker forever, wedging its semaphore slot; the 32 slots filled up and the sweep stalled, so nodes stayed `initialCheckDone=false` and the dashboard showed 0 available even though manual/batch probes found thousands reachable. Each worker now races the probe against its deadline in a goroutine and always releases its slot within the timeout
- **Batch probe hangs forever (WebUI frozen at "N/M")**: a probe against a node that stalls (accepts TCP but never responds, or a protocol dial/handshake that ignores context cancellation) could block indefinitely, so the stuck goroutines occupied every semaphore slot and `wg.Wait` never returned — freezing the run (e.g. stuck around 1600/8363). Fixed with two layers: (1) `Probe` now runs the check in its own goroutine and races it against the 10s context deadline, so it always returns even if the underlying dial ignores `ctx`; (2) a connection watchdog force-closes the probe connection when the deadline fires, unwinding the stalled goroutine (some sing-box connection types don't honor `SetDeadline`, so the deadline alone was insufficient)
- **WebUI: dashboard blacklist/abnormal count stuck at 0**: `GET /api/nodes` returned only the filtered healthy set, so the frontend never saw blacklisted/unavailable nodes and their count always showed 0. It now returns the full node set, restoring the count and making blacklisted nodes visible in the table with a working "解封" (release) button
- WebUI: long node names and URIs are now truncated so they no longer break the table layout
- Prevent crash from malformed VLESS `packetEncoding` nodes
- Preserve inline nodes when a subscription update occurs

## [3.0.1] - 2026-06-17

### Added
- WebUI: sticky proxy settings are now editable from the dashboard

## [3.0.0] - 2026-06-17

### Added
- **Sticky Proxy**: Optional dedicated entry port (default `listener.port + 1`, e.g. `2324`) that pins each client to a single upstream node by source IP, keeping the egress IP stable instead of rotating per connection
  - Coexists with the regular non-sticky pool entry (`2323`) — choose per port
  - Pin is permanent until the node is blacklisted/removed, then re-selects automatically
  - New `sticky` config section (`enabled`, `port`); listen address and credentials inherited from `listener`
  - Pool/hybrid mode only
- **Log Rotation**: Configurable log file rotation with size limits, backup count, and compression
  - New `log` section in config with `output`, `file`, `max_size`, `max_backups`, `max_age`, `compress` options
  - Uses lumberjack for automatic log rotation
  - Defaults: 50MB max size, 3 backups, 7 days retention
- **WebUI Console**: Real-time log streaming in the dashboard
  - In-memory ring buffer captures last 1000 log lines
  - WebSocket-based live log streaming to browser
  - Console tab in WebUI for instant log viewing
- **AnyTLS Protocol**: Support for AnyTLS outbound protocol
  - Parse `anytls://` URIs from subscriptions
  - Full TLS configuration support
- **TUIC Protocol**: Support for TUIC outbound protocol
  - Parse `tuic://` URIs with UUID and password authentication
  - Congestion control and UDP relay mode configuration
  - Full TLS/ALPN support
- **Clash API Integration**: Embedded Clash API controller
  - Internal controller at `127.0.0.1:9092`
  - Enables Clash-compatible tooling integration

### Changed
- **Subscription Parsing**: Improved Clash YAML format detection
  - User-Agent changed to `clash-verge/v2.2.3` for better compatibility
  - YAML detection sample size increased from 200 to 16384 characters
  - Better support for modern proxy types (AnyTLS, TUIC) in Clash format
- **Docker Entrypoint**: Fixed bind-mount permission issues
  - Uses gosu for privilege dropping
  - Ensures proper file ownership for nodes.txt and logs

### Fixed
- Docker nodes.txt permission denied on bind-mount
- VMess node name extraction from base64 payload
- Cross-platform file locking for Windows support

## [2.0.0] - 2025-01-XX

### Added
- SOCKS5 inbound protocol support via Mixed type
- Cross-platform file locking for Windows support
- GeoIP database auto-download and hot-reload
- Hysteria2 (hy2://) protocol support
- Comprehensive security and performance improvements

### Changed
- Major protocol, performance and UI overhaul
- Improved subscription parsing with better error handling
- Enhanced dashboard with real-time statistics

## [1.1.0] - 2024-12-XX

### Added
- GeoIP region routing and dashboard statistics
- Global skip_cert_verify option
- Node port assignment persistence across reloads
- ARM64 support for Docker image

### Fixed
- Hybrid mode export credentials
- Settings save permission issues
- Health check timing after node registration

## [1.0.0] - 2024-11-XX

### Added
- Initial release
- Pool, multi-port, and hybrid runtime modes
- Support for vmess, vless, trojan, ss, hysteria2, socks5, http protocols
- Subscription support (Base64/plain text/Clash YAML)
- Web dashboard with node management
- Automatic health checks and blacklist recovery
- Configurable DNS resolver
