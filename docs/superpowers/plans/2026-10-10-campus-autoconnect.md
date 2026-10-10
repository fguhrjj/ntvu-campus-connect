# 校园网自动连接小程序 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现 Windows 托盘常驻程序 `CampusConnect.exe`，在 NTVU-R 上用纯 HTTP 完成统一身份认证、运营商登录与在线确认，并安全自动重连。

**Architecture:** Go 单程序按 `netguard`、`portal`、`cas`、`flow`、Windows 平台服务、`app`、`ui` 分层。所有门户/认证流量通过手动校验的请求白名单，`flow` 用有界状态机驱动 CAS adapter → authserver → 运营商登录；Windows-only 层负责 DPAPI、WLAN、电源通知、单实例与托盘界面。

**Tech Stack:** Go 1.27.1；`github.com/tailscale/walk v0.0.0-20260702185836-28b80ea70d3b`、`github.com/tailscale/win v0.0.0-20260619195133-2d76c33a64c1`、`golang.org/x/sys v0.48.0`、`golang.org/x/net v0.61.0`（HTML 解析）；构建工具 `github.com/tc-hib/go-winres v0.3.3`。Windows UI 用 walk；网络与密码加密用 Go 标准库。

**Spec:** `docs/superpowers/specs/2026-10-08-campus-autoconnect-design.md`（修订版，包含已批准的 `cas-sso` adapter → `authserver.ntvu.edu.cn` 流程）。

## Global Constraints

- 目标为 Windows amd64；交叉构建 `GOOS=windows GOARCH=amd64`，GUI 链接参数 `-ldflags "-H=windowsgui"`；不依赖浏览器、Node、.NET 或 PowerShell 运行时。
- 校园 SSID 为 `NTVU-R`；用 WLAN API 识别，不使用 `NetConnectionProfile.Name`；SSID 读取失败时仍尝试 Discover。
- 每次 Connect 重新 Discover，不缓存 `sessionId`；不访问 Discover 返回的 `portal-main` URL。
- 只向 `nap.ntvu.edu.cn`、`authserver.ntvu.edu.cn`、`www.msftconnecttest.com` 发请求；门户与身份认证请求使用 HTTPS，TLS 校验开启、`Proxy=nil`；每请求超时 7 秒，单次 Connect 总超时 60 秒。
- 凭据只 POST 至 `https://authserver.ntvu.edu.cn/authserver/login`；账号密码不提交至 `cas-sso` 原生 UsernamePassword 表单。
- HTTP 自动重定向关闭；每个 Location 逐跳按 spec 主机、scheme、path 与跳数校验。禁止所有 logout/offline 路径及 `updateUserSession`；不实现 `checkOnlineUser()` 注销分支。
- 每次 Connect 最多提交 authserver 登录 1 次、`serviceLogin` 1 次；节点循环 ≤4 轮、认证后 `getCurrentNode` 轮询 ≤10 次、在线确认 ≤15 次，全部受 60 秒总 deadline 限制。
- 认证失败、锁定、风险、验证码、服务未找到或缺配置时暂停自动登录，设置保存后清除暂停；自动重试退避为 1、2、5 分钟，之后保持 5 分钟。
- 配置位于 `%APPDATA%\\CampusConnect\\config.json`，密码用当前用户 DPAPI；日志位于 `%LOCALAPPDATA%\\CampusConnect\\campus.log`，最大 1 MB 并保留 1 份；禁止记录密码、密文、盐、execution、ticket、Cookie、sessionId、userIndex、完整学号。
- 固定直接依赖版本并提交 `go.mod`/`go.sum`；HTML 使用 `golang.org/x/net/html` 解析，不用正则解析表单 DOM。

## Review Focus

