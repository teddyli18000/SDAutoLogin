# C：门户客户端与登录编排审查报告

审查员：C 号（对抗性安全审查）。审查对象：`internal/portal`（门户协议客户端）、`internal/service`（登录编排与状态机）、`internal/logging`、`internal/i18n`、`internal/brand` 及其测试。

> **文件行数与任务书不一致（先说清楚）**：任务书写的是 `service.go` 1202 行、`portal.go` 399 行、`portal_test.go` 534 行。工作区实际为 `service.go` **1319** 行、`portal.go` **434** 行、`portal_test.go` **624** 行、`service_test.go` **1444** 行（`logging/log.go` 193、`brand.go` 163、`i18n.go` 151、`locale_windows.go` 22、`locale_other.go` 21、`state_test.go` 86）。本报告所有行号以**当前工作区实际内容**为准。

---

## 摘要

1. **凭据全程明文过网（S1）**：默认配置的两个门户地址都是 `http://10.9.1.3...`（`config.go:136-137`），登录是 **GET**，`user_password` 就在 query string 里（`portal.go:206-224`）。同网段的在途者 / 伪造门户可直接读到密码，且程序**每 15 秒自动轮询**，攻击者不需要等用户操作。
2. **一轮检查即可收割全部账号（S1）**：门户只要对某条 entry 回 `ret_code=1/7`（"账号或密码错"），编排器就会**在同一个 tick 内立刻**把下一条 entry 的密码提交出去，**两次提交之间没有任何延迟**（`service.go:754-812`）。伪造门户能在几十毫秒内拿到配置里所有账号的明文密码。
3. 登录路径上的两个请求（`online_list`、`login`）**不跟随重定向**（`portal.go:109-116`），这是本项目最重要的一条正确防线，密码不会被 302 带到别的域。但**门户首页那个请求跟随默认重定向策略**（最多 10 跳、可跨主机），被劫持时是一个 SSRF-lite 跳板（S2）。
4. `Config.Validate()` **只在设置页保存路径上被调用**（`settings.go:557`），`LoadConfig` 从不调用（`config.go:165-189`）；而该校验本身只检查 scheme 是 http/https 且 host 非空（`config.go:740-747`）。因此"凭据发往哪个主机"在代码里**没有任何约束**：改 `config.json` 或（拿到设置页 token 后）改设置都能把密码指向任意主机（S2）。
5. 响应解析用**正则**（RE2，线性时间，**无 ReDoS**），`v4ip` 只允许数字与点，因此不存在换行/参数注入；响应体有 **4MB `io.LimitReader`** 上限且 `defer resp.Body.Close()` 齐备，**没有内存 DoS 与连接泄漏**。`online_ip`/`user_account`/`online_time` 却**完全没有字符过滤**并被 `%s` 拼进日志，可伪造日志行（S3）。
6. 脱敏（`MaskSecrets`）**只按参数名 `user_password` 匹配**（`portal.go:101`）：URL、`url.Error`、门户回显的请求行都能盖住，但门户若在 `msg` 里回显裸密码就会漏出（S3）。
7. **没有指数退避，没有 jitter，不理会 `Retry-After`**：瞬时失败每 `CheckIntervalSeconds`（最小 5s，默认 15s）重试一次；永久失败才进 10 分钟冷却（S3）。

**结论一句话**：代码在"输入解析健壮性""响应大小上限""重定向不跟随凭据请求""日志文件权限 0600"上做得比同类工具好；真正的风险不在解析而在**协议本身是明文 HTTP + 没有主机信任边界 + 一轮内无间隔地交出全部密码**。

---

## HTTP 请求规格表

所有请求都由唯一的出口函数 `get()` 发出：`portal.go:323-352`。

- 方法：**永远是 GET**（`portal.go:324` `http.NewRequest(http.MethodGet, rawURL, nil)`）——**本程序没有任何 POST/PUT，也没有请求体**。任务书问的"POST 是否会带着密码被转到别的域"在本代码中不成立，因为不存在 POST；等价的问题是"带密码的 GET 是否会被跟随重定向"，答案是**否**（见下表 redirect 列）。
- 超时：两个 client 都只设 `http.Client.Timeout = cfg.HTTPTimeoutSeconds * time.Second`（`portal.go:105-117`）。该值被 `normalize()` 夹在 **2s 至 900s**（`config.go:296-306`），默认 **6s**（`config.go:140`）。**没有任何 context/`WithTimeout`**；`Timeout` 覆盖连接、TLS、请求头、写入与读取整个生命周期，因此"每个阶段都有超时"成立（整体式），但**没有分阶段超时**。
- 响应体上限：`io.ReadAll(io.LimitReader(resp.Body, 4<<20))`（`portal.go:343`），两个 client 共用。**超限是静默截断，不报错**。
- 关闭：`defer resp.Body.Close()`（`portal.go:341`）；正常路径下 body 被读完到即复用连接；仅当 `ReadAll` 出错时提前返回（返回 `resp.StatusCode, "", err`）。
- `http.Transport`：**没有自定义**。使用 `http.DefaultTransport`（默认 `MaxIdleConnsPerHost=2`、`Proxy=ProxyFromEnvironment`）。
- `tls.Config`：**全仓库不存在**（已 grep `InsecureSkipVerify|tls.Config`，仅命中注释与超时相关行）。若把地址配成 `https`，走的是 Go 默认：证书链 + 主机名校验，**没有 `InsecureSkipVerify`**。
- 请求头：`User-Agent`（伪装 Chrome 124）、`Accept: */*`、`Accept-Language: zh-CN,zh;q=0.9`（`portal.go:330-332`）。**未显式设置 `Accept-Encoding`**，因此 Go 会自动加 `gzip` 并**透明解压**；4MB 限制施加在**解压后**的流上（这是正确的顺序，解压炸弹被限制在 4MB 内存内）。

| # | 函数 / 调用点 | 方法 | URL 构造 | 超时 | 体积上限 | 重定向策略 | 行号 |
|---|---|---|---|---|---|---|---|
| 1 | `Client.QueryStatus` 读门户首页取 `v4ip` | GET | `c.cfg.PortalBaseURL` **原样使用**（无 `url.Parse`、无规范化、无拼接） | `client.Timeout` = 2–900s（默认 6s） | 4 MiB | **`c.follow`：默认策略，最多 10 跳、可跨主机、可降级 http↔https** | `portal.go:126`（构造），`portal.go:108`（client） |
| 2 | `Client.QueryStatus` 问在线状态 | GET | `c.cfg.EportalURL + "?c=Portal&a=online_list&user_account=drcom&user_password=123&wlan_user_mac=000000000000&wlan_user_ip=" + url.QueryEscape(st.ClientIP) + "&curr_user_ip=" + url.QueryEscape(st.ClientIP) + "&jsVersion=" + url.QueryEscape(c.cfg.JsVersion)` | 同上 | 4 MiB | **`c.plain`：不跟随**（`ErrUseLastResponse`） | `portal.go:148-154`（构造），`portal.go:109-116` |
| 3 | `Client.Login` 提交凭据 | GET | `c.cfg.EportalURL + "?" + q.Encode()`，`q` 共 15 个参数：`c/a/callback/login_method=1/user_account/user_password/wlan_user_ip/wlan_user_ipv6/wlan_user_mac/wlan_ac_ip/wlan_ac_name/jsVersion/v` 等 | 同上 | 4 MiB | **`c.plain`：不跟随** | `portal.go:206-224` |
| 4 | `Client.confirmLogin` 登录后回读 | GET | 与 #1 + #2 相同，最多 **3 次** `QueryStatus`（即最多 6 个请求），间隔 `confirmDelay=750ms` | 同上 | 4 MiB | 同 #1/#2 | `portal.go:848-855`、`service.go:817` |
| 5 | `Client.probeOnce`（可选，`CheckInternet=true` 时，默认开） | GET | `c.cfg.ProbeURLs` 中**每个**条目原样使用，默认 3 条：`http://connect.rom.miui.com/generate_204`、`http://www.baidu.com`、`http://www.msftconnecttest.com/connecttest.txt` | 同上 | 4 MiB | **`c.plain`：不跟随** | `portal.go:260-309`、`config.go:155-159` |

