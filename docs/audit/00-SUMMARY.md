# SDAutoLogin 对抗性审查总报告

- 审查对象：`sdautologin`，HEAD `c5d8d5d`（`main`，`origin/main` 领先 6 个提交），模块 go 1.26
- 审查方式：全量逐文件静态阅读 + 实测验证；6 名子代理按包边界并行，互不重叠
- 威胁模型（用户确认）：**全威胁面，含本机提权与供应链**
- 报告日期：本次会话；各分报告见同目录 A–F 六个文件

---

## 0. 结论摘要

**在这套威胁模型下，程序的安全性取决于一个隐含假设：门户主机与本地 `config.json` 是可信的。** 一旦该假设不成立，凭据即失守；而代码在**信任边界处几乎没有强制手段**（无主机白名单、无 TLS、无签名、无对端身份校验）。

三句话结论：

1. **更新链比其余部分安全得多。** 有一条真实、可验证的信任根（ed25519 公钥钉在二进制里，`update.go:149`、`install.go:152`），顺序正确，不存在「验证失败仍安装」的路径，git 历史中**没有任何私钥材料**。仅控制域名/DNS/自建 Gitea **不足以**替换被安装的字节。
2. **强/弱两条链的落差是本次最重要的结构性发现。** 更新源用钉死的 ed25519 公钥验证，门户与本地设置服务却依赖明文 HTTP + 一次性令牌；同一个程序对「可从网络拿到的字节」用两种完全不同的信任标准。门户被伪造的场景下，程序每 15 秒自动把明文口令交出去。
3. **没有任何 S0。** 未发现「无前置条件即可远程/本地取得凭据或执行代码」的漏洞。7 条 S1 各自需要现实但非平凡的前置条件（同机另一账户、能读日志者、非 root 代码执行、局域网在途者）。

**计分**：S0 = 0 ｜ S1 = 7 ｜ S2 = 18 ｜ S3 = 36 ｜ S4 = 16（合计 77 条，按各分报告独立发现计）。

### 正面结论（同样重要，避免只读缺陷清单）

- `go vet ./...`（linux）、`CGO_ENABLED=0 GOOS=windows go vet ./...`、`gofmt -l cmd internal tools` **全部干净**；`go test -race -count=1 ./...` **13 个包全部通过，无 DATA RACE**。为此前各号只能静态推断的并发正确性提供了实证。
- 凭据处理的正向设计到位：命令行凭据暴露**已在 README 与 `--help` 中明确警告**，并提供 `--password-file` 与 systemd `$CREDENTIALS_DIRECTORY` 两条更安全通道；设置页**只写不读**密码（`pageConfig` 清空所有密码字段）。
- 设置页的鉴权实现本身是正确的：令牌 16 字节 `crypto/rand`、`subtle.ConstantTimeCompare`、写操作强制自定义头（跨源预检不可过）、`SameSite=Strict` + host-only cookie、CSP 无 `unsafe-inline`。
- 前端**无一处可利用的 DOM 注入**：1532 行 `app.js` 逐行读完，3 处 `innerHTML` 全是清空，无 `eval`/`new Function`/`document.write`/内联事件属性；门户响应与日志行全部经 `textContent`。
- 带密码的请求使用**不跟随重定向**的客户端（只有无密码的首页请求跟随重定向）；响应体有 4 MiB `LimitReader` + `defer Close`；正则全为 RE2（**无 ReDoS**）；无内存 DoS、无连接泄漏、无 goroutine 泄漏。
- 发布脚本与维护者脚本**无** `eval`、无 `rm -rf`、无 `curl | sh`、无未加引号变量；CI 中 `uses:` 出现 **0 次**，且 `pull_request` 触发器早于签名密钥引入，fork PR 从来没有读密钥的窗口。
- 部署侧加固扎实：systemd 单元带 `NoNewPrivileges`/`ProtectSystem=strict`/`ProtectHome`/`RestrictAddressFamilies`/`MemoryDenyWriteExecute` 等；Windows 清单为 `asInvoker`（不提权）、只写 HKCU、Run 值带引号（无 unquoted service path 问题）。

---

## 1. 高危问题（S1 × 7）

### 归因：四个根本原因