- `Location` 指向伪装域名（如 `nap.ntvu.edu.cn.evil.example`）、HTTP 身份认证地址或未允许的 path 时，必须在请求发出前拒绝；归 `netguard` 的 `TestPolicyRejectsHostSuffixSpoof` 与 `TestPolicyRejectsUnlistedPathOrScheme`。
- authserver `checkNeedCaptcha.htl` 返回 `isNeed=true` 或 `isNeedActive=true` 时，必须在 POST 账号密码之前分别停止为 `CaptchaRequired` 或 `NeedsAttention`；归 `cas` 的 `TestLoginStopsBeforePostWhenCaptchaRequired` 与 `TestLoginStopsWhenAccountNeedsActivation`。
- 门户报告在线但外网探测失败时，不得重复提交登录；归 `flow` 的 `TestAlreadyOnlineWithInternetFailureDoesNotAuthenticate`。
- 设置中密码留空表示保留已存 DPAPI 密文；损坏或版本未知的配置不得覆盖成空密码；归 `config` 的 `TestSaveBlankPasswordPreservesCiphertext` 与 `TestLoadRejectsUnsupportedSchema`。
- WLAN API 返回空 SSID/权限错误、睡眠恢复与 WLAN 通知同时到达时，不得误判为非目标网络或并发启动两个 Connect；归 `wlan` 的 `TestDecodeSSIDRejectsEmptyOrInvalidLength` 与 `app` 的 `TestConcurrentTriggersCoalesce`。

---

### Task 1: 模块骨架与 HTTP 请求边界

**Files:**
- Create: `go.mod`, `Makefile`
- Create: `internal/netguard/policy.go`, `internal/netguard/client.go`
- Test: `internal/netguard/policy_test.go`, `internal/netguard/client_test.go`

**Interfaces:**
- Produces: `type Policy interface { Allow(method string, u *url.URL) bool }`
- Produces: `func ProductionPolicy() Policy`
- Produces: `func NewClient(policy Policy, transport http.RoundTripper, timeout time.Duration) *Client`
- Produces: `func (c *Client) Do(req *http.Request) (*http.Response, error)`；每次请求先校验白名单，默认 transport 禁用代理，`CheckRedirect` 返回 `http.ErrUseLastResponse`。

- [ ] **Step 1: 写失败测试**：`TestPolicyRejectsHostSuffixSpoof`（拒绝 `nap.ntvu.edu.cn.evil.example`）；`TestPolicyRejectsUnlistedPathOrScheme`（拒绝注销 path、认证 host 的 HTTP、错误 method）；`TestDoDoesNotFollowRedirect`（服务端返回外域 302 时只收到一次请求且调用方拿到 302）。
- [ ] **Step 2: 运行测试确认失败**：`go test ./internal/netguard -run 'TestPolicy|TestDo' -v`；预期缺少包/API 导致失败。
- [ ] **Step 3: 实现白名单 policy 与不自动跟随 redirect 的客户端**：精确匹配 scheme/host/path/method；connect test 仅允许其 spec 中的 HTTP URL；query/path 参数语义由 endpoint client 另外校验。
- [ ] **Step 4: 运行测试确认通过**：`go test ./internal/netguard -v`；三个安全边界测试 PASS。
- [ ] **Step 5: 提交**：`git add go.mod go.sum Makefile internal/netguard && git commit -m "feat: enforce campus HTTP request allowlist"`。

### Task 2: Portal discovery 与 ePortal API

**Files:**
- Create: `internal/portal/types.go`, `internal/portal/client.go`, `internal/portal/discover.go`, `internal/portal/api.go`
- Test: `internal/portal/discover_test.go`, `internal/portal/api_test.go`

**Interfaces:**
- Produces: `type Session struct { ID, UserIP, UserMAC, SSID, NASIP, CustomPageID string; AdvertisedOnline bool }`
- Produces: `type Node struct { Path string }`, `type Service struct { Label, Value string }`, `type OnlineInfo struct { UserID, Service string }`
- Produces: `func NewClient(httpClient *netguard.Client, baseURL *url.URL) *Client`
- Produces: `func (c *Client) Discover(ctx context.Context) (Session, error)`
- Produces: `func (c *Client) UserOnline(ctx context.Context, sessionID string) (bool, error)`
- Produces: `func (c *Client) OnlineUserInfo(ctx context.Context, sessionID string) (OnlineInfo, bool, error)`
- Produces: `func (c *Client) CurrentNode(ctx context.Context, sessionID, flowKey string) (Node, error)`
- Produces: `func (c *Client) Services(ctx context.Context, sessionID string) ([]Service, error)`
- Produces: `func (c *Client) ServiceLogin(ctx context.Context, sessionID, service string) error`