注：
- 表格第 2 行里硬编码的 `user_password=123` 是 Dr.COM 门户自身的约定值（门户页脚本就是这么问的），**不是用户凭据**，不构成泄漏；但它确实在代码里看起来像凭据，容易误导后续审查者。
- **HTTP 请求站点（种类）共 4 个**：首页、`online_list`、`login`、probe。**其中使用 HTTPS 的数量：0（默认配置下两个门户地址、三个 probe 地址全部是 `http://`）**。只有用户把地址手工改成 `https://` 才有 1–4 个 HTTPS 请求，且此时走 Go 默认校验（无 `InsecureSkipVerify`）。
- 单次 `tick` 最坏请求数：`QueryStatus`(2) + `Login`(1) + `confirmLogin`×3×`QueryStatus`(6) = **9 个请求**（`CheckInternet` 时 +1–3），全部串行。以默认 6s 超时计最坏 ≈ 54s；若用户把 `httpTimeoutSeconds` 设到上限 900s，则最坏 ≈ 2 小时（见 S3-5）。

---

## 状态机与重试时序

### 状态机（`service.tick()`，`service.go:419-593`）

输入是三值：`st.Reachable`（HTTP 层是否答了）、`st.Known`（`online_list` 的 `result` 字段是否解析出来了）、`st.Online`（`result==1` 或 `=="ok"`，`portal.go:167-175`）。

| # | 条件 | 结果状态 | 是否提交登录 | 行号 |
|---|---|---|---|---|
| 1 | `!st.Reachable`（连不上） | `StateError` | **否** | `service.go:463-471` |
| 2 | `st.Known == false`（答了但读不懂） | `StateNotKnown`（"状态未知"） | **否** | `service.go:473-486` |
| 3 | `st.Online` 且需切换服务类型 | 走 `switchToPreferred` → 再次登录 | **是（在会话在线时登录）** | `service.go:488-495` → `997-1051` |
| 4 | `st.Online` 其余情况 | `StateOnline`（外网探测只改 message） | 否 | `service.go:497-538` |
| 5 | `!st.Online` 且 `AutoLogin=false` | `StateOffline` | **否** | `service.go:540-545` |
| 6 | `!st.Online` 且 `!Configured()` | `StateError`（缺账号/缺密码） | **否** | `service.go:547-561` |
| 7 | `!st.Online`（门户明确说不在线） | 走 `signIn` | **是** | `service.go:563-564` → `731-844` |

登录结果判定（`signIn`）：

| `res.Success` | `confirmLogin().Online` | 结果 | 行号 |
|---|---|---|---|
| true | true | `StateOnline`，写 `lastLogin`/`activeEntry` | `service.go:817-840` |
| true | false（无论 `Known` 与否） | `StateLoggingIn`，**不判失败**，留给下一轮 | `service.go:818-835` |
| false 且 `RetCode ∉ {1,7}`（**含 `RetCode==""` 的网络失败与所有未知码**） | — | `transient()` 为 true → `StateError`，**结束本轮** | `service.go:775-791`、`1108-1119` |
| false 且 `RetCode ∈ {1,7}` | — | 该 entry 进 10 分钟冷却，**同一轮内继续试下一条 entry** | `service.go:794-812` |
| 列表里没有可试的 entry | — | `allRefused` → `StateError` | `service.go:750-752`、`1089-1103` |

关键点：`transient()` 对**任何未知 `ret_code`（包括空串）都返回 true**（`service.go:1108-1119`，`switch` 只列了 "1"、"7"），因此"陌生应答"被当作门户自身故障，不会误伤账号——这是对的；但也意味着**网络失败（`ret_code==""`）与门户故障走同一条"下一轮再试"路径，没有任何退避**。

### 重试时序

| 事件 | 间隔 | 是否退避 | 行号 |
|---|---|---|---|
| 循环启动 | `Start()` 后**立刻**一次 `tick()`（不是等待一个周期） | — | `service.go:401` |
| 正常轮询 | `CheckIntervalSeconds`，**最小 5s**（`normalize` 抬底 `config.go:293-295`），默认 **15s**（`config.go:139`） | 无 | `service.go:399-417` |
| 手动 `Trigger()` / `noteUpdate` | 立即（channel 容量 1，非阻塞） | — | `service.go:389-397`、`722-725` |
| **同一轮内 entry 之间** | **0（仅一个 HTTP RTT，无 sleep）** | — | `service.go:754-812` |
| 永久拒绝（`ret_code` 1/7）后该 entry 冷却 | **10 分钟**（`cooldownPeriod`），`Trigger()` 或改配置会立刻清空 | 固定值 | `service.go:176`、`1123-1131`、`389-392` |
| 切换服务类型被拒后 | **1 小时**（`switchRetryPeriod`） | 固定值 | `service.go:190`、`1021-1023` |
| `confirmLogin` 的 3 次回读 | 750ms（`confirmDelay`，测试可替换） | — | `service.go:181-185`、`848-855` |
| 更新检查 | 6 小时（`updateInterval`） | — | `service.go:599`、`618` |

**最小登录尝试间隔的答案**：跨轮 ≥ `CheckIntervalSeconds`（硬下限 **5 秒**，默认 **15 秒**）；**同一轮内为 0**（不同账号之间）。**没有任何指数退避、没有 jitter、不读 `Retry-After`**——`failStreak` 只提升日志级别到 ERROR（`service.go:1286-1319`）。

### 多账号是否泄漏"哪个账号错了"

程序本身不向第三方泄漏（账号名只进 0600 日志与设置页）；但**门户只要回 `ret_code=1` 就能逐个确认账号是否存在**——这是门户自身协议特性，不属于本程序缺陷。真正的问题见 S1-2：它也能借此**逐个收走全部密码**。

### `--once` / CLI 与托盘模式差异