| # | 根本原因 | 涉及发现 | 任务卡 |
|---|---|---|---|
| a | **门户主机无信任边界** | C-1、C-2、B-S2-2、A-1 | T1、T3 |
| b | **本机单实例与凭据通道可被篡改** | D-1、D-2 | T2 |
| c | **凭据经旁路落盘/回显** | F-01、B-S2-1 | T3 |
| d | **OpenWrt 以 root 执行未校验路径** | D-3 | T4 |

### S1 清单

1. **[C] 登录凭据以明文 HTTP 发送，密码位于 URL query** —— 默认 `http://10.9.1.3:801/eportal/`；全仓无 `tls.Config`、无自定义 Transport、**HTTPS 请求数 = 0**。程序每 15 秒自动轮询，攻击者无需等待用户操作。
2. **[C] 一个检查周期内无间隔提交全部账号** —— `service.go:754-812`：`ret_code=1/7` 只冷却该条目后 `continue`，两次提交之间仅一个网络往返（RTT）。伪造门户可**单轮毫秒级收割整份密码表**。这是第 1 条的放大器。
3. **[D] Unix 单实例锁位于 `/tmp` 固定路径** —— 任意本机用户对 `/tmp/sdautologin.lock` 取得 `flock` 即可让他人的程序**静默拒绝启动**；`/tmp` 为 sticky 位使其无法被清除。跨用户、可持久、无前置条件。
4. **[D] OpenWrt 包装脚本以 root 身份 `exec` `/tmp` 下未校验属主与权限的程序** —— 只检查 `-x`，且 `mkdir -p` 跟随符号链接；任何非 root 账号可预先放置同名可执行文件。
5. **[F] 拼错的凭据选项被逐字回显并写入持久化日志** —— `cli.go:277-278` 回显整段参数，`main.go:57` 先写日志再退出；拼错的 `--pasword=口令` 因此**落盘进 `logs/app.log`**，从「进程列表短暂可见」升级为「持久化 + 走 journald/容器日志」。
6. **[B] 凭据重定向链（跨模块）** —— 凭证持有者可改 `portalBaseUrl` 指向任意主机，密码留空时服务端会**填回已存储的真实密码**（`config.go:606-614`），随后 `testLogin`（`appstate.go`）用改后的地址登录 ⇒ **真实校园网密码被送往攻击者主机**。
7. **[B] entry token 被写进日志并被设置页显示** —— `settings.go:196` 每次 `rollEntry()` 都打印含 `?token=` 的 URL；`startSession` 内部那次 roll 产生的令牌**从未交给任何浏览器却是当前有效值**，即已落入 `logs/app.log`，与代码自身的设计注释直接矛盾。

### 相对关键的一点：3、4、6、7 条最终都归结为同一个根因

`ValidHTTPURL`（`config.go:729-736`）只检查 `scheme ∈ {http,https}` 与 `Host != ""`，**不限制主机**；`Validate()` 的生产代码调用点只有 `settings.go:557`，而 `LoadConfig` 解析后**从不校验**。全仓 `10.9.1.3` 只出现在默认值与测试中，**没有任何主机钉定**。

因此存在**两条独立路径**达成同一失陷：① 持 token 经 API 改地址；② 直接改 `config.json`。这说明该失陷模式**不依赖设置页是否安全**——也解释了为何 B 号在设置页本身给出「无 S0/S1」的正面结论：**弱点不在鉴权，而在信任边界本身**。

---

## 2. 架构与设计评估

### 2.1 分层与依赖方向：干净

`internal/` 依 `logging, i18n, brand, webui, update` → `platform` → `config, portal` → `service` → `app` → `tray` → `cmd` 编号，**无导入环**。`internal/service` 只导入 `config/logging/portal/update`，且 `appstate.go:42-44` 明确写下这一约束（`Updater` 接口由 `app` 注入以避开环）。这是有意的、被记录在注释里的设计，值得肯定。

**两处层次不洁（非缺陷，但削弱了分层意义）：**

- `internal/config` 导入 `internal/update`（`config.go:17`），仅为让 `UpdateChannelValue()` 返回 `update.Channel`。**一个设置包依赖一个网络客户端包**，方向被倒置。
- `internal/tray` 自己构造 `app.NewServer`（`tray_unix.go:87`、`tray_windows.go:141`），**UI 包持有 HTTP 服务器的生命周期**。

### 2.2 进程拓扑：可理解但耦合面大

至少 12 类长生命周期 goroutine/循环（托盘消息循环、检查循环、设置服务、空闲回收、更新检查、第二实例监听、7 个菜单观察者、信号循环、子进程回收器、密码提示回显恢复）。检查循环用 `stop`/`done` 正常退出、`time.NewTimer`（非 Ticker）、通道发送带 `default:`，**未见泄漏或阻塞**——`-race` 亦通过。