- [ ] **Step 1: 写失败测试**：`TestDiscoverParsesFreshPortalSession`（只跟随 `/` → `/eportal/redirect.jsp`，解析 `portal-main` Location 的 session/terminal 参数且不访问 portal-main）；`TestDiscoverRejectsUnexpectedRedirectHost`；`TestServiceLoginRequiresAuthResultSuccess`（只有 `code==200 && authResult=="success"` 成功）；`TestOnlineUserInfoReadsPortalOnlineUserInfo`（解析已观察的 `portalOnlineUserInfo.result`、`service` 与 `onlineUser` 判定）。
- [ ] **Step 2: 运行测试确认失败**：`go test ./internal/portal -v`；预期缺包/API。
- [ ] **Step 3: 实现 portal client**：严格按 spec 的 JSON method/path/body；flow key 使用 `portal_auth`；Discover 最多 3 跳并只解析、不 GET portal-main；服务映射使用真实 `key/value`，不猜 value。
- [ ] **Step 4: 运行测试确认通过**：`go test ./internal/portal -v`；上述行为及错误响应断言 PASS。
- [ ] **Step 5: 提交**：`git add internal/portal && git commit -m "feat: implement Ruijie portal client"`。

### Task 3: `cas-sso` adapter 与 authserver 登录

**Files:**
- Create: `internal/cas/authenticator.go`, `internal/cas/parse.go`, `internal/cas/encrypt.go`, `internal/cas/errors.go`
- Test: `internal/cas/parse_test.go`, `internal/cas/encrypt_test.go`, `internal/cas/authenticator_test.go`

**Interfaces:**
- Produces: `type PortalContext struct { SessionID, UserIP, UserMAC, SSID, NASIP, CustomPageID string }`
- Produces: `type Credentials struct { Username, Password string }`
- Produces: `type Result struct { ReturnURL *url.URL }`
- Produces: `type Authenticator struct { /* private HTTP client, URL policy, random source */ }`
- Produces: `func NewAuthenticator(httpClient *netguard.Client) *Authenticator`
- Produces: `func (a *Authenticator) Login(ctx context.Context, p PortalContext, credentials Credentials) (Result, error)`
- Produces errors classifiable as `AuthenticationRejected`, `AccountLocked`, `AccountRisk`, `CaptchaRequired`, `NeedsAttention`, or `PortalChanged`.

- [ ] **Step 1: 写失败测试**：`TestParseLoginFormRequiresFieldsInPwdFromID`（只解析 `#pwdFromId`，校验 execution、lt、submit、cllt、dllt、16 字符 salt）；`TestEncryptPasswordAES128CBC`（固定随机源后解密密文，断言 PKCS#7 可解、前缀 64 字符且后缀原密码）；`TestLoginStopsBeforePostWhenCaptchaRequired`、`TestLoginStopsWhenAccountNeedsActivation`（断言认证 POST 次数为 0）；`TestLoginRejectsForeignCallbackHost`。
- [ ] **Step 2: 运行测试确认失败**：`go test ./internal/cas -v`；预期缺实现或断言失败。
- [ ] **Step 3: 实现 adapter → authserver 流程**：首次 GET `/cas-sso/login` 参数取 portal context；仅接受 `current-login-type=adapter`；GET `/cas-sso/clientredirect?client_name=adapter`，service 使用修订 spec 的认证页 URL；校验 authserver Location；解析 authserver 页面与 captcha precheck；对 `password` 用 AES-CBC/PKCS#7（UTF-8 salt key、16 字符随机 IV、64 字符随机前缀、指定字符集）加密；只 POST 一次；错误页取 `#showErrorTip`；成功 ticket callback 逐跳校验 nap `/cas-sso/`，≤3 跳后返回门户 service URL，不请求 logout 或外站。以 `golang.org/x/net/html` 解析 DOM。
- [ ] **Step 4: 运行测试确认通过**：`go test ./internal/cas -v`；另用固定密文向量验证 AES 结果与 authserver CryptoJS `encrypt.js` 兼容。
- [ ] **Step 5: 提交**：`git add internal/cas && git commit -m "feat: implement delegated authserver login"`。

### Task 4: 有界连接状态机

**Files:**
- Create: `internal/flow/types.go`, `internal/flow/connector.go`, `internal/flow/connector_test.go`