没有 `--once` 这个开关；等价物是 `status`（只做一次 `QueryStatus`，不登录，`cli.go:704`）与 `login`（`cmdLogin` → `app.TestLogin`，`cli.go:945-958`）。`TestLogin` **尊重"在线就不提交"**（`appstate.go:200-206`），与托盘一致；`run`/`--daemon` 用同一个 `Service` 循环。因此 CLI 与托盘在"何时提交"上没有安全相关差异。**无法验证**：CLI 各子命令对配置的校验路径是否与托盘一致（我只确认了 `Validate()` 的唯一调用点是设置页）。

---

## 发现

### [S1] 登录凭据以明文 HTTP 发送，密码位于 URL query

- 类别：中间人 / 凭据外泄
- 位置：`internal/config/config.go:136-137`（默认地址）、`internal/portal/portal.go:206-224`（提交）、`internal/portal/portal.go:323-352`（出口）
- 证据：
  ```go
  PortalBaseURL:        "http://10.9.1.3/",
  EportalURL:           "http://10.9.1.3:801/eportal/",
  ...
  q.Set("user_password", attempt.Password)
  ...
  code, body, err := c.get(c.plain, c.cfg.EportalURL+"?"+q.Encode())
  ```
- 攻击路径：攻击者与受害者在同一校园网（或控制了交换机/DNS/网关）→ 对 `10.9.1.3:801` 做 ARP 欺骗/DNS 投毒/伪造门户主机 → 程序**每 15 秒**自动 `QueryStatus`（`service.go:401,409`），拿到 `result=0` 即提交 `GET /eportal/?...&user_password=<明文>&...` → 攻击者从 query string 直接读到密码。**无需任何用户交互**。
- 影响：机密性**完全失守**（账号+密码，可能还包括 `user_account` 与 ISP 后缀）；可被持久化（攻击者长期持有密码）。程序不做任何门户身份验证（无 challenge、无 token、不核对 `v4ip` 是否等于本机实际出口地址），所以"哪个主机应答就给谁密码"。
- 修复：最小改法是在 `NewClient`/`QueryStatus` 里检查 `cfg.EportalURL`/`PortalBaseURL` 的 scheme：为 `http` 时至少 **WARN 一次并在设置页显著提示"凭据将明文过网"**；更重要的是**优先使用 `https`**（`ValidHTTPURL` 已允许 https，`tls.Config` 走 Go 默认校验，不需要改协议实现）。彻底修复不可能——Dr.COM eportal 本身只提供 http，属于继承风险，因此必须在文档与设置页面上明确告知，而不是静默使用。
- 置信度：高（代码可完整确认；"校园网内可 ARP 欺骗"是题目威胁模型内的既定前提）。**无法验证**：真实校园门户是否提供 https（无网络访问条件）。

### [S1] 一个检查周期内无间隔提交全部账号，伪造门户可一次性收割整份密码表

- 类别：凭据外泄 / 状态机
- 位置：`internal/service/service.go:742-812`（尤其 `799-812` 的 `continue`）、`internal/config/config.go:376-392`
- 证据：
  ```go
  for i, a := range ready {
      ...
      res := s.client.Login(st, a)
      if !res.Success {
          ...
          s.reject(a, res, now)          // 只冷却这一条
          ...
          snap.Message = ...
          continue                        // 立刻试下一条 entry，无 sleep
      }
  ```
- 攻击路径：伪造门户对 `online_list` 回 `{"result":0}`（离线）→ 程序开始 `signIn`；对第 1 条 entry 的 `login` 回 `{"result":0,"ret_code":"1"}`（"账号或密码错"，`transient()` 判为永久拒绝）→ `s.reject()` 只冷却这一条，`continue` 立刻提交第 2 条；重复直到列表走完。**两次提交之间只有一个 HTTP RTT，没有任何延迟**（`cooldownPeriod` 只影响**后续轮次**）。
- 影响：机密性——配置里 N 个账号的明文密码在**单次检查（毫秒级）**内全部交出，攻击者不需要等待任何时间窗口，也不依赖用户在离线状态。相比 S1-1，这是"一次成功即全部失守"的放大。
- 修复：在 `ready` 循环的每两次 `Login` 之间插入一个固定最小间隔（例如 `time.Sleep` 或 `confirmDelay` 级别的 200–500ms），或把"永久拒绝后继续走列表"改成"本轮到此为止，下一轮再试下一条"（依赖已有的 10 分钟冷却即可实现同样的转向，代价是转向变慢）。前者最小、最直接。
- 置信度：高（循环体与 `continue` 逐字可读，无 sleep）。

### [S2] `Config.Validate()` 不在加载路径上，且校验不限制主机——凭据可以指向任意主机

- 类别：凭据外泄 / 架构
- 位置：`internal/config/config.go:165-189`（`LoadConfig`）、`config.go:307-312`（`normalize` 只补空值）、`config.go:740-747`（`ValidHTTPURL`）、`internal/app/settings.go:557`（唯一调用点）
- 证据：
  ```go
  func ValidHTTPURL(raw string) bool {
      u, err := url.Parse(strings.TrimSpace(raw))
      if err != nil { return false }
      return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
  }
  ```
  `LoadConfig` 里只有 `cfg.normalize()` / `markStoredPasswords()` / `loadPasswords()`，**没有 `Validate()`**；唯一调用 `Validate()` 的地方是设置页保存（`settings.go:557`），而它也只检查 scheme+host 非空。
- 攻击路径：（a）直接编辑 `config.json` 把 `eportalUrl` 改成 `http://attacker.example/` → 重启/`SIGHUP` 重载后（`main.go:336` → `reloadConfig`）下一次检查就把密码发到攻击者主机；（b）拿到设置页 token 后走 `/api/config`，因为校验对主机没有任何约束，同样通过（设置页自身若被暴露到网络，`settings.go:165` 有相应 WARN）。
  前置条件：写 `config.json`（0600，同用户）或持有设置页 token——两者都不算强前置，但也不是未授权远程。
- 影响：机密性（凭据被定向外泄）；完整性（可把程序指向伪造门户，配合 S1-2 一次拿走全部密码）。
- 修复：最小改法是在 `LoadConfig` 末尾调用 `Validate()` 并把错误回给调用方（不能静默改值）；同时在 URL 校验里加一条**主机一致性约束**：`EportalURL` 与 `PortalBaseURL` 必须同 host（或至少限制在私网/链路本地地址段），并把不符合的配置在设置页与日志里明确报出。
- 置信度：高（`Validate` 的调用点已用 grep 穷举）。

### [S2] 门户首页请求跟随默认重定向（最多 10 跳、可跨主机）——SSRF-lite 跳板与 `v4ip` 伪造

- 类别：SSRF / 输入解析
- 位置：`internal/portal/portal.go:108`（`follow` 只设 Timeout）、`portal.go:126`（用它取首页）、`portal.go:136-140`（从响应体取 `v4ip`/`v46ip`）
- 证据：
  ```go
  follow: &http.Client{Timeout: timeout},
  ...
  code, page, err := c.get(c.follow, c.cfg.PortalBaseURL)
  ```
  对比 `plain` client 有 `CheckRedirect: ... return http.ErrUseLastResponse`（`portal.go:113-115`）——首页请求**没有**这条防线。