**值得注意的耦合**：一次在途的更新下载会**推迟关闭**（`stopLocked` 在 `service.go:348` join `updateWG`）；这意味着退出延迟由网络决定。

### 2.3 两条信任链的强度落差（核心架构问题）

| | 更新链 | 门户 + 本地设置 |
|---|---|---|
| 信任根 | ed25519 公钥钉在二进制（**有**） | 一次性令牌 / 明文 HTTP（**无持久信任根**） |
| 覆盖对象 | 产物内容 + 产物名（含版本号） | 无 |
| 传输保护 | HTTPS + 系统根证书 | **HTTP 明文**，密码在 GET query |
| 主机约束 | 编译期常量 `app.Repo` | **无任何白名单** |

同一个程序对「网络拿到的字节」采用两种标准：对更新产物要求密码学签名，对登录凭据只要求「地址能连通」。**建议把门户地址纳入与更新同级的信任约束**（至少：非 loopback 门户要求显式确认 + 记录主机变更）。

### 2.4 值得肯定的设计决策（避免只列问题）

- 带密码的请求**不跟随重定向**，只有无密码的首页请求跟随 —— 这是本项目最关键的一处正确防线。
- 密码**永不**从设置页回读（只告知「是否已存储」）。
- 权限收紧在启动时执行并写日志说明。
- 更新「无密钥即不安装」被实现为硬失败而非警告。

---

## 3. 安全评估

### 3.1 凭据生命周期：三个落点都需加固

密码存在四处：`config.json`、密码文件、进程参数列表、**HTTP GET query**。设计意图清晰（文档化推荐 `--password-file`/systemd credential），但三个落点各有一条越界路径：

- **落盘**：F-01（拼错选项回显入日志）、B-S2-1（token 入日志）。
- **跨主机发送**：C-1/C-2/B-S2-2/A-1（无主机约束）。
- **环境注入**：F-13 —— 全仓**零** `ProxyFromEnvironment` 处理，故走 Go 默认逻辑；`HTTP_PROXY`/`ALL_PROXY` 一旦被设置，明文口令随 GET URL 经代理发出，**代码对此零感知**。

### 3.2 本地提权与持久化：Windows 侧明显比 Linux 侧干净

Windows：清单 `asInvoker`（永不提权）、只写 HKCU、Run 值带引号 ⇒ **无 unquoted service path、无 UAC 提权面**。

风险集中在 Unix/嵌入式：

- `/tmp` 固定锁路径（D-1）——跨用户静默拒绝服务。
- OpenWrt 以 root `exec` 未校验路径（D-2）——**唯一一条真实提权链**。
- Unix 设置页套接字无 `SO_PEERCRED` 校验、权限由 umask 决定、名字可抢占（D-S2-1）。
- Windows 同会话低完整性进程可预创建命名互斥体/事件（D-S2-2）。

### 3.3 供应链：本次结论最明确的一块

- **信任根存在**（我独立核实：公钥 base64 解码为**恰好 32 字节**；验证顺序正确；无「验证失败仍安装」路径）。
- **无密钥泄漏**：工作树无任何 `*.key/*.pem/*.p12/*.pfx`；125 个提交的**全部 blob** 中 `BEGIN … PRIVATE KEY` **零命中**；`*.key`/`*.pem` 从未被 add；签名密钥只从环境变量读。
- **历史中间态已修复但曾真实存在**：`0a2c6a2` 删除了一段「从工作树读 `signing.key`」的回退，即此前「能读检出目录就能签出所有副本都接受的发布」。README 的声明**现为真**，但由此提交才成立。
- **签名边界**（我以磁盘产物实证并修正 E 号的表述）：`dist/packages/SHA256SUMS` 每行是「摘要 + 文件名」，文件名内嵌版本号，故**版本确实间接落在被签名对象之内**（E 号「不覆盖任何版本元数据」略过宽）。未被覆盖的是 forge 的发布级字段：`Prerelease` 标志、`browser_download_url`、notes。故攻击者可**伪造版本显示、把下载引向任意主机（含明文 http）、拒绝服务**，但**不能替换字节、不能降级、不能改变写入路径**。
- **降级防护是隐含不变量**：靠「安装文件名必须出现在某份已签清单里」+ `NewerThan`。一旦 `ArchiveName` 不再依赖 tag，防护会**无声消失**——建议改为显式检查。
- `dist/packages` 中**没有 `SHA256SUMS.sig`**：当前工作区产物处于未签名状态，印证「无密钥的构建什么都不装」，但也提示发布流程尚未在本地产出过签名。