**Interfaces:**
- Consumes: `portal.Client` 的 discovery、在线查询、node、service-selection、service-login 方法；`cas.Authenticator.Login`；配置 credentials/service value。
- Produces: `type Status string`（spec 第 6 节全部状态）；`type Result struct { Status Status; Message string }`；`type Portal interface { Discover(context.Context) (portal.Session,error); UserOnline(context.Context,string)(bool,error); OnlineUserInfo(context.Context,string)(portal.OnlineInfo,bool,error); CurrentNode(context.Context,string,string)(portal.Node,error); Services(context.Context,string)([]portal.Service,error); ServiceLogin(context.Context,string,string) error }`；`type Authenticator interface { Login(context.Context,cas.PortalContext,cas.Credentials)(cas.Result,error) }`；`type Connector struct { Portal Portal; Authenticator Authenticator; Credentials cas.Credentials; ServiceValue string; Probe Probe }`；`func (c *Connector) Connect(ctx context.Context) Result`。

- [ ] **Step 1: 写失败测试**：`TestConnectAlreadyOnlineWithInternetFailureDoesNotAuthenticate`；`TestConnectAuthenticatesThenSelectsConfiguredService`（authenticate → CAS → serviceSelection，CUCC 一次 → finish/online → Connected）；`TestConnectStopsAtUnknownNode`（NeedsAttention 且不调用 serviceLogin）；`TestConnectHonorsNodeAndPollBounds`（最多 4 轮、认证后最多 10 次 node poll、在线确认最多 15 次）；`TestConnectNeverCallsLogoutPath`（测试 transport 记录的所有请求都属于白名单）。
- [ ] **Step 2: 运行测试确认失败**：`go test ./internal/flow -v`；预期缺 API。
- [ ] **Step 3: 实现 `Connector.Connect`**：按 spec 先 fresh Discover 与 userOnline；已在线只做外网探测；offline 根据 currentNode 执行 authenticate 或 serviceSelection；`serviceLogin` 成功严格要求 portal client 返回成功；确认 finish/online；所有轮询受次数与 60 秒 context deadline 限制；不调用任何 logout/updateUserSession。
- [ ] **Step 4: 运行测试确认通过**：`go test ./internal/flow -v`；验证所有成功、暂停与边界状态。
- [ ] **Step 5: 提交**：`git add internal/flow && git commit -m "feat: orchestrate bounded portal connection flow"`。

### Task 5: DPAPI 配置与脱敏日志

**Files:**
- Create: `internal/config/config.go`, `internal/config/protect_windows.go`, `internal/config/protect_other.go`, `internal/config/config_test.go`
- Create: `internal/logx/logger.go`, `internal/logx/logger_test.go`

**Interfaces:**
- Produces: `type Config struct { SchemaVersion int; Username, PasswordProtected, ServiceValue, ServiceLabel, SSID string; Autostart bool }`
- Produces: `type Protector interface { Protect([]byte) (string,error); Unprotect(string) ([]byte,error) }`
- Produces: `func Load(path string, protector Protector) (Config,error)`；`func Save(path string, cfg Config, newPassword string, protector Protector) error`（空 `newPassword` 保留已有密文）
- Produces: `func NewLogger(path string) (*Logger,error)`；`func (l *Logger) Request(method, path string, status int, elapsed time.Duration)`；`func (l *Logger) Event(message string)`；文件超过 1 MiB 时轮转 1 份。

- [ ] **Step 1: 写失败测试**：`TestSaveBlankPasswordPreservesCiphertext`；`TestLoadRejectsUnsupportedSchema`；`TestProtectRoundTrip`（Windows DPAPI 实机测试；可移植测试用 fake protector）；`TestLoggerRedactsSensitiveQueryAndFields`（sessionId/ticket/Cookie/学号完整值不出现在日志）；`TestLoggerRotatesAtOneMiB`。
- [ ] **Step 2: 运行测试确认失败**：`go test ./internal/config ./internal/logx -v`；预期缺实现。
- [ ] **Step 3: 实现配置存取与日志**：配置路径按 spec；Windows protector 调用 `CryptProtectData`/`CryptUnprotectData` 且设置 UI forbidden；其它平台 protector 返回明确 unsupported error；原子写配置；日志接口只接收 path 而不接收完整 URL，学号在记录前仅保留后 4 位。
- [ ] **Step 4: 运行测试确认通过**：`go test ./internal/config ./internal/logx -v`；另以 Windows test binary 执行 DPAPI 往返。
- [ ] **Step 5: 提交**：`git add internal/config internal/logx && git commit -m "feat: protect credentials and redact logs"`。

### Task 6: WLAN 与唤醒事件

**Files:**
- Create: `internal/wlan/wlan_windows.go`, `internal/wlan/wlan_other.go`, `internal/wlan/wlan_test.go`
- Create: `internal/power/power_windows.go`, `internal/power/power_other.go`, `internal/power/power_test.go`