- 攻击路径：劫持/伪造门户首页（ARP/DNS，同 S1-1）→ 应答 `302 Location: http://192.168.1.1/reboot`（或任何内网地址，最多链 10 跳）→ 程序以本机身份对该内网主机发出 **GET** → 若该内网设备有"GET 即可变状态"的接口（路由器/打印机/摄像头/`169.254.169.254` 元数据端点），即形成 CSRF 式副作用；同时攻击者页面的 `v4ip` 会被采纳（`portal.go:136-139`）并用于后续 `online_list` 请求的 `wlan_user_ip`。跨主机时 Go 会剥掉 `Authorization` 之类的头部，所以这里是**匿名 GET**，且攻击者看不到响应内容。
- 影响：完整性（内网设备的 GET 副作用）、可用性（请求放大）；机密性影响有限（无凭据参与、响应不回传）。因为不带密码，低于 S1。
- 修复：给 `follow` 也加上"只允许同 scheme+host 重定向"的策略（`CheckRedirect` 里比较 `req.URL.Host` 与首个请求的 host，不同即 `ErrUseLastResponse`）。
- 置信度：高（`net/http` 默认最多 10 跳、跨主机跟随是文档行为，代码未覆盖）。**无法验证**：真实内网设备是否确有可被 GET 触发的接口。

### [S3] 日志注入：`online_ip` / `user_account` / `online_time` 未过滤即用 `%s` 拼进日志行