### 3.4 文档声称与实际实现的一致性核对

我逐条比对了 README「Security notes」的声明：

| README 声明 | 核实结果 |
|---|---|
| 只监听 loopback、每阶段有 deadline、常量时间比较、拒绝响应也带安全头 | **成立**（`withHeaders` 覆盖全部响应含 403） |
| token 换成会话 cookie、地址栏不留 token、资源也在会话之后 | **成立** |
| 一次性地址、15 分钟空闲关闭 | **成立** |
| `config.json` 与日志 0600 / 目录 0700、启动时收紧、Windows 用 ACL | **成立**（但 ACL 失败被 `_ =` 丢弃，见 A-S2-4） |
| 页面只写不读密码 | **成立** |
| 发布须证明由本项目发布、`SHA256SUMS` 经 ed25519、摘要取自该已验清单 | **成立** |
| 私钥在检出目录之外、只从环境变量读 | **成立**（自 `0a2c6a2` 起） |
| 无密钥的构建只记录不安装 | **成立** |
| 「一次性令牌不留痕」的设计意图（代码注释） | **不成立** —— 令牌进日志并被页面显示（B-S2-1） |

**结论：文档与实现总体高度一致，仅「令牌不留痕」一条与代码自身的注释相矛盾。**

---

## 4. 实现与质量：测试覆盖实测

```
go vet ./...                                  → exit 0（干净）
CGO_ENABLED=0 GOOS=windows go vet ./...       → 干净
gofmt -l cmd internal tools                   → 零输出（CI 门禁通过）
go test -race -count=1 ./...                  → 13/13 包通过，无 DATA RACE
```

逐包语句覆盖率（实测）：

| 包 | 覆盖率 | 解读 |
|---|---|---|
| `internal/service` | 93.8% | 核心编排覆盖优秀 |
| `internal/config` | 90.9% | 凭据存储覆盖优秀 |
| `internal/brand` | 95.6% | — |
| `internal/update` | 82.9% | 供应链覆盖良好 |
| `internal/portal` | 80.2% | — |
| `internal/logging` | 79.2% | — |
| `internal/webui` | 75.0% | — |
| `internal/i18n` | 74.4% | — |
| `internal/app` | 72.7% | 设置服务良好 |
| `internal/platform` | **50.7%** | 见下 |
| `cmd/SDAutoLogin` | **38.3%** | 见下 |
| `tools/build` | 36.6% | 构建工具 |
| `internal/tray` | **21.7%** | 见下 |

**零覆盖的函数精确坐实了两条指控**（`go tool cover -func` 实测）：

- `cli.go:270` = **`parseOptions` 零覆盖** ⇒ F-01（未知选项回显）与 F-02（`--save=false`）**从未被任何测试执行**。
- `main.go:22`(`main`)、`main.go:107`(`runGUI`)、`password.go:14`(`promptPassword`)、`password_other.go:16/48`(`echoOff`/恢复) 全部**零覆盖** ⇒ F 号「无一处用真实 `os.Args`、`promptPassword`/`echoOff` 零测试」成立。
- `singleinst_unix.go` 的 11 个函数（`55/79/91/105/118/195/214/226/234/250`）零覆盖 ⇒ D-1（`/tmp` 锁）**无任何测试保护**。
- `internal/tray` 21.7%：`dib.go`、`click.go`、`labels.go`、`icon.go`、`tray_unix.go` 主入口全部零覆盖。

**平台错位（实测）**：

- Windows 代码**确实被类型检查**：`CGO_ENABLED=0 GOOS=windows go vet ./...` 覆盖全部 windows 标签文件（含 `_test.go`）——「Windows 代码从未被编译验证」**不成立**。
- 但 Windows 测试**从不运行**（CI 只 `vet`，不 `GOOS=windows go test`）；执行层零验证。
- **macOS 连编译都没有**——实测 `CGO_ENABLED=0 GOOS=darwin go vet ./internal/tray` 失败于 `vendor/fyne.io/systray` 的 cgo 后端（`undefined: setInternalLoop` 等 5 处），而同一命令对 `./internal/platform` **通过**。缺口在 systray 后端，不在平台层。
- `internal/platform/singleinst_other.go` 带 `//go:build !windows && !linux && !darwin` ⇒ **任何发布目标都不会编译它**（已实测：freebsd 构建时才生效）。