**Interfaces:**
- Produces: `func CurrentSSID() (string,error)`；`func Subscribe(ctx context.Context, onConnected func(ssid string)) (unsubscribe func(), err error)`；`func power.SubscribeResume(ctx context.Context, onResume func()) (unsubscribe func(), err error)`。

- [ ] **Step 1: 写失败测试**：`TestDecodeSSIDRejectsEmptyOrInvalidLength`、`TestDecodeSSIDPreservesSSIDBytes`；`TestResumeCallbackCanUnsubscribe`（API wrapper 驱动 callback，确认取消后不再投递）。
- [ ] **Step 2: 运行测试确认失败**：`go test ./internal/wlan ./internal/power -v`；预期缺实现。
- [ ] **Step 3: 实现 Windows API 包装**：使用 `wlanapi.dll` 的 `WlanOpenHandle`、枚举/查询接口和 `WlanRegisterNotification`；使用 `powrprof.dll` 的 `PowerRegisterSuspendResumeNotification`；每个注册句柄在取消或 context 结束时只释放一次；非 Windows 构建返回 unsupported error。
- [ ] **Step 4: 运行测试确认通过**：`go test ./internal/wlan ./internal/power -v`；`GOOS=windows GOARCH=amd64 go test -c` 成功，Windows 上执行单测 PASS；在当前 Windows WLAN 上实测读取 SSID。
- [ ] **Step 5: 提交**：`git add internal/wlan internal/power && git commit -m "feat: observe WLAN and resume events"`。

### Task 7: 单实例、调度、退避与暂停

**Files:**
- Create: `internal/app/runner.go`, `internal/app/schedule.go`, `internal/app/singleinstance_windows.go`, `internal/app/singleinstance_other.go`, `internal/app/runner_test.go`

**Interfaces:**
- Consumes: `flow.Connector.Connect(context.Context) flow.Result`、`wlan.Subscribe`、`power.SubscribeResume`、配置 store、状态回调。
- Produces: `type Trigger string`（startup/WLAN/resume/timer/manual）；`type Runner struct { /* connector, config, clock, status callback */ }`；`func (r *Runner) Run(ctx context.Context) error`；`func (r *Runner) Trigger(trigger Trigger)`；`func AcquireSingleInstance() (release func(), acquired bool, err error)`。

- [ ] **Step 1: 写失败测试**：`TestConcurrentTriggersCoalesce`（正在 Connect 时多触发只排队一次）；`TestBackoffSequenceIsOneTwoFiveMinutes`；`TestAuthenticationFailurePausesAutomaticTriggers`；`TestManualTriggerBypassesPause`。
- [ ] **Step 2: 运行测试确认失败**：`go test ./internal/app -v`；预期缺实现。
- [ ] **Step 3: 实现 runner**：startup、WLAN connect、resume、60 秒 ticker 与 manual trigger；WLAN/resume 延迟 3 秒；自动失败按 1/2/5 分钟 backoff；成功重置；指定状态暂停自动登录，保存新设置后清除并立即运行；Windows named mutex `Local\\CampusConnect` 保证单实例。
- [ ] **Step 4: 运行测试确认通过**：`go test ./internal/app -v`；用可控 clock 测试退避与暂停，不用真实长时间 sleep。
- [ ] **Step 5: 提交**：`git add internal/app && git commit -m "feat: schedule single-instance reconnects"`。

### Task 8: 托盘菜单与设置窗口

**Files:**
- Create: `internal/ui/presenter.go`, `internal/ui/presenter_test.go`, `internal/ui/tray_windows.go`, `internal/ui/settings_windows.go`

**Interfaces:**
- Consumes: `app.Runner` 的 Trigger/status callback、`config.Config` 与 Save、portal.Services、logger 文件路径。
- Produces: `func Run(ctx context.Context, runner *app.Runner, store Store, loggerPath string) error`；`type Store interface { Load() (config.Config,error); Save(config.Config,string) error; Services(context.Context) ([]portal.Service,error) }`。
- Presenter 将 spec 状态映射为托盘颜色、tooltip 与一次性状态提示；UI 菜单文本按 spec：状态、立即连接、打开认证页面、设置、开机自启、打开日志、退出。