- 类别：日志泄漏 / 输入解析
- 位置：`internal/portal/portal.go:90-94`（正则 `[^"]*`，允许任意字节）、`portal.go:176-184`（取值覆盖）、`internal/service/service.go:512-520`、`837-839`、`1250-1272`（`DescribeSnapshot` 裸拼接）、`service.go:587`、`1244`（记录点）
- 证据：
  ```go
  reOnlineIP    = regexp.MustCompile(`"online_ip"\s*:\s*"([^"]*)"`)
  ...
  if ip := firstMatch(reOnlineIP, body); ip != "" { st.ClientIP = ip }   // 任意字节
  ...
  logging.Log.Info("session is online (account %s, client ip %s)", st.Account, st.ClientIP)
  ...
  if s.ClientIP != "" { out += " (ip " + s.ClientIP + ")" }   // DescribeSnapshot，%s 输出
  ```
  `Message`（来自门户 `msg`）经过了 `Truncate`→`strings.Fields`（换行被吃掉），但 `ClientIP`/`Account`/`OnlineSince` **完全没有经过任何清洗**。
- 攻击路径：伪造门户在 `online_list` 里返回 `"online_ip":"1.2.3.4\n2026-01-01 00:00:00 [INFO] session is online (account admin, client ip 10.0.0.1)"`（或带 `\x1b[2J`/`\x1b[31m` 的 ANSI 序列）→ 该串原样进入 `app.log`，伪造出完整的一行，或在用户 `tail -f`/打开日志时操纵终端。（设置页用 `textContent` 渲染日志尾部，`app.js:1165`，所以**不会升级为 XSS**——这一点与 B 号报告需要一致的假设已核。）
- 影响：完整性（日志可被植入伪行、掩盖攻击痕迹）；机密性无直接影响。日志文件 0600，注入内容不外传。
- 修复：把 `st.ClientIP`/`st.Account`/`st.OnlineSince`/`st.MAC` 在**取值处**（`portal.go:176-184`）统一过一遍清洗：既用于日志也用于快照，最省事的做法是复用 `Truncate()`（已做空白折叠与长度截断），或对 `ClientIP` 加"必须能 `netip.ParseAddr` 成功"的校验（同时解决 S3-8）。
- 置信度：高（拼接与格式化符逐字可读）。

### [S3] 脱敏只按参数名 `user_password` 匹配，门户回显裸密码即漏

- 类别：日志泄漏
- 位置：`internal/portal/portal.go:97-101`（`rePassword`）、`384-386`（`MaskSecrets`）、`421-422`（`Truncate`）、`246-247`（把门户 `msg` 放进 `result.Message`）
- 证据：
  ```go
  rePassword = regexp.MustCompile(`(?i)(user_password["']?\s*[:=]\s*["']?)(?:[^"'&\s<>\\]|\\.)*`)
  ...
  if msg := unescapeJSON(firstMatch(reMsg, body)); msg != "" { result.Message = Truncate(msg) }
  ```
- 攻击路径：门户（或中间人改写的应答）返回 `{"result":0,"ret_code":"","msg":"authentication failed, password p@ss is wrong"}` → `msg` 里没有 `user_password` 这个 token → `MaskSecrets` 不匹配 → 明文密码进入 `login parsed: ... message=%q`（`portal.go:252-253`）与 `service.go:779` 的 WARN，并进入设置页 JSON（`settings.go:640` 的 `result`）。
- 影响：机密性**间接**受损：门户本来就知道密码，所以这不是攻击者的新收获；风险在"日志会被粘贴到 issue / 设置页可能被局域网访问"这条下游路径上（README 与托盘菜单都鼓励用户打开日志）。
- 修复：在 `Login` 里额外做一次**值级**脱敏：`MaskSecrets` 之后再用本次请求已知的 `attempt.Password` 做 `strings.ReplaceAll(out, attempt.Password, "***")`。这能覆盖任何不含参数名的回显。
- 置信度：高（正则语义可确认）。**无法验证**：真实门户是否会回显裸密码。

### [S3] 日志文件与日志尾部读取跟随符号链接（预置 `logs/app.log` 链接可让程序写入/`chmod` 任意用户可写文件）

- 类别：日志泄漏 / 本地攻击者
- 位置：`internal/logging/log.go:101-115`（`OpenFile`+`Chmod`）、`log.go:156-182`（rename 轮转）、`internal/app/settings.go:744-759`（`tailLog` 用 `os.ReadFile`）
- 证据：
  ```go
  f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, logFileMode)
  ...
  _ = os.Chmod(name, logFileMode)
  ```
  没有 `O_NOFOLLOW`；`os.Chmod` 与 `os.ReadFile` 同样跟随符号链接。
- 攻击路径：本机另一用户/进程在日志目录中预置 `app.log -> /home/victim/.bashrc`（或其它受害用户可写文件）→ 程序启动时以 `O_APPEND` 打开该目标并 `chmod 0600`，把日志行追加进去；`tailLog` 之后还能把该文件的尾部读回设置页。轮转时 `os.Rename` 重命名的是**链接本身**（目标文件不受影响），因此最坏路径是"追加 + chmod + 读取"。
  前置条件：能在那台机器的日志目录里创建文件——`logs/` 是程序用 `0700` 创建的（`log.go:64`），所以只有在日志目录所在父目录可被他人写入（便携安装、临时目录、共享机器）时才成立。
- 影响：完整性（污染任意用户可写文件）、机密性（读回任意用户可读文件的尾部到设置页）。
- 修复：`os.OpenFile` 前先 `os.Lstat` 判链接（或 Linux 上加 `syscall.O_NOFOLLOW`），发现符号链接就改用 `app.log.1`/报错；`tailLog` 用 `os.Lstat` 同样判一次。
- 置信度：中（代码行为确凿，构成实际攻击还需"日志目录父目录可写"这个前提）。

### [S3] 无退避、无 jitter、不理会 `Retry-After`

- 类别：状态机 / DoS（对门户）
- 位置：`internal/service/service.go:775-791`（瞬时失败每轮重试）、`399-417`（循环周期）、`1311-1319`（`failStreak` 只改日志级别）、`internal/config/config.go:293-295`（下限 5s）
- 证据：
  ```go
  if transient(res) {
      snap.State = StateError
      ...
      logReconnectFailure(attempt, streak, cfg.CheckIntervalSeconds, offline)
      return snap                      // 本轮结束，下一轮同一间隔再试
  }
  ```
- 攻击路径：门户对登录持续回 `ret_code=3`（"portal busy"）或持续超时 → 程序以**固定 5–15 秒**的节奏无限重试，永不退避；`failStreak` 只让日志从 WARN 升为 ERROR。
- 影响：可用性（对已经吃紧的门户持续加压；对单一账号形成可被风控识别为异常的模式）。不持久。
- 修复：在 `transient` 分支维护一个"连续失败→间隔倍增"的计数器（上限例如 10 分钟，成功即清零），或至少读取门户应答里的等待提示再决定下一轮时间。
- 置信度：高（无任何 backoff 代码路径，已 grep 确认）。

### [S3] 退出/停止路径可被更新下载与单轮多次请求阻塞（最长 5 分钟以上）

- 类别：并发 / 可用性
- 位置：`internal/service/service.go:332-353`（`stopLocked` 里 `s.updateWG.Wait()`）、`627-632`、`705-710`（更新 goroutine）、`internal/update/install.go:22-24,130`（`archiveTimeout = 5 * time.Minute`）、`service.go:848-855`（`confirmLogin` 的 3 次回读）、`portal.go:105-117`（每请求超时）
- 证据：
  ```go
  <-done
  // And the look for a newer release, which is not the loop's to end: it is a
  // download, and one may be running when the program is stopped.
  s.updateWG.Wait()
  ```
- 攻击路径：慢速/半开连接的对端（更新服务器或门户）→ `Stop()`（由保存设置、托盘退出、设置页退出触发）卡在 `Wait()`；若此时一个 release 正在下载，上限是 `archiveTimeout=5min`；单轮 tick 自身最坏 9 个请求 × `HTTPTimeoutSeconds`（用户可把它设到 900s）。
- 影响：可用性（界面表现为"退出无响应"）；用户在等待中强杀进程可能中断 `config.json` 写入（`Save` 非原子：`os.WriteFile` + `Chmod`，`config.go:529-538`）。
- 修复：给更新的 `Check`/`Install` 传入可取消的 context（`stopLocked` 里 `cancel()`），并把 `Wait()` 换成带超时的等待。
- 置信度：高（调用链可读）。**无法验证**：实际阻塞时长（未运行程序）。

### [S3] 使用 `http.DefaultTransport`，环境代理会把明文登录转给代理

- 类别：中间人 / 凭据外泄
- 位置：`internal/portal/portal.go:104-118`（`http.Client` 只设 `Timeout`/`CheckRedirect`，**没有 `Transport`**）
- 证据：
  ```go
  return &Client{
      cfg:    cfg,
      follow: &http.Client{Timeout: timeout},
      plain:  &http.Client{Timeout: timeout, CheckRedirect: ...},
  }
  ```
- 攻击路径：进程环境里存在 `HTTP_PROXY`/`http_proxy`（Windows 上子进程继承用户/服务环境，快捷方式或服务定义都能设）→ 默认 transport 的 `ProxyFromEnvironment` 会把包括 `user_password` 在内的请求发给该代理 → 代理持有明文凭据。
- 影响：机密性（凭据经非预期主机过网，且与日志注入叠加时更难发现）。
- 修复：给两个 client 显式设置 `Transport`，其中 `Proxy: nil`（或仅允许显式配置的代理）；至少把"检测到代理环境变量"写一条 WARN。
- 置信度：中（默认 transport 与 `ProxyFromEnvironment` 属 stdlib 契约，代码侧确凿；**无法验证** Windows 上除环境变量之外的代理来源是否也被 `net/http` 采纳——该结论我未在 Go 源码/运行环境中核实，故不作为证据）。

### [S3] 登录应答不校验 HTTP 状态码（与 `online_list` 的处理不一致）

- 类别：输入解析 / 状态机
- 位置：`internal/portal/portal.go:224-236`（`Login` 里 `code` 只在兜底消息里用到）、对比 `portal.go:160-165`（`QueryStatus` 会拒绝非 200/204）
- 证据：
  ```go
  code, body, err := c.get(c.plain, c.cfg.EportalURL+"?"+q.Encode())
  ...
  result.Raw = Truncate(body)
  res := firstMatch(reResult, body)
  result.Success = res == "1" || strings.EqualFold(res, "ok")
  ```
- 攻击路径：门户/中间人回 `HTTP 500` 或 `302`，但响应体里带 `"result":1` → `LoginResult.Success=true`，随后 `confirmLogin` 再向同一（可伪造的）来源确认 → 界面与日志报告"已登录"（假成功）。`QueryStatus` 对同样的状态码会判 `!Known` 而拒绝，两者标准不一致。
- 影响：完整性（状态误报；也可能把一次真正的失败当作成功，从而不再重试 → 可用性）。
- 修复：在 `Login` 里对齐 `QueryStatus` 的判定：`code != 200 && code != 204` 时直接返回失败（`Message` 带上状态码），不解析 body 里的 `result`。
- 置信度：高（代码路径确定）；实际可利用性依赖门户可被伪造（与 S1 同前提）。

### [S3] `v4ip` / `online_ip` 未校验为合法 IP 地址

- 类别：输入解析
- 位置：`internal/portal/portal.go:86-88`（正则只限数字与点，但允许 `999.999.999.999` 这类越界八位组）、`portal.go:136-140`、`176-178`
- 证据：
  ```go
  reV4 = regexp.MustCompile(`(?i)v4ip\s*=\s*'([0-9]{1,3}(?:\.[0-9]{1,3}){3})'`)
  ...
  if ip := firstMatch(reOnlineIP, body); ip != "" { st.ClientIP = ip }
  ```
- 攻击路径：伪造门户首页返回 `var v4ip = '999.999.999.999';` 或 `online_ip` 填任意字符串（含 CR/LF、ANSI）→ 该值进入后续 `online_list` 的 query（经 `url.QueryEscape`，**不能**注入新参数）、进入日志（见 S3-1）、进入设置页展示。
- 影响：完整性/可用性（把错误地址提交给门户，导致"被接受但仍离线"的循环）；**不构成参数或 URL 注入**（`v4ip` 受正则字符集限制，其余取值经 `url.Values.Encode`/`QueryEscape` 转义——这一点是明确的正面结论）。
- 修复：取值处用 `netip.ParseAddr` 校验，不合法则视为"没有 IP"（`ClientIP=""`），并让后续逻辑走"未知"分支。
- 置信度：高。

### [S3] 测试盲区（TLS、超时、超大/压缩响应、畸形应答、日志注入、login 重定向）

- 类别：测试盲区
- 位置：`internal/portal/portal_test.go:100`、`internal/service/service_test.go:134`、`portal_test.go:213-226`、`service_test.go:258-266`、`service_test.go:870`、`service_test.go:1405`
- 证据（全部 stub 都是明文 HTTP 的 `httptest.NewServer`）：
  ```go
  srv := httptest.NewServer(mux)      // portal_test.go:100 / service_test.go:134
  ...
  func TestQueryStatusDoesNotFollowARedirect(t *testing.T) {   // 只覆盖 online_list 的 302
  ```
- **已覆盖**：在线/离线/`result` 缺失/500、`v4ip` 回退到首页地址、`result:"ok"`、不可达门户、login 被接受/已知 `ret_code`/未知 `ret_code` 用门户 `msg`、参数断言（含 `url.Values` 全集）、零 MAC 回退、请求失败、永不 logout、probe 的 204/拦截/无 probe、`Truncate` 与 `MaskSecrets` 的多组用例、`Raw` 不含密码且被截断、cooldown 过期与走列表、未知 `ret_code` 不误伤账号、`Trigger` 非阻塞、Start/Stop 幂等、Stop 等待在飞检查、Restart 并发、更新检查的开关/间隔/签名/安装失败。
- **未覆盖（逐项核对后仍为空）**：
  1. **TLS 完全没测**：没有 `httptest.NewTLSServer`、没有证书错误用例（`portal_test.go:44,100` 全是 `http`）。
  2. **超时/慢响应没测**：`HTTPTimeoutSeconds: 5`（`portal_test.go:30`、`service_test.go:146`）但没有任何 handler 故意 sleep 超过超时；`http.Client.Timeout` 路径无断言。
  3. **超大响应 / 压缩炸弹没测**：没有 >4MB 的 body，也没有 `Content-Encoding: gzip` 的用例，因此 `io.LimitReader`（`portal.go:343`）与透明解压的组合**一行测试都没有**。
  4. **畸形/敌意应答没测**：没有 `online_ip`/`user_account`/`online_time` 含 `\n`、`\x1b[`、超长、Unicode 越界字符的用例（这正是 S3-1 的成因）；没有重复 JSON 字段、`null`、类型不符、超大数组的用例（解析是正则，本就不该依赖 JSON 语义）；没有 `Content-Type` 错误的用例。
  5. **`login` 的重定向没测**：只测了 `online_list` 的 302（`portal_test.go:213`），没有"门户把带密码的请求 302 到另一主机"的用例——虽然代码上 `plain` 已统一处理，但缺回归保护。
  6. **真实时钟被替换**：`confirmDelay` 被改成 1ms（`service_test.go:258-266`）、`switchRetryPeriod` 改成 0（`service_test.go:458-465`）、更新间隔断言只查上界（`service_test.go:1241`）。因此**"确认回读确实等 750ms×2"没有测试覆盖**；也正因为退避逻辑不存在，**退避无从被测**（S3-6 的修复必须同时补测试）。
  7. **flaky 风险**：`service_test.go:870` 用 `time.Sleep(200 * time.Millisecond)` 作为"Stop 是否等待"的判据（负载高时可能不稳定）；`service_test.go:1405` 用 `time.Sleep(5 * time.Millisecond)` 判断"更新检查不阻塞检查"。
  8. **`logging` 测试未覆盖符号链接**（`log_test.go:53-96` 只验证 0600/0700 与失败轮转）。
- 影响：S1–S3 中多条（S3-1、S3-7、S3-8 与 4MB 上限）没有回归保护，将来重构极易回退。
- 修复：优先补 4 个用例：`httptest.NewTLSServer` + 坏证书、`\n`/ANSI 的 `online_ip` 断言日志单行、>4MB body 断言不 OOM、`login` 的跨主机 302 断言不跟随。
- 置信度：高（测试文件已逐函数清点；`^func` 列表见附录 I）。

### [S4] 可达性探测不影响登录决策（已核实），代价是"门户自报在线"永不重连

- 类别：状态机 / 架构
- 位置：`internal/service/service.go:497-507`（探测只写 `InternetChecked/InternetOK/Message`）、`563-564`（登录分支完全不看探测结果）、`internal/portal/portal.go:260-309`
- 证据：
  ```go
  if cfg.CheckInternet {
      snap.InternetChecked = true
      snap.InternetOK = s.client.ProbeInternet()
  } else {
      snap.InternetOK = true
  }
  snap.State = StateOnline
  ```
  `ProbeInternet` 的返回值在 `tick()` 里只用于 `snap.Message` 与 WARN（`service.go:504-531`），**没有任何分支以它为前提去 `signIn`**。README 的说法属实。
- 影响：可用性——伪造门户（或半坏的真实会话）只要一直回 `result=1`，程序就永远报告 `StateOnline`，即使外网完全不通，也不会尝试重新认证；唯一的痕迹是一行 WARN（且只在状态转换时打一次，`service.go:523-531`）。
- 修复（可选）：把"门户说在线 + 探测连续 N 轮失败"作为一个可配置的触发条件（例如强制重新登录一次），或至少在设置页把该状态标为"未确认"。
- 置信度：高（判定链已逐行核对，无隐藏路径）。

### [S4] `io.LimitReader` 静默截断超大响应（无错误信号）

- 类别：DoS / 输入解析
- 位置：`internal/portal/portal.go:343`
- 证据：`data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))` —— 超过 4MB 时 `ReadAll` 正常返回，`err == nil`，调用方无法知道响应被截断。
- 影响：解析基于**前 4MB**；一枚把关键字段放在 4MB 之后的应答会被当作"没有该字段"（走 `StateNotKnown`，不提交密码——方向安全）。**上限本身是正确的防线，确实存在**，故此项仅为观察。
- 修复：用 `io.LimitReader(r, 4<<20+1)` 后检查长度并在超限时记一条 WARN（当前仅在 HTTP 非 200 时记 body）。
- 置信度：高。

### [S4] 首页没有 `v4ip` 时仍会提交空 `wlan_user_ip` 的登录

- 类别：状态机
- 位置：`internal/portal/portal.go:136-145`（只有一条 WARN）、`portal.go:213`（`q.Set("wlan_user_ip", st.ClientIP)`）
- 证据：
  ```go
  if st.ClientIP == "" {
      logging.Log.Warn("no v4ip found on the portal page, a login request would send an empty wlan_user_ip")
  }
  ```
  注释说"没有它就不知道给门户认证哪个客户端，重连会被拒"，但**没有 return/不做提交**的守门逻辑；`signIn` 仍会被调用。
- 影响：完整性/可用性——把请求指向"门户的默认地址"，或在门户用源地址兜底时产生非预期登录；同时"被接受但仍离线"的判定会反复触发。
- 修复：`st.ClientIP == ""` 时把 `Known` 置为 false（或让 `tick()` 走 `StateNotKnown`），与"读不懂就不提交"的既有设计一致。
- 置信度：高。

### [S4] 环境无 Go 工具链，未能运行任何构建或静态检查

- 类别：测试盲区
- 位置：工作区（`go.mod` 声明 `go 1.26`）
- 证据：本机 Windows 与 WSL 均无 `go` 命令（按任务书说明，未尝试构建）。因此：**未运行** `go build`、`go vet`、`go test`、`go test -race`、`staticcheck`。
- 影响：本报告所有结论来自**逐行阅读**，不含编译器/竞态检测器/测试执行结果。以下事项因此**无法验证**：(a) 是否存在数据竞争（我按 happens-before 手工核对了 `service.go` 的锁与 channel，结论是未见竞态，但没有 `-race` 佐证）；(b) 是否编译通过；(c) 测试是否通过、是否有 flaky。
- 修复：在具备工具链的环境跑一遍 `go test ./internal/portal/... ./internal/service/... ./internal/logging/... -race`。
- 置信度：高（这是环境事实，不是代码判断）。

---

## 并发与资源清单

`internal/service/service.go`（全部 goroutine / channel / 锁 / 定时器）：

| 项 | 类型 | 位置 | 退出路径 / 备注 |
|---|---|---|---|
| `go s.loop(stop, done, interval)` | goroutine | `319` | 由 `stop` channel 关闭 + `defer close(done)`（`400`）退出；**无泄漏** |
| `time.NewTimer(interval)` + `defer timer.Stop()` | 定时器 | `402-403` | 已 `Stop`；用 `Reset`（`410,414`），不用 `Ticker`；模块声明 `go 1.26`，因此不涉及 Go 1.23 之前的 `Reset` 陈旧值问题 |
| `go func(){ s.lookForUpdate(cfg, up) }()` | goroutine | `628-631` | 由 `updateWG.Add/Done` 跟踪（`627,629`），`Install` 最长 5 分钟（`update/install.go:24`）→ `Stop()` 最长等这么久（S3-5）；**无泄漏但可长阻塞** |
| `go func(){ s.lookForUpdate(...) }()`（手动） | goroutine | `706-709` | 同上，无节流（用户点一次起一个） |
| `s.mu` | mutex | `203`（定义） | 保护 `snap/listener/cfg/client/stats/rejected/active*/switch*/update*`；临界区都很短，未发现持锁做 I/O |
| `s.lifeMu` | mutex | `208` | 串行 `Start/Stop/Restart`；`Stop` 在**释放 mu 之后**等 `<-done`（`341-345`），注释与实现一致，未发现死锁 |
| `stopCh` / `done` | channel | `215,218`（定义）、`312-315`（创建）、`339`（close）、`345`（等待） | 每次 `startLocked` 新建一对，因此重复 Start/Stop 不会二次 close |
| `triggerCh` | buffered channel（cap 1） | `267`、`393-396`、`722-725` | 两处发送都带 `default:`，**永不阻塞**；无接收方时也不卡 |
| `s.updateWG` | WaitGroup | `260`、`348`、`627`、`705` | 只在 `stopLocked` 里 `Wait`；若在 `Stop` 之后再有 `CheckForUpdateNow`，会重新 `Add`（轻微 TOCTOU，非安全问题） |
| `time.Sleep(confirmDelay)` | 阻塞 | `851` | 在 loop goroutine 内，最长 1.5s；`Stop` 需等它结束 |
| `snapshot → listener` | 回调 | `1238-1247` | 在 **loop goroutine** 上同步调用；`publish` 在 `signIn` 的循环里每次尝试都调一次（`757`）。当前两个实现都只写日志/更新托盘（`main.go:316-323`），因此不阻塞；若将来 listener 变阻塞，会**直接拖住检查循环** |

`internal/logging/log.go`：`Logger.mu`（`31`）保护 `dir/file/size/rotateDone`；`verbose` 是 `atomic.Bool`（`38,93,96`）——正确。`write` 持锁做 `WriteString`（`142`），`rotateLocked` 在持锁状态下 `Close/Rename/Open`（`156-182`），会阻塞并发日志调用，但没有锁顺序问题。无 goroutine。

**`s.cfg`/`s.client` 无锁读取的核对**：`tick()` 在 `421` 无锁读 `s.cfg`，`Restart` 在 `375-376` 持锁写 `s.cfg/s.client`。两者之间有 happens-before：`Restart` 先 `stopLocked()` → `close(stopCh)` + `<-done`，而 loop 的读取在同 goroutine 中先于 `close(done)`（`400`）。因此**不是竞态**（`s.cfg` 是"每次 Restart 换指针"的不可变快照约定，`Clone()` 支撑这一点，`config.go:274-282`）。

---

## 架构与设计层面问题

1. **信任边界缺失是整个审查的中心议题**：程序把"配置里那个 URL 的应答"完全等同于门户本身。没有门户身份验证、没有 `v4ip` 与本机实际地址的比对、没有主机白名单、没有任何"应答看起来像 Dr.COM 门户"的强校验（`looksLikePortal` 只用于 probe 判定，`portal.go:318-321`）。因此"S1 明文 HTTP"和"S2 任意主机"其实是同一个缺口的两种表现：**只要能把流量引到攻击者主机，凭据就走**。
2. **凭据走 URL query 是协议继承风险，但放大了泄漏面**：URL 会进入门户/代理/反向代理的访问日志，也可能进入中间设备的日志。换成 POST body 不能解决明文 HTTP，只减少留痕面；在协议不可改的前提下值得在文档中明确写出。
3. **"未知就不提交"的设计是好设计**（`StateNotKnown`），但它的边界只覆盖"读不懂应答"，不覆盖"读得懂但来源不可信"，也不覆盖"首页没取到 IP 却仍提交"（S4-3）。
4. **`status`/`login`/托盘/设置页四条入口共享同一套 `portal.Client` 与 `config` 语义**（`appstate.go:200-213`、`service.go:731`），这是好设计，但也意味着 S1/S2 的修复必须在**一处**（`portal.NewClient` 附近）落地，不要分散到多个调用点。
5. **日志设计整体是克制的**：默认 `VerboseLog=true`（`config.go:144`）会写每条 URL（已 `MaskSecrets`）与 `online_list` 原始 body 的截断版（`portal.go:191`，仅离线时）。脱敏点集中在 `Truncate`/`safeURL`/`maskedError`（`portal.go:354-386`），可审计性好——这一点值得保留。但 `online_ip`/`user_account`/`online_time` 绕过了 `Truncate`，是唯一的破口（S3-1）。

---

## 覆盖声明

**已逐行读完**（`read` 工具；行号以工作区实际为准）：

- `internal/portal/portal.go`（434 行，全读）
- `internal/service/service.go`（1319 行，全读）
- `internal/logging/log.go`（193 行，全读）
- `internal/i18n/i18n.go`（151 行，全读）、`internal/i18n/locale_windows.go`（22 行，全读）、`internal/i18n/locale_other.go`（21 行，全读）
- `internal/brand/brand.go`（163 行，全读）
- `internal/config/config.go`（774 行，全读——URL 构造与校验面的直接依赖）
- 测试：`internal/portal/portal_test.go`（624 行，全函数清点 + 关键段落细读）、`internal/service/service_test.go`（1444 行，全函数清点 + stub/Stop/时钟替换段落细读）、`internal/logging/log_test.go`、`internal/brand/brand_test.go`、`internal/i18n/state_test.go`、`internal/i18n/i18n_web_test.go`（函数级清点）
- 交叉核对（仅相关片段）：`internal/app/settings.go`（路由/`Validate` 调用/`tailLog`/`testLogin` 应答）、`internal/app/appstate.go`（`TestLogin`/`SwitchService`/listener 装配）、`internal/update/update.go`+`install.go`（超时值）、`internal/webui/assets/app.js`（渲染 sink：`textContent`）、`cmd/SDAutoLogin/main.go`（headless listener 与信号）、`go.mod`。

**未读**：`internal/tray/*`、`internal/platform/*`、`internal/update/*` 的完整实现、`internal/config/passwordfile.go`、`internal/webui/webui.go` 的实现细节、`tools/*`、`vendor/*`（属于其他人/其他号的范围；我只读了与上述结论直接相关的片段）。

**未执行**：任何 `go` 命令（Windows 与 WSL 均无 Go 工具链，按任务书要求未尝试构建）——**因此 `go vet` / `go test` / `go test -race` / `staticcheck` 全部未运行**（见 S4-5）。本次审查**未修改任何源码文件**，只新建了本报告。

**无法验证（明确列出）**：

1. 真实校园门户是否提供 https、真实 `10.9.1.3` 的行为（无网络条件）。
2. 是否存在数据竞争/编译问题（无工具链；只做了 happens-before 手工核对）。
3. `net/http` 在 Windows 上除环境变量之外的代理来源是否生效（S3-7 的置信度因此标为中）。
4. 日志注入对终端/日志收集器的实际后果（未运行程序、未构造真实日志）。
5. 符号链接攻击的实际可利用性（依赖"日志目录父目录可写"这一环境前提，未在目标机器上核实）。
6. 真实门户是否会在 `msg` 里回显裸密码（S3-2 的下游影响因此限定在"日志被粘贴/设置页被访问"）。

---

## 附录：关键代码摘录

A. 两个 client 的构造（`portal.go:104-118`）——注意 `follow` 没有 `CheckRedirect`：

```go
func NewClient(cfg *config.Config) *Client {
	timeout := time.Duration(cfg.HTTPTimeoutSeconds) * time.Second
	return &Client{
		cfg:    cfg,
		follow: &http.Client{Timeout: timeout},
		plain: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}
```

B. 唯一出口，含 4MB 上限与 `defer Close`（`portal.go:323-352` 摘）：

```go
req, err := http.NewRequest(http.MethodGet, rawURL, nil)
...
resp, err := client.Do(req)
...
defer resp.Body.Close()
data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
```

C. 凭据请求（`portal.go:206-224` 摘）：

```go
q.Set("user_account", c.cfg.Account(attempt.Username, attempt.ISP))
q.Set("user_password", attempt.Password)
q.Set("wlan_user_ip", st.ClientIP)
...
code, body, err := c.get(c.plain, c.cfg.EportalURL+"?"+q.Encode())
```

D. 一轮内无间隔试下一条（`service.go:794-812` 摘）：

```go
s.reject(a, res, now)
logging.Log.Warn("reconnect attempt #%d as %s was refused: ...")
...
snap.Message = fmt.Sprintf("%s was refused: %s", a.Entry(), res.Message)
continue
```

E. `transient()` 的判定（`service.go:1108-1119`）：

```go
func transient(res portal.LoginResult) bool {
	switch res.RetCode {
	case "1", "7":
		return false
	}
	return true   // 空 ret_code（网络失败）与一切未知码都算门户自身故障
}
```

F. 脱敏正则（`portal.go:97-101`）：

```go
rePassword = regexp.MustCompile(`(?i)(user_password["']?\s*[:=]\s*["']?)(?:[^"'&\s<>\\]|\\.)*`)
```

G. 日志文件与目录权限（`log.go:21-24`、`101-115` 摘）：

```go
logDirMode  = 0o700
logFileMode = 0o600
...
f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, logFileMode)
...
_ = os.Chmod(name, logFileMode)     // 跟随符号链接
```

H. 全仓库唯一 `Validate()` 调用点（`app/settings.go:557`）与唯一的 URL 校验（`config.go:740-747`）见 S2。

I. 测试函数清单（`internal/portal/portal_test.go`，`^func`）：`testConfig(22)`、`testAttempt(36)`、`closedBase(42)`、`stubPortal.start(67)`、`lastLoginQuery(105)`、`seenActions(111)`、`TestQueryStatusReportsAnOnlineSession(117)`、`...FallsBackToTheAddressOnTheHomePage(142)`、`...ReportsAnOfflineSession(157)`、`...AcceptsAnOKResult(173)`、`...WithoutAResultLeavesTheStateUnknown(181)`、`...WithAServerErrorLeavesTheStateUnknown(200)`、`...DoesNotFollowARedirect(213)`、`...ReportsAnUnreachablePortal(228)`、`TestLoginAccepted(241)`、`TestLoginRejectedWithAKnownRetCode(260)`、`TestLoginUsesThePortalMessageForAnUnknownRetCode(276)`、`TestLoginSendsTheExpectedParameters(288)`、`TestLoginFallsBackToAZeroMAC(322)`、`TestLoginReportsAFailedRequest(334)`、`TestTheClientNeverLogsOut(345)`、`TestProbeInternetAcceptsAnExpectedNoContent(363)`、`TestProbeInternetRejectsAnInterceptedReply(377)`、`TestProbeInternetWithoutProbesIsNotAFailure(393)`、`TestTruncateCollapsesWhitespaceAndCutsLongText(402)`、`TestTruncateCutsOnACharacterBoundary(414)`、`TestLoggedURLsNeverContainThePassword(437)`、`TestLoginTruncatesALongPortalMessage(457)`、`TestMaskSecretsHidesThePasswordInAURL(473)`、`TestMaskSecretsHidesThePasswordInARawReply(490)`、`TestTruncateRemovesThePasswordFromAReply(508)`、`TestTruncateKeepsEverythingButThePassword(538)`、`TestTheKeptReplyHasNoPasswordInIt(554)`、`TestTheKeptReplyIsCutToALine(577)`、`TestALoginThatFailsDoesNotCarryThePassword(592)`。**其中没有一条触碰 TLS、超时、超大响应或日志注入。**

J. `internal/service/service_test.go` 的更新相关函数（1083-1444）用 `stubForge` 替换 forge，因此**门户网络层在 service 层测试中是明文 `httptest`**；`confirmDelay`（258）与 `switchRetryPeriod`（458）被替换成极小值——真实的 750ms/1h 等待从未被测试。