---

## 5. 修复优先级建议

**立即（P0，均为一行到数行改动）**

1. F-01：未知选项不回显原始参数（改为「未知选项」+ 选项名白名单校验）。
2. F-02：`--save=false`/`--install=false` 按布尔值解析，或拒绝带值的布尔标志。
3. B-S2-1：`rollEntry` 不再打印含 token 的 URL（日志只留端口），或仅打印一次且不落盘。
4. 门户地址约束：`ValidHTTPURL` 增加主机白名单/变更确认；把 `Validate()` 放进 `LoadConfig`。

**短期（P1，结构性）**

5. D-1：单实例锁移出共享 `/tmp`（用 `$XDG_RUNTIME_DIR` 或用户私有目录，并拒绝符号链接）。
6. D-2：OpenWrt 包装脚本校验属主与权限，`/tmp` 路径改私有目录，禁止跟随符号链接。
7. C-1/C-2：门户地址非 loopback 时要求显式确认；同一周期内账号之间加入退避与 jitter。
8. F-13：显式 `Proxy: nil` 或使用不接受环境代理的 Transport，或对非 loopback 门户拒绝启动。
9. A-S2-3：`config.json` 改为临时文件 + `rename` 原子写入。
10. A-S2-4：`restrictToOwner` 失败不再丢弃；ACL 在写入前按目录设置。

**中期（P2，测试与流程）**

11. 补 `parseOptions` 与凭据不回显的测试（当前零覆盖，且无任何「口令不出现在输出/日志」断言）。
12. 补单实例、符号链接、写失败、跨源/Host/Content-Type 的对抗性用例。
13. CI 增加 `GOOS=windows go test`（当前只 vet）；评估 darwin 托盘的可编译性。
14. 把 `Tag` 纳入签名对象，并把降级防护从隐含不变量改为显式检查。
15. 评估依赖陈旧度（`golang.org/x/sys v0.15.0` 相对 go 1.26）——本次无 CVE 数据源，未给结论。

---

## 6. 覆盖声明与残余风险

### 已覆盖

- **82 个非测试 Go 源文件全部分派且无重叠**（A 13 / B 8 / C 7 / D 41 / E 21 / F 9，边界在任务书中显式切开）。
- 6 份分报告共 77 条发现，全部带 `file:line` 与逐字证据。
- 我**独立复核了 20 项关键声称**，全部与源码一致（详见各号报告与本节）。
- 运行了权威构建/测试门禁：`go vet`（双平台）、`gofmt`、`go test -race`、`go build -tags notray`、覆盖率与零覆盖函数清单。

### 明确未覆盖 / 无法验证

1. **运行时行为未做动态验证**：无浏览器、未实际运行程序。设置页在真实浏览器中的 cookie 行为、DNS rebinding 下的实际表现、`/static/*` 缓存行为均为静态推理。
2. **Windows 侧零运行验证**：本机无 Windows Go 工具链，`protect_windows.go` 的 ACL 调用、FAT/exFAT/SMB 上的失败、卸载/备份以 SYSTEM 运行的表现只能读码。
3. **macOS 侧零编译验证**（实测 darwin 托盘不可编译，见 §4）。
4. **`winres/rsrc_windows_*.syso` 未解码**：Windows 清单为 `asInvoker` 系 D 号从 `.syso` 部分读取得出，未做完整二进制核对。
5. **依赖 CVE 未评估**：无数据源，`vendor` 一致性只做**结构核对**（一致），未做内容哈希核对。
6. **审查自身的一处方法论错误已更正**：我最初判定「本机无 Go 工具链」（Windows 侧确实没有），据此要求子代理不要构建；A 与 D 实测 WSL 侧存在 go1.26.0 并据实纠正。**这导致 B/C/E/F 的运行时结论停留在静态阅读**。我已在规范检出目录上补做权威门禁（§4）以部分弥补，但**动态/浏览器侧验证仍缺失**。
7. 真实校园门户行为（是否支持 HTTPS、是否按失败次数封禁）无法验证。
8. 本报告的严重度是**对抗性阅读判断**，非 CVSS 评分；S1 与 S2 的边界（是否需要特殊前置条件）存在主观空间。