- [ ] **Step 1: 写失败测试**：`TestPresenterMapsEveryStatusToTrayState`（在线绿、暂不可用黄、未连接灰、配置/认证错误红）；`TestPresenterSuppressesDuplicateProblemNotification`。
- [ ] **Step 2: 运行测试确认失败**：`go test ./internal/ui -v`；预期缺实现。
- [ ] **Step 3: 实现 walk UI**：主 goroutine 初始化 walk；托盘菜单动作调用 runner；模态设置窗提供学号、密码（留空不改）、运营商列表、开机自启；校园网可达时从 `serviceSelection` 加载 value/label，否则显示已存值；首次无配置自动打开；控件有可访问名称并可键盘操作。
- [ ] **Step 4: 运行测试确认通过**：`go test ./internal/ui -v` 与 `GOOS=windows GOARCH=amd64 go test -c`；实际启动 GUI 后检查菜单、设置窗与状态变化截图。
- [ ] **Step 5: 提交**：`git add internal/ui && git commit -m "feat: add tray and settings UI"`。

### Task 9: Windows 入口、资源、端到端验证

**Files:**
- Create: `cmd/campusconnect/main_windows.go`, `cmd/campusconnect/main_other.go`
- Create: `resources/winres.json`, `resources/app.ico`, `resources/status-online.ico`, `resources/status-warning.ico`, `resources/status-error.ico`, `resources/status-idle.ico`
- Modify: `Makefile`

**Interfaces:**
- Produces: Windows `main` 组装 config、DPAPI、logx、netguard、portal、cas、flow、WLAN、power、app、ui；`make test`、`make windows`、`make windows-test`。

- [ ] **Step 1: 写集成测试/构建检查**：`TestApplicationStartupWithoutConfigOpensSettings`（无配置时不启动认证）；`TestAppWiringUsesProductionPolicy`（正式入口只构造 production allowlist）；设置资源 manifest 检查 DPI awareness 与 Common Controls v6。
- [ ] **Step 2: 运行测试确认失败**：`go test ./...`；预期入口/资源尚未实现。
- [ ] **Step 3: 实现 Windows main 与资源**：walk `NotifyIcon`、注册 manifest/icon、单实例；`HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Run\\CampusConnect` 自启开关；`-H=windowsgui`；初始化失败写脱敏日志/显示可见错误，不静默退出。生产网络 transport 仅 `Proxy=nil`、TLS verify、request 7 秒 timeout。
- [ ] **Step 4: 全量验证**：`go test ./...`；`GOOS=windows GOARCH=amd64 go vet ./...`；`make windows` 输出 `CampusConnect.exe`；从 WSL 调用 Windows test binary 执行 Windows 单测；启动真实 exe，截图核对托盘与设置窗口；验证当前已在线路径；由用户按 spec 手动下线后运行“立即连接”，确认 authserver → CUCC → online，并按脱敏日志处理第 2.6 节未核实项。
- [ ] **Step 5: 提交**：`git add cmd resources Makefile && git commit -m "feat: package CampusConnect for Windows"`。

## Self-Review

- **Spec coverage:** Tasks 1–4 cover request allowlist, portal endpoints, adapter/authserver protocol, AES-CBC, callback validation, bounded connection flow and status classification. Task 5 covers DPAPI, config locations/schema, redacted 1 MiB rotating log. Task 6 covers WLAN SSID/connect and suspend/resume. Task 7 covers single instance, 60-second patrol, trigger coalescing, backoff and pause. Task 8 covers tray, settings, service picker, autostart and notifications. Task 9 covers manifest/icons, Windows packaging, old-login migration awareness and manual offline end-to-end verification.
- **Step scan:** Every task starts with named failing tests, gives the failure command, names the implementation boundary, gives the passing command, then commits. Each produced API is listed before later tasks consume it.
- **Type consistency:** `flow.Portal` matches the `portal.Client` methods; `flow.Authenticator` consumes `cas.Authenticator.Login`; `app.Runner` calls `flow.Connector.Connect`; UI only consumes runner events and store interfaces.
- **Review focus:** All five items above map to named tests in `netguard`, `cas`, `flow`, `config`, `wlan`, or `app` tasks.
- **Open protocol risk:** Successful credentials and the final `cas-sso` callback have not been exercised; the manual offline flow in Task 9 is required before claiming end-to-end success. Never test with guessed credentials or automatically take the user's connection offline.
- **Proportion:** Nine tasks separate security, protocol, state machine, Windows platform, runtime, UI, and packaging boundaries; no implementation code is embedded in this plan.
