# F：程序入口与命令行审查报告

审查对象：`cmd/SDAutoLogin/` 全部 9 个文件（`main.go`、`cli.go`、`setup.go`、`password.go`、
`password_windows.go`、`password_other.go`、`console_windows.go`、`console_other.go`、`cli_test.go`）。
方法：静态逐行阅读；所有结论以逐字代码 + 行号为准。**本机无 Go 工具链**，未构建、未运行、
未跑 `go vet`/`go test`，因此涉及运行期行为的结论一律标注置信度与缺口。

行数说明：实际行数与任务书给的数字不一致（文件已增长），以本次实测为准——
`main.go` 357、`cli.go` 1019、`setup.go` 129、`password.go` 35、`password_windows.go` 25、
`password_other.go` 52、`console_windows.go` 46、`console_other.go` 7、`cli_test.go` 1384。

---

## 摘要

入口是一只手写的参数解析器（无 `flag`/`cobra`/`pflag` 依赖，`go.mod` 只有 systray、dbus、
x/sys）。整体结构清晰、选项与命令的归属检查（`options.check`/`checkTrayOptions`）做得比
多数手写解析器严谨，`--save` 复用了 `internal/config` 的权限收紧逻辑，自动启动项只重复
`--home`、明确不带口令，口令从不进 stdout、不进 `cfg.Summary()`。

但入口层有 **1 个 S1、4 个 S2** 的实际缺陷，全部集中在「参数解析器把用户输入当可信文本」
与「退出码/锁覆盖不足」两点上：

- **S1**：拼错的凭据选项（`--pasword=…`、`-password=…`、`--acount=…`）会被
  `unknown option %q` 逐字回显，并被 `main.go:57` 写进**持久化**的 `logs/app.log`。
  口令从「短暂可见的进程列表」升级为「落盘 + 走 syslog/journald/容器日志」。
- **S2**：`--save=false` / `--install=false` 这类显式假值被当作**真**，即用户明确写了
  「不要保存」时恰恰把口令明文写进 `config.json`；`--install=false` 会真的下载并覆盖程序文件。
- **S2**：`login` 的「密码错」与「门户不可达」返回同一个退出码 1，且一次性路径不共用
  service 的 10 分钟冷却，脚本重试循环会把错误口令一次次打到门户（账号封禁风险）。
- **S2**：`run --settings=0.0.0.0:PORT` 时带令牌的地址被写进日志；拿到令牌即等于拿到
  设置页写权限（可改 `portalBaseUrl`），从而把已存口令引到攻击者服务器。
- **S2**：代理环境变量（`HTTP_PROXY`/`HTTPS_PROXY`）未禁用，默认门户是明文 `http://`，
  口令以 GET 参数经代理发出（跨范围根因在 `internal/portal`）。

命令行口令本身**已被文档化**（README.md:280-282、README.zh-CN.md:282-283、`--help` 的
cli.go:99-103），并提供了 `--password-file` 与 systemd `$CREDENTIALS_DIRECTORY` 两条更安全
通道；因此这不是「未文档化的凭据暴露」，而是「已文档化但没有运行时警告」（S3，见 F-12）。

---

## 子命令与标志清单

命令（`cli.go:542-563` 的 `switch`，含 `main.go:68-81` 的快捷词）：

| 命令词 | 语义 | 取单实例锁 |
|---|---|---|
| `run` / `daemon` | 后台常驻，无托盘（`cmdRun`，cli.go:631） | 是（cli.go:641，`false`） |
| （无命令） | 托盘 + 回环设置页（`runGUI`，main.go:107） | 是（main.go:110，`true`=请求已有副本开设置页） |
| `status` | 一次状态检查后退出（cli.go:699） | 否 |
| `update` | 手动检查/安装新版本（cli.go:810） | 否 |
| `login` | 用第一个条目提交一次登录（cli.go:945） | 否 |
| `config` | 交互式向导或 `--show`（cli.go:987） | 否 |
| `version` | 打印版本（cli.go:553） | 否 |
| `help` | 打印用法（cli.go:556） | 否 |

快捷/别名词（`knownWord`，cli.go:214-223）：`-h|--help|-help`、`-v|--version`、
`-test-login|--test-login`（= `login`）、`-cli|--cli|-daemon|--daemon|-headless`（= `run`）、
`-show|--show`（`config` 的开关，在 `cmdConfig` cli.go:992-997 单独匹配，**不进 `options`**）。

选项（`knownOption` cli.go:190-198 与 `options.set` cli.go:226-265，共 13 个）：

| 标志 | 类型 | 默认 | 涉及凭据 |
|---|---|---|---|
| `--home=DIR` | 字符串（`--home DIR` 亦可） | 未给出：exe 目录 → 构建目录 cwd → `os.UserConfigDir()/sdautologin` | 否（决定 `config.json`/`logs/` 落点） |
| `--no-log` | 布尔开关 | false | 否 |
| `--quiet` | 布尔开关 | false | 否 |
| `--account=USER:PASSWORD[@SVC[+SVC]]` | 字符串，**可重复**（追加成列表） | 无 | **是（口令）** |
| `--username=NAME` | 字符串 | 无 | 否（账号名） |
| `--password=SECRET` | 字符串 | 无 | **是（口令）** |
| `--password-file=PATH` | 字符串 | 无 | 是（口令所在文件路径） |
| `--isp=SUFFIX` | 字符串（可空值） | 无 | 否 |
| `--save` | 布尔开关 | false | 间接：把口令写进 `config.json` |
| `--settings=ADDR` | 字符串（仅 `run`/`daemon`） | 无＝不服务设置页 | 否（但令牌随日志输出，见 F-04） |
| `--json` | 布尔开关（仅 `status`/`update`） | false | 否 |
| `--install` | 布尔开关（仅 `update`） | false | 否 |
| `--app[=ADDR]` | 可选字符串（仅托盘模式） | 无＝不预开窗口；`--app=` 空值＝回环随机端口 | 否 |

合计：命令 7 个（`daemon` 为 `run` 别名）＋ 可选标志 13 个 ＋ 其他已识别短横词 14 个（含
`--show`/`-show`）。

---

## 环境变量清单

程序（`cmd/` + `internal/`，不含 `tools/`）读取的环境变量，逐个核对
`os.Getenv|LookupEnv|os.Environ` 与标准库间接读取（`os.UserConfigDir`/`os.UserCacheDir`/
`os.TempDir`/`net/http` 默认 Transport）：

| 变量 | 读取位置 | 用途 | 安全敏感 |
|---|---|---|---|
| `CREDENTIALS_DIRECTORY` | **cli.go:419**（F 范围内） | 取 `$CREDENTIALS_DIRECTORY/password` 作本次运行的口令；`--save` 时该路径被写进配置 | **是**（口令来源，见 F-09） |
| `XDG_CONFIG_HOME`、`HOME` | 经 `os.UserConfigDir()`，**main.go:252** 调用 | 决定 `config.json` 与 `logs/` 的落点（exe 目录不可写/在系统目录时才走到） | **是**（见 F-10） |
| `STATE_DIRECTORY` | `internal/platform/singleinst_unix.go:80`（跨范围） | systemd 服务状态下单实例锁文件所在目录 | 是（锁目录） |
| `TMPDIR` | 经 `os.TempDir()`，`internal/platform/singleinst_unix.go:65`（跨范围） | 无 systemd 状态目录时的锁文件目录（`/tmp/sdautologin.lock`） | 是（锁目录） |
| `XDG_CACHE_HOME` | 经 `os.UserCacheDir()`，`singleinst_unix.go:69`（跨范围） | 锁目录的最后回退 | 是（锁目录） |
| `LC_ALL`、`LC_MESSAGES`、`LANG` | `internal/i18n/locale_other.go:12-13`（跨范围） | 界面/日志语言（`zh*` → 中文） | 否 |
| `BROWSER` | `internal/platform/openurl_other.go:17-18`（跨范围） | **变量值被直接当命令执行**：`cmd = exec.Command(os.Getenv("BROWSER"), target)` | **是（代码执行）** |
| `ProgramFiles`、`ProgramFiles(x86)`、`LOCALAPPDATA` | `internal/platform/appwindow.go:57`（跨范围） | 拼出 Chrome/Edge 的绝对路径后 `exec.Command` | 是（低：可指向攻击者控制的 exe） |
| `HTTP_PROXY`、`HTTPS_PROXY`、`NO_PROXY` ＋小写变体 | 隐式：`internal/portal/portal.go:108-116` 的 `http.Client` 未设 `Transport` → `http.DefaultTransport` → `ProxyFromEnvironment` | 门户/更新请求走代理 | **是**（明文口令随 `http://` GET URL 经代理，见 F-13） |

统计：**19 个**（13 个显式 + 代理 6 个拼写变体），其中**安全敏感 9 类**：
`CREDENTIALS_DIRECTORY`、`XDG_CONFIG_HOME`/`HOME`、`STATE_DIRECTORY`、`TMPDIR`、
`XDG_CACHE_HOME`、`BROWSER`、`LOCALAPPDATA`/`ProgramFiles*`、`HTTP_PROXY` 家族。
其中**只有 2 项由 F 的文件读取**：`CREDENTIALS_DIRECTORY`（cli.go:419）与经
`os.UserConfigDir()` 的 `XDG_CONFIG_HOME`/`HOME`（main.go:252）；其余属其他审查员范围，
此处只为回答「程序读了哪些环境变量」而列出，**不作为 F 的发现编号**。

补充事实：程序中**不存在** `os.Setenv`（全仓库 grep 无匹配），即程序从不把凭据放进自己或
子进程的环境变量；唯一派生的子进程是 `stty`（password_other.go:49），其环境继承父进程，
父进程环境里没有口令。

`ALL_PROXY`：Go 的 `ProxyFromEnvironment` **不识别** `ALL_PROXY`（只认
`HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY` 及其小写形式），因此任务书里担心的 `ALL_PROXY`
在此不生效；`HTTP_PROXY`/`HTTPS_PROXY` 生效。

---
## 退出码与生命周期

`main.go` 完整流程与关键行号：

1. `args := os.Args[1:]`（main.go:23）。
2. `parseOptions(args)`（main.go:27，实现在 cli.go:270-293）——手写解析器。
3. `chooseAppDir(opts.home)`（main.go:28，实现 main.go:189-212）。
4. `platform.SetAutostartArguments(autostartArguments(opts))`（main.go:32）——只记
   `--home=绝对路径`（main.go:299-308），**不带口令**。
5. `logging.Log.Init(<appDir>/logs, !opts.noLog)`（main.go:37）——在配置被读之前，
   因为读配置失败也要有日志；`--no-log` 在此已生效。
6. `if len(args) > 0 { attachConsole() }`（main.go:39-43）——只有带参数才去挂父控制台。
7. 目录解析失败 → `os.Exit(2)`（main.go:44-50）；参数错误 → 记日志 + stderr +
   `printUsage` + `os.Exit(2)`（main.go:53-61）。
8. 分派：`rest[0]` 不以 `-` 开头 → `os.Exit(runCommand(rest, appDir, opts))`（main.go:63-65）；
   否则按 `main.go:68-81` 的 `switch` 匹配快捷词（help/version/`-test-login`/`-cli` 等）。
9. 剩下的选项若属于命令 → `checkTrayOptions` 拒绝 + `os.Exit(2)`（main.go:86-91）；
   `--app` 在无托盘构建里 → 拒绝 + `os.Exit(2)`（main.go:94-99）。
10. `runGUI(appDir, opts)`（main.go:101）→ 结束后 `logging.Log.Info("SDAutoLogin stopped")`。

**托盘模式与「CLI 模式」的分支条件**：没有命令词（或只有选项/快捷词）→ 托盘 `runGUI`；
第一个以非 `-` 开头的词命名命令 → 一次性命令。任务书中的 `--once` **不存在**：该词会落到
`knownOption` 为假、`knownWord` 为假 → `unknown option "--once"` + `exit 2`（cli.go:277-278）。
后台无界面模式的真实入口是 `run`/`daemon`/`-cli`/`--cli`/`-daemon`/`--daemon`/`-headless`
（main.go:78，cli.go:543）。

**单实例锁的获取与释放**：

- 托盘：`platform.AcquireSingleInstance(true)`（main.go:110）。第二个副本拿到 `false` 时会
  去请求已运行副本打开设置页（跨范围 `internal/platform/singleinst_unix.go:152-158`），
  然后 `runGUI` 直接 `return` → 进程以 **0** 结束。
- 后台 `run`：`platform.AcquireSingleInstance(false)`（cli.go:641）——**不**请求设置页，
  失败时打印「another instance is already running」并 `return 1`（cli.go:641-645）。
- **没有显式释放调用**：全仓库无 `Release*`；Unix 侧靠进程退出时 `flock` 随 fd 关闭释放，
  Windows 侧靠进程退出释放命名 mutex。
- **`login`/`status`/`update`/`config` 完全不取锁**（cli.go:699、810、945、987），
  即这些命令可与正在运行的托盘并发执行（见 F-11）。
- 顺序：`cmdRun` 先 `loadForCLI` → `checkAccountForRun` → 才取锁（cli.go:632-645），
  所以「本机已有实例 + 配置里没账号」时报的是「no account configured」而不是「已有实例」。

**退出码清单**（`printUsage` 的文档在 cli.go:56-64）：

| 码 | 语义 | 出处 |
|---|---|---|
| 0 | 成功；`status` 在线；`update` 检查/安装完成；第二个托盘副本请求完设置页；托盘正常退出 | main.go:72/75/101、cli.go:719/858/984/1011 |
| 1 | 一般失败：`status` 不可达或未知；`login` 失败（含密码错）；`config` 未写入；`run` 无账号/设置页端口占用/**已有实例**；`update` 失败；`loadForCLI` 失败 | cli.go:634/639/644/669/702/832/857/948/953/982/990/1009 |
| 2 | 用法错误：未知选项、缺值、选项不属于该命令、未知命令、无可用配置目录 | main.go:49/60/90/98、cli.go:539/562 |
| 3 | `status`：门户明确说本机未登录 | cli.go:783（`statusExitCode`） |

**信号处理**：只有无界面/后台循环 `runHeadless` 装了处理器
（main.go:327-345：`os.Interrupt`、`SIGTERM`、`SIGHUP`；`SIGHUP` = 重载配置），
退出前调用 `state.Stop()`。`status`/`login`/`config`/`update` 这类一次性命令**不装处理器**，
被 `SIGINT`/`SIGTERM` 打断时由 Go 运行时按默认行为结束（Unix 上通常 130/143），退出码未
文档化；`config` 向导在口令提示期间另有 `password_other.go:24-37` 装的临时处理器（只为恢复回显）。
托盘模式的信号处理属 `internal/tray`（未读，不在本报告范围）。

---

## 发现

### [S1] F-01 拼错的凭据选项被逐字回显，并写进持久化的 logs/app.log

- 类别：凭据暴露
- 位置：cli.go:275-279（回显）、main.go:53-61（写日志）、logging 双写（`internal/logging/log.go:26-29`，跨范围引用）
- 证据：
  ```go
  name, value, hasValue := strings.Cut(args[i], "=")
  if !knownOption(name) {
      if strings.HasPrefix(args[i], "-") && !knownWord(args[i]) {
          return opts, nil, fmt.Errorf("unknown option %q", args[i])
  ```
  ```go
  logging.Log.Error("%v", err)          // main.go:57
  fmt.Fprintf(os.Stderr, "%v\n\n", err)  // main.go:58
  ```
- 攻击路径（本机非特权用户 / 操作者误写，二者皆现实）：
  1. 操作者按程序自己文档化的单横线风格书写（`-cli`、`-test-login`、`-show` 都存在）：
     `SDAutoLogin run -password=Hunter2 --account=23D001:Hunter2`，或把 `--password`
     误写成 `--pasword`、把 `--account` 误写成 `--acount`。
  2. 解析器把整段参数当作「未知选项」原样放进错误文本。
  3. 该文本同时进 stderr 与 `logs/app.log`（logging 的 console 副本走 stderr、file 副本走
     `app.log`，`internal/logging/log.go:26-29`）；`app.log` 会轮转到 `app.log.1`。
  4. 结果：明文口令落在磁盘上并在服务场景进入 syslog/journald/容器日志；同机任意能读到
     日志流或日志文件的进程/用户、以及任何拿到日志粘贴件的人都得到口令。
- 影响：机密性，**持久**（相对 `/proc/<pid>/cmdline` 的短暂可见，这是新增的持久通道）；
  程序自己也把日志当作 Windows GUI 构建下唯一的错误通道（main.go:53-56 注释），
  说明这条路径必然被走到。
- 修复：错误文本不回显参数文本，只回显选项名：`strings.Cut(args[i],"=")` 得到的 `name`
  已足够定位（`fmt.Errorf("unknown option %q", name)`）；同时对超长/含 `=` 的未知参数
  截断到前 32 字节。
- 置信度：**高**（纯代码路径，无需运行）。运行期唯一未验证项：Windows GUI 构建下
  `logging.Log.Init` 是否总能开成 `app.log`（开不成时只走 stderr，泄漏面略小）。

### [S2] F-02 `--save=false`／`--install=false` 被当作真值，显式拒绝保存口令反而把口令写进 config.json

- 类别：参数解析 / 凭据暴露
- 位置：cli.go:283-290（`hasValue` 对无值开关被丢弃）、cli.go:251-260（`set` 忽略 value）、
  cli.go:587-597（`--save` 的落盘逻辑）
- 证据：
  ```go
  if takesValue(name) && !hasValue {      // cli.go:283：只对「需要值」的选项处理无值情形
      ...
      value = args[i]
  }
  opts.set(name, value)                    // cli.go:290：开关的 value 被丢掉
  ```
  ```go
  case "--save":
      o.save = true                        // cli.go:251-252：value 完全不看
  ```
- 攻击路径（操作者自伤，无需攻击者）：
  1. `SDAutoLogin run --account=23D001:Hunter2 --save=false`
     （作者本意：用一次就走，不落盘）。
  2. `parseOptions` 把 `--save=false` 解析为 `name="--save"`、`hasValue=true`，`takesValue`
     为假 ⇒ 不消费后续参数，直接 `set("--save","false")` ⇒ `o.save == true`。
  3. `loadForCLI`：`opts.save` 为真 → `len(opts.accounts) > 0` ⇒ `cfg.RememberPassword = true`
     （cli.go:591-593）→ `cfg.Save()`（cli.go:594）把**明文口令写进 config.json**。
  4. 同构路径：`update --install=false` → `opts.install == true` → 真的下载并覆盖程序文件
     （cli.go:839-841）；`--no-log=false` → `opts.noLog == true` → 不建日志文件。
- 影响：机密性（`config.json` 0600 目录 0700，但口令持久明文、会被拷贝/同步）+ 完整性
  （`--install=false` 触发一次计划外的自我覆盖）。持久。
- 修复：对无值开关，`hasValue` 为真且值非空即报错
  （例如 `"%s takes no value"`）；或接受 `=true/=false` 并真正解析布尔值，其余值报错。
- 置信度：**高**。

### [S2] F-03 `login` 的「密码错」与「门户不可达」同为退出码 1，且一次性路径无冷却 → 重试脚本会反复提交错误口令

- 类别：DoS / 一致性
- 位置：cli.go:945-985（`cmdLogin` 的三条 `return 1`）、cli.go:56-64（退出码文档只覆盖
  `status`/`update`）、cli.go:958（走 `app.TestLogin` 而非 service 的检查循环）
- 证据：
  ```go
  if !test.Result.Success {   // 门户拒绝：密码错、账号停用、ret_code 非 0
      return 1                 // cli.go:981-983
  }
  ```
  ```go
  if !ok { return 1 }                       // cli.go:947-949 配置读不出
  if len(attempts) == 0 { ... return 1 }    // cli.go:951-954 没账号
  ```
- 攻击路径（脚本自伤，现实运维写法）：
  1. 监控脚本 `while ! SDAutoLogin login; do sleep 5; done`（或 cron 每 10 秒一次，
     或 systemd `Restart=on-failure` 包一个 `login`）。
  2. 账号口令被改过期后，每次运行都「配置能读 + 门户可达 + 门户拒绝」→ 退出码 1，
     与「门户不可达」不可区分。
  3. `login` 是独立进程、每次新建 `app.New(cfg)`，**不共享** 长驻循环里的 10 分钟冷却
     （`internal/service/service.go:176 cooldownPeriod = 10 * time.Minute`，跨范围引用：
     该冷却只在 service 检查循环内生效，`TestLogin` 不用它）。
  4. 结果：每秒/每 5 秒一次错误口令提交，直到门户按失败次数锁定账号。
- 影响：可用性（账号封禁，需人工解锁）+ 门户侧风控；每次运行还额外多打一次状态查询
  （`TestLogin` 先 `QueryStatus` 再 `Login`，`internal/app/appstate.go:200-213`）。
- 修复：给 `login` 一个「认证失败」专用退出码（例如 3，与 `status` 的 offline 对齐）并写入
  `--help` 的退出码表；或在 `cmdLogin` 里对认证类失败打印明确的「不要重试，先核对口令」并
  在 Usage 中标注。
- 置信度：代码路径 **高**；「门户是否会因重复失败锁定账号」**无法验证**（无门户、无工具链）。

### [S2] F-04 非回环 `--settings` 时带令牌的设置页地址进日志，而设置页可改门户地址 → 已存口令可被引向攻击者服务器

- 类别：凭据暴露 / 代理与网络
- 位置：cli.go:116-121（用法文本推荐 `0.0.0.0:8088` 并说明「地址，含令牌，写在日志里」）、
  cli.go:651-676（`run` 建设置页并打印/记录地址）
- 证据：
  ```
  --settings=ADDR     also serve the settings page on ADDR, for example
                      127.0.0.1:8088 or 0.0.0.0:8088, so a machine with no tray
                      and no display can still be configured from a browser.
                      The address to open, token and all, is the one the log
                      prints; the bare port is refused
  ```
  ```go
  server := app.NewServer(state, *opts.settings, func() { close(stop) })  // cli.go:654
  url, err := server.URL()                                               // cli.go:662
  logging.Log.Warn("no account is written down yet, write one at %s", url) // cli.go:675
  ```
- 攻击路径：
  1. 前置：运维按用法文本在无桌面机器上执行
     `SDAutoLogin run --settings=0.0.0.0:8088 --account=… --save`。
  2. 该运行把 `http://127.0.0.1:8088/?token=<随机令牌>` 写进 stderr 与 `logs/app.log`
     （跨范围：`internal/app/settings.go:165-167` 先警告「只有地址里的令牌保护它」再把含令牌的
     URL 写日志）。任何能读到日志流/日志文件的用户（journald、日志采集器、运维共享终端）拿到令牌。
  3. 用该 URL 打开页面（局域网内可达），提交配置：`portalBaseUrl` 是设置页可写字段
     （跨范围证据：`internal/app/settings_test.go:602` 的配置 POST 体含
     `"portalBaseUrl":"http://10.9.1.3/"`），把它改成攻击者控制的 HTTP 服务器。
  4. 下一次登录（托盘或 `run`）把**明文口令**以 GET 参数送给该服务器（默认门户即
     `http://10.9.1.3/` 明文，`internal/config/config.go:136`），口令外泄且永久生效
     （配置已落盘）。
- 影响：机密性（账号口令），持久。前置条件 3 个（非回环绑定 + 攻击者能读到日志/控制台输出 +
  攻击者知道令牌），因此不评 S1。
- 修复：绑到非回环地址时，把含令牌的地址**只写控制台一次**、不写 `app.log`（或只写
  host:port + 令牌指纹）；用法文本里的 `0.0.0.0:8088` 例子旁补一句「只在可信网络/隧道后用」。
- 置信度：`--settings` 非回环可通过且地址含令牌进日志 **高**（cli.go:119-121 自述 +
  跨范围 settings.go:165-167）；「仅凭令牌即可改门户地址」**中**（证据是跨范围测试夹具，需
  设置页审查员确认写路径无额外确认）。

### [S2] F-13 代理环境变量未禁用：`http://` 门户上的明文口令随 GET URL 经代理发出

- 类别：代理与网络
- 位置（F 侧入口）：cli.go:704（`cmdStatus`）、cli.go:958（`cmdLogin`）
  → `portal.NewClient(cfg)`；**跨范围根因**：`internal/portal/portal.go:108-116` 的
  `&http.Client{Timeout: timeout}` 未设 `Transport` ⇒ 使用 `http.DefaultTransport`
  ⇒ `Proxy: http.ProxyFromEnvironment`
- 证据（跨范围，仅作根因引用）：
  ```go
  follow: &http.Client{Timeout: timeout},
  plain: &http.Client{ Timeout: timeout, CheckRedirect: ... },
  ```
  默认门户是明文 HTTP：`PortalBaseURL: "http://10.9.1.3/"`
  （`internal/config/config.go:136`），登录把 `user_password` 放在 GET 查询串里（README.md:59）。
- 攻击路径：
  1. `SDAutoLogin login`（或常驻 `run`/托盘）在设置了 `HTTP_PROXY`/`https_proxy` 等变量的
     环境里启动（企业容器、公司网络、被污染的 shell profile、被改写的服务 unit）。
  2. Go 的默认 Transport 自动选用该代理，门户是 `http://`，代理看到完整请求行
     `GET /?c=Portal&a=login&user_account=…&user_password=<明文>&…`。
  3. 代理运营方（或任何能改写该代理配置者）得到校园网账号口令。
- 影响：机密性（口令），每次登录都发生；持久（口令通常长期有效）。
- 修复：门户/更新客户端显式 `Transport: &http.Transport{Proxy: nil}`（校园内网直连不需要
  代理），或在文档中明确「本程序不使用代理，若环境里有 `HTTP_PROXY` 仍会生效」并加运行时
  检测告警。**该修复属 `internal/portal`/`internal/update` 范围，请对应审查员落地。**
- 置信度：Go 默认 Transport 行为 **高**；门户 URL 为明文 HTTP **高**（config.go:136）；
  是否真的存在代理 **环境相关**。**跨范围发现**：F 只负责确认入口不会关闭该行为、且默认门户是
  `http://`。

### [S3] F-05 回显关闭失败时静默以明文读入口令，提示语毫无区别

- 类别：凭据暴露 / 权限边界
- 位置：password.go:23-30、password_windows.go:17-23、password_other.go:16-19
- 证据：
  ```go
  restore, hidden := echoOff(os.Stdin)
  if hidden {
      defer fmt.Println()
      defer restore()
  }
  line, _ := in.ReadString('\n')     // hidden==false 时照读，且不告诉用户
  ```
- 攻击路径：目标机上 `stty` 不存在（精简容器）、`stdin` 不是终端、或
  `SetConsoleMode`/`GetConsoleMode` 失败（例如 Windows 控制台被非交互句柄替代、权限受限）
  → `ok=false` → 提示仍是 `password:`，用户以为隐藏输入，实际口令逐字显示在屏幕上，
  进入终端回滚缓冲、`script(1)` 记录、SSH 客户端日志或旁人视线。`--password-file` 之外的
  唯一交互通道因此可能不安全，而界面不做任何区分。
- 影响：机密性（肩窥/终端记录），**不持久**但难以察觉。
- 修复：`hidden == false` 时向 stderr 打一行明确警告（例如
  `warning: this terminal cannot hide what you type; use --password-file to keep the secret off the screen`），
  或直接拒绝并在非终端 stdin 时才继续。
- 置信度：**高**。

### [S3] F-06 口令输入被打断时回显不恢复（Windows 无信号处理；Unix 漏 `SIGQUIT`）

- 类别：可用性 / 架构
- 位置：password_windows.go:19-24（只返回一个靠 `defer` 调用的恢复函数）、
  password.go:26-29（恢复挂在 `defer` 上）、password_other.go:24-37（Unix 侧为此专门装了处理器，
  cli.go 侧无对应实现）
- 证据：
  ```go
  return func() { _ = windows.SetConsoleMode(handle, mode) }, true   // password_windows.go:24
  ```
  ```go
  signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)   // password_other.go:25
  ```
- 攻击路径：Windows 构建在 `config` 向导的口令提示处按 Ctrl+C（或任务管理器结束进程）：
  `!windows` 侧有处理器负责先 `stty echo` 再自杀（password_other.go:27-37），Windows 侧只依赖
  `defer`；进程被强制结束不会执行 defer，`ENABLE_ECHO_INPUT` 保持清零，此后该控制台里
  输入的任何命令都不显示（命令照常执行）。Unix 侧同类缺口是 `SIGQUIT`（`Ctrl+\`）：
  不在 `Notify` 列表里，Go 默认会打印栈并退出，`stty echo` 恢复函数不会被调用。
- 影响：可用性（用户控制台「看起来坏了」，且因命令仍执行而可能误输入）；影响有限但持续到
  控制台关闭。
- 修复：Windows 侧与 Unix 侧对称——在 `echoOff` 内 `signal.Notify(os.Interrupt)` 并在收到时
  先恢复模式再 `signal.Reset` + 重发信号（可把 `password_other.go` 的处理器抽成共用逻辑）；
  把 `syscall.SIGQUIT` 补进 Unix 列表。
- 置信度：代码不对称与 `SIGQUIT` 缺口 **高**；「Windows 上 Ctrl+C 一定走不到 defer」
  **中**（需 Windows 主机确认运行时行为）。

### [S3] F-07 命令名之前出现「已识别的短横词」时，命令被静默忽略并启动托盘模式

- 类别：参数解析
- 位置：main.go:63-81（分派条件）、cli.go:214-223（`knownWord`）
- 证据：
  ```go
  if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {   // main.go:63
      os.Exit(runCommand(rest, appDir, opts))
  }
  for _, a := range rest {                                  // main.go:68：只认这几个词
      switch a { case "help", "-h", ... case "-cli", ... }
  }
  ```
- 攻击路径：`SDAutoLogin -show config`（把 `config --show` 的开关写在命令前）。`parseOptions`
  把 `-show` 与 `config` 都留在 `rest`（cli.go:277-281），`rest[0]` 以 `-` 开头 ⇒ 不走
  `runCommand`；`main.go:68` 的 `switch` 不匹配 `-show`/`config` ⇒ 落到托盘分支：无退出码 2、
  无诊断输出，脚本在无桌面的机器上等一个永不出现的报告（`run`/`status` 都不执行）。
  同类：`SDAutoLogin -show status`、`SDAutoLogin -v`、`SDAutoLogin -h config`（后者打印用法并
  以 0 结束，也算「命令被吞」）。
- 影响：可用性/完整性（一次请求的动作被替换成另一个模式，且不报错）。
- 修复：分派改为「在 `rest` 里找第一个不以 `-` 开头的词作为命令；若存在命令词而其前有
  未被识别的短横词，报 `unknown option` 并 `exit 2`」，或在 `main.go:63` 前先扫描
  `rest` 中是否存在命令词。
- 置信度：**高**（静态推理；因无工具链未能实机复现）。

### [S3] F-08 `config` 向导无条件把 `rememberPassword` 改成 true

- 类别：权限边界 / 一致性
- 位置：setup.go:93（`cfg.RememberPassword = true`）、setup.go:66-67（读入口令）、
  setup.go:95（`cfg.Save()`）
- 证据：
  ```go
  cfg.RememberPassword = true     // setup.go:93，历史配置里的 false 被静默覆盖
  if err := cfg.Save(); err != nil {   // setup.go:95
  ```
- 攻击路径：用户此前特意把 `rememberPassword` 设为 false（或依赖 `passwordFile`）以免明文
  口令进 `config.json`；之后为了改检查间隔/语言/服务类型运行 `SDAutoLogin config`，
  向导不提示这一点，直接把开关写回 true 并落盘，口令随之明文保存（口令来自向导提示，
  或来自内存中已有的条目）。
- 影响：机密性（持久明文口令，用户的显式选择被推翻）；完整性（配置语义被改）。
- 修复：向导开始处读取原值，仅在用户显式回答一个新提问（「把口令写进配置文件？[y/N]」）
  后才置 true；未回答则保持原值。
- 置信度：**高**（`setup.go:93` 无分支）。附注：若口令来源是 `passwordFile`
  （`PasswordSource=SourceFile`），`config.Save()` 的 `marshal()` 仍会丢弃该口令
  （跨范围 `internal/config/config.go:560-566`），所以「口令原文落盘」只在
  口令来自配置/向导输入时成立。

### [S3] F-09 `CREDENTIALS_DIRECTORY` 静默覆盖配置里的 `passwordFile`，`--save` 还会把该路径写进配置

- 类别：权限边界 / 供应链
- 位置：cli.go:418-428（读环境变量）、cli.go:383-399（覆盖与落盘路径）、cli.go:587-594
- 证据：
  ```go
  func credentialPasswordPath() string {
      dir := os.Getenv("CREDENTIALS_DIRECTORY")     // cli.go:419
      if dir == "" { return "" }
  ```
  ```go
  case credentialPasswordPath() != "":
      cfg.PasswordFile = credentialPasswordPath()   // cli.go:391-393：压过 config.json 里的 passwordFile
      named = true
  ```
- 攻击路径：能给目标进程设置环境变量的主体（同一用户的 shell 配置、服务的 unit 文件
  `Environment=`、容器编排里可改的环境段、wrapper 脚本）设置
  `CREDENTIALS_DIRECTORY=/tmp/evil`（其中放一个名为 `password` 的文件）；此后每次登录都用该
  文件里的口令 —— 程序**不打印**实际采用的口令来源，日志里只有 `cfg.Summary()`（不含口令与
  来源）。若该次运行还带 `--save`，这个路径被写进 `config.json`（cli.go:387+594），
  后续托盘运行也持续从攻击者控制的文件取口令（把口令改错 → 反复认证失败 → 账号封禁）。
  程序对读到的口令文件也不做权限校验（跨范围 `internal/config/config.go:226-238`
  只处理「读不到/格式错」）。
- 影响：完整性（口令被替换，登录失败/封禁）+ 机密性（把口令读写引向攻击者选的路径）；
  前置条件需要能设置目标进程环境（用户级或更高），因此不评更高。
- 修复：口令来源决定后记一行不含秘密的日志（`password source: credential|file <path>|command line|config`）；
  当 `CREDENTIALS_DIRECTORY` 存在但文件对其他用户可读/可写时告警；
  `--save` 时若路径来自环境变量，要求显式 `--password-file` 才持久化。
- 置信度：行为 **高**；攻击前置条件如实标注（**中**：需要环境变量控制权）。

### [S3] F-10 `config.json`/`logs/` 落点受 `XDG_CONFIG_HOME`/`HOME` 影响，且不收紧已存在的目录

- 类别：权限边界 / 供应链
- 位置：main.go:251-267（`perUserDir`，`os.UserConfigDir()` + `MkdirAll(base, 0o755)` +
  `MkdirAll(dir, 0o700)`）、main.go:189-212（选择顺序：`--home` → exe 目录 → cwd → 用户配置目录）
- 证据：
  ```go
  base, err := os.UserConfigDir()                    // main.go:252：$XDG_CONFIG_HOME 或 $HOME/.config
  if err := os.MkdirAll(base, 0o755); err != nil {   // main.go:257：已存在的目录不会被收紧
  if err := os.MkdirAll(dir, 0o700); err != nil {    // main.go:260
  ```
- 攻击路径：程序装在 `/usr/bin`（`systemBinDir` 命中，main.go:226-228、238-246）或 exe 目录
  不可写时，配置目录落向 `os.UserConfigDir()`。能设置目标进程环境的主体把
  `XDG_CONFIG_HOME` 指向自己可读的目录（`MkdirAll` 对已存在目录不改正权限）：
  `XDG_CONFIG_HOME=/tmp/evil`，并预先创建 `/tmp/evil/sdautologin`（0777）；
  随后该进程的 `config.json`（含明文口令，若 `rememberPassword` 为真）与 `logs/app.log`
  写在攻击者可读的目录里；`--save`（cli.go:594）或托盘保存都会触发。
- 影响：机密性（配置与日志落在攻击者可读位置），持久。
- 修复：选定目录后校验并收紧权限（`os.Chmod(dir, 0o700)` 并检查 owner 是否为当前 uid、
  目录是否 world-writable，异常则拒绝启动并提示 `--home`）；日志/文档明确写出
  「`XDG_CONFIG_HOME`/`HOME` 决定配置落点」。
- 置信度：代码路径 **高**（`os.UserConfigDir` 遵循 `XDG_CONFIG_HOME`/`HOME` 是标准库行为）；
  攻击需要环境变量控制权（**中**）。

### [S3] F-11 CLI 的一次性 `login` 不取单实例锁，可与托盘并发提交登录

- 类别：一致性 / DoS
- 位置：cli.go:945-985（`cmdLogin` 无 `AcquireSingleInstance`）、cli.go:641（只有 `run` 取锁）、
  main.go:110（托盘取锁）
- 证据：
  ```go
  func cmdLogin(appDir string, opts options) int {
      cfg, ok := loadForCLI(appDir, opts)     // cli.go:946：直接进登录，无锁
      ...
      test := app.New(cfg).TestLogin(attempt) // cli.go:958
  ```
- 攻击路径：托盘（或 `run`）正在运行时，另开终端执行 `SDAutoLogin login`（或用
  `-test-login`）——两者都会向门户提交登录。防线只有 `TestLogin` 先查一次在线状态
  （跨范围 `internal/app/appstate.go:200-203`：已在线则「nothing submitted」），
  因此稳定态不重复提交；但当托盘恰好处于「离线 → 正在登录」的窗口内，两者会各提交一次，
  而用法文本自述门户「allows one session per address」（cli.go:48-50）——重复提交可能踢掉
  刚建立的会话或触发门户侧防滥用。此外 `login` 的一次性实现只共用 `portal.Client`
  （协议层一致），**不共用** service 的轮换/冷却策略（策略漂移，见 F-03）。
- 影响：可用性（会话抖动/门户风控）；影响有限。
- 修复：`cmdLogin` 与 `cmdStatus` 前加 `platform.AcquireSingleInstance(false)`，冲突时
  打印「已有实例在跑，登录由它负责」并返回专用退出码；或在 `login` 前向已有实例请求状态。
- 置信度：代码路径 **高**；门户并发后果 **中**（无法对真实门户验证）。

### [S3] F-12 命令行口令已文档化，但运行时没有任何警告（`--account` 的暴露也未单独说明）

- 类别：凭据暴露（文档/加固）
- 位置：cli.go:95-105（用法文本，含 `--password-file` 的理由）、cli.go:605-607（唯一的运行期
  提醒是 `--save`）、README.md:278-282、README.zh-CN.md:282-283
- 证据：
  ```
  --password-file=PATH
      ... A secret given on
      the command line is in the process list of every user on the
      machine, so this is the form to use from a script, a service or a container.
  ```
  ```go
  fmt.Fprintln(os.Stderr, "note: the values given on the command line are used for this run only, add --save to write them to the config file")
  ```
- **明确回答任务书 Q3**：口令出现在命令行上 **是已知且已文档化**的——依据
  README.md:280-282（「a secret on the command line is in the process list of every user on
  the machine」）、README.zh-CN.md:282-283、以及 `--help` 内 cli.go:99-103 的同一句话；
  程序还提供了 `--password-file`（cli.go:95-104）与 systemd `$CREDENTIALS_DIRECTORY`
  （cli.go:104、cli.go:418-428）两条更安全的通道，并在 README 的「Where the password is kept」
  一节说明威胁边界。**因此这不是「未文档化的凭据暴露」**。
- 仍存在的问题：实际使用 `--password`／`--account`（含口令）时，程序**不打印任何提示**
  （cli.go:605-607 只在未 `--save` 时提醒 `--save`），且 `--account=USER:PASSWORD` 的口令
  暴露在用法里只在 `--password-file` 段落被间接提到。
- 修复（最小）：运行期检测到「口令字符串来自命令行」时，向 stderr 打一行
  `warning: the password is in this process's command line, readable by any user on this machine`，
  并在 `--account` 的用法段落单独点明同样的话。
- 置信度：**高**。

### [S4] F-14 无 `--`/`-` 终止符，值形如「已识别选项」时无法传入

- 类别：参数解析
- 位置：cli.go:277-278（`--`、`-` 本身被当成未知选项）、cli.go:283-286（缺值判定）
- 证据：
  ```go
  if strings.HasPrefix(args[i], "-") && !knownWord(args[i]) {
      return opts, nil, fmt.Errorf("unknown option %q", args[i])
  }
  if i+1 >= len(args) || knownOption(args[i+1]) { return opts, nil, fmt.Errorf("%s needs a value", name) }
  ```
- 具体行为：`--` → `unknown option "--"`（无终止符语义）；`-` → 同上；`--password --json`
  → `--password needs a value`（口令恰好等于某个选项名时无法用空格形式给出，只能
  `--password=--json`——而这一形式**可以**，因为 `hasValue` 分支跳过了缺值检查）。
- 影响：可用性；无安全后果。
- 修复：识别 `--` 为终止符；`--password` 后即使下一个词是已知选项也优先当值（或要求
  用 `--password=…`）。
- 置信度：**高**。

### [S4] F-15 值可以以 `-` 开头，因此 `--home -cli` 会创建一个名为 `-cli` 的目录

- 类别：参数解析
- 位置：cli.go:284（只拒绝「已知选项」作值）、main.go:190（`--home` 直接采用）、
  main.go:37（`logging.Log.Init(filepath.Join(appDir, "logs"))` 会 `MkdirAll`）
- 证据：`if i+1 >= len(args) || knownOption(args[i+1]) { ... } else { i++; value = args[i] }`
- 具体行为：`SDAutoLogin --home -cli run` → `home="-cli"` → 相对路径 → 在 cwd 下创建
  `-cli/logs/` 与 `-cli/config.json`。这是「允许值以 `-` 开头」这一正确取舍的副作用；
  `--password`/`--account` 的同类行为是有意为之（`TestParseOptionsAcceptsAValueThatLooksLikeADash`，
  cli_test.go:140-151）。
- 影响：可用性/整洁性，无安全后果。
- 修复：对 `--home` 这类路径型选项做一次 `filepath.IsAbs`/存在性检查并在 stderr 提示。
- 置信度：**高**。

### [S4] F-16 命令后的多余位置参数被静默忽略；`--app ADDR` 会被报成「未知命令」

- 类别：参数解析
- 位置：cli.go:533-563（`runCommand` 只看 `args[0]`）、cli.go:992-997（`cmdConfig` 只筛
  `--show`）、cli_test.go:723-734（证实 `--app ADDR` 把 ADDR 留作 stray 参数）
- 证据：`command := args[0]` + `case "config": return cmdConfig(appDir, args[1:], opts)`
  （多余参数既不报错也不使用）；`--app` 不在 `takesValue`（cli.go:203-209），裸地址留在 `rest`
  → `main.go:63` 当作命令 → `unknown command "0.0.0.0:8088"` + `exit 2`。
- 影响：可用性（诊断信息误导：用户写的是地址，却被说成未知命令）；退出码非 0，是安全方向。
- 修复：命令解析后若 `len(rest) > 1` 且其余不是该命令的开关则报错；`--app` 缺少 `=` 时给出
  专门提示（`--app takes its address with an '='`）。
- 置信度：**高**。

### [S4] F-17 `applyTo` 的第三条互斥分支不可达

- 类别：参数解析（死代码）
- 位置：cli.go:313-323
- 证据：
  ```go
  case len(o.accounts) > 0 && (o.username != nil || o.password != nil || o.passwordFile != nil || o.isp != nil):
      ...
  case len(o.accounts) > 0 && o.password != nil:   // cli.go:321：已被上一分支覆盖
  ```
- 说明：`--account` 与 `--password` 的组合永远先被第一条命中，因此 cli.go:322 的专门错误
  消息永不出现；表意上的差异（该用哪条信息提示）丢失，行为仍正确（都拒绝）。
- 影响：无安全影响。
- 修复：删掉该分支，或把它提到第一条之前。
- 置信度：**高**。

---

## 架构与设计层面问题

1. **日志是 Windows GUI 构建下唯一的错误通道，因此任何回显错误文本都等于写文件**。
   `main.go:53-56` 的注释自述：构建为 GUI 程序时参数错误「leaves nothing on screen and
   app.log is the only trace」；`console_windows.go:27-41` 只 `AttachConsole`
   （**没有 `AllocConsole`**），且只有「带参数」的运行才尝试挂父控制台（main.go:39-43）。
   结论：Windows 上从资源管理器/`run.cmd`（`start "" dist\SDAutoLogin.exe`）启动的托盘运行
   没有任何可见输出，错误只能进 `logs/app.log` —— 这正是 F-01 的放大因素，
   也意味着「不把秘密写进错误文本」是入口层的硬约束。
2. **无控制台时读写不会 panic**：stdout/stderr 保持无效句柄（`fmt.Fprint*` 的返回错误被
   忽略），`attachConsole` 在 `GetStdHandle` 有效或管道/文件重定向时提前返回
   （console_windows.go:28-33），无 `AllocConsole` 就没有窗口弹出、也没有 panic 路径。
   代价是「启动失败静默」（见上一条）。
3. **单实例锁只在两个模式里生效**（main.go:110、cli.go:641），`login`/`config`/`update` 裸奔；
   无显式释放 API，靠进程退出释放。**跨范围观察（不在 F 的发现编号内，供 platform 审查员确认）**：
   Unix 侧锁文件可能落在 `os.TempDir()`（`internal/platform/singleinst_unix.go:65`，
   即 `/tmp/sdautologin.lock`），若另一个本地用户抢先用**自己拥有、0600** 的文件占住该名字，
   受害进程 `os.OpenFile` 失败后按 `acquireInstance` 的路径「无法判断是否已有实例，照常启动」
   （`singleinst_unix.go:143-151`）——即**单实例保护可被本地非特权用户静默关闭**，导致多份
   检查循环同时向门户提交登录（与 F-03/F-11 同类的封禁风险）；反之若攻击者先 `flock` 住该文件，
   托盘的 `runGUI` 会直接 `return`（main.go:110-112），表现为「双击没反应、退出码 0」。
   两方向都值得 platform 审查员单独立项。
4. **CLI 与托盘的一致性**：协议层共用（`cmdLogin` 走 `app.New(cfg).TestLogin` → 同一个
   `portal.Client`，cli.go:958），**策略层不共用**（轮换顺序、10 分钟冷却、禁用条目、
   `switchToPreferredService` 只存在于 `internal/service`）。因此「同一次登录的行为一致，
   但重试/退避策略不一致」，`login` 的一次性路径没有任何退避（F-03）。
5. **CLI 模式日志落点与权限**：`run`/`status`/`login` 都把日志写到 `<appDir>/logs/app.log`
   （main.go:37），目录 0700、文件 0600（`internal/logging/log.go:21-24`），
   `--no-log` 或配置里 `logToFile:false` 时关闭文件副本（main.go:37、cli.go:612-617）；
   `--quiet` 只关详细级别（cli.go:404-406）。即：CLI 与托盘共用同一日志文件与权限模型，
   不存在「CLI 写到更宽松位置」的问题。
6. **`setup.go` 生成的文件**：向导自己不创建任何文件（`setup.go` 内只有 `cfg.Save()`，
   setup.go:95），落盘由 `internal/config` 的 `Save()` 完成（0600 + `Chmod` +
   Windows ACL，跨范围 `config.go:503-543`），stdout 只打印语言/账号/服务/间隔与
   `saved to <path>`（setup.go:100），**不回显口令**；唯一与安全相关的副作用是
   setup.go:93 无条件打开 `rememberPassword`（F-08）。
7. **优先级/覆盖关系**（威胁模型 3 的直接回答）：`--password` > `--password-file` >
   `$CREDENTIALS_DIRECTORY/password` > `config.json` 的 `passwordFile` > `config.json` 的
   `password`（cli.go:383-399，README.md:410-419 亦如此描述）。**门户地址无法由环境变量或
   命令行改写**（只有 `config.json` 与设置页；`--settings` 只决定设置页监听地址），
   **配置路径不能由环境变量直接指定**（只有 `--home`），但可经 `XDG_CONFIG_HOME`/`HOME`
   间接重定向（F-10）；**强制 `--save` 把口令写进攻击者可读位置**需要攻击者同时具备
   「设置环境变量」（F-09/F-10）与「预置可写目录」两个条件，且 `config.json` 会以 0600 写入
   当前用户拥有的目录，所以只有在攻击者能预置**该目录本身**（F-10 的 0777 情形）时才成立。

---

## 覆盖声明

**已逐行读完（本次范围）**：`cmd/SDAutoLogin/main.go`(357)、`cli.go`(1019)、`setup.go`(129)、
`password.go`(35)、`password_windows.go`(25)、`password_other.go`(52)、`console_windows.go`(46)、
`console_other.go`(7)、`cli_test.go`(1384)。未跳读、无 `t.Skip` 之外的省略。

**为理解调用关系而只读（未写结论，引用时标注「跨范围」）**：`internal/config/config.go`
（`Save`/`marshal`/`normalize`/`Summary`/`LoadPasswords`/`defaultConfig`）、
`internal/config/passwordfile.go`、`internal/portal/portal.go`（`NewClient`/`MaskSecrets`）、
`internal/logging/log.go`、`internal/platform/singleinst_unix.go`、`internal/platform/exec.go`、
`internal/platform/autostart_other.go`、`internal/platform/appwindow.go`、
`internal/i18n/locale_other.go`、`internal/app/settings.go`、`internal/app/appstate.go`、
`go.mod`、`run.cmd`、`tools/build/main.go`（`-H=windowsgui`）、README.md、README.zh-CN.md。

**未读**：`internal/service`、`internal/tray`、`internal/update`、`internal/i18n` 其余文件、
`internal/winres`、`winres/`、`packaging/`、`Dockerfile`、`tools/build` 其余文件、
`docs/audit/` 其他审查员报告、`CHANGELOG*`。

**测试审查结论（`cli_test.go` 1384 行）**：覆盖良好的是**纯函数层**——`parseOptions` 两种写法
（21-48、50-69）、未知选项/缺值/值像短横（85-99、132-151）、显式空值（202-219）、
`--app` 可选值（697-735）、`applyTo` 全部分支（229-567）、`parseAccount` 含坏值不回显
（569-625）、`check`/`checkTrayOptions`/`checkTray`（737-777、838-915）、
`autostartArguments` **断言口令与账号不进启动项**（779-833）、`statusState`/`statusExitCode`/
JSON 字段集（917-1019）、`loadForCLI` 真读真写 `config.json`（361-390、439-485，
其中 458-481 断言口令文件里的秘密**没有**被写进配置）、`cmdUpdate` 用 `httptest` 真跑
（1220-1384）。

**未覆盖（测试盲区，均给出具体行号）**：

- `promptPassword`/`echoOff`（password.go:14、password_windows.go:15、password_other.go:16）
  **零测试**——回显关闭、失败降级、恢复路径全部未验证（对应 F-05/F-06）。
- `attachConsole`/`validStdHandle`（console_windows.go:27、43）**零测试**，Windows 控制台
  路径从未被验证（对应 F-01 的「Windows 只靠日志」前提）。
- 单实例锁：`cmd` 包测试里**没有** `AcquireSingleInstance`（全仓库只有
  `internal/platform/singleinst_unix_test.go` 测 platform 内部），因此「CLI 与托盘是否会
  互斥」从未被验证（对应 F-11）。
- `cmdRun`、`cmdStatus` 的文本输出、`cmdLogin`、`cmdConfig`、`runSetupWizard`、`runGUI`、
  `runHeadless` 均**未被调用**；`captureOutput`（1174-1198）只捕获进程内 `os.Stdout/Stderr`，
  `main()` 的 `os.Exit` 路径没有任何测试（因此退出码清单本身未被测试固化）。
- 全部测试用**注入的切片**（`parseOptions(args)`）或直接结构体，**没有一处用真实 `os.Args`**
  （grep 确认 `os.Args` 在 `cmd/` 的测试中无匹配），因此「参数解析 → `main` 分派 →
  `exit code`」这条链没有端到端验证（对应 F-07、F-16）。
- **没有任何测试断言口令不出现在输出/日志里**（唯一相关断言是 cli_test.go:621-623 针对
  `parseAccount` 的错误文本、以及 462-464 针对 `config.json` 内容）；`parseOptions` 的
  未知选项错误文本（cli.go:278）没有「不回显值」的断言——这正是 F-01，测试反而在
  cli_test.go:86-87 的注释里只关心「报错而不是静默」。
- 重复标志（`--home=a --home=b`）、`--flag=val` 形式的**无值开关**（即 F-02 的
  `--save=false`）完全没有用例；`--`/`-` 终止符、非 UTF-8、超长参数、大小写变体无用例。
- `t.Skip`：本文件仅 1336 行（「this build carries a signing key」，即带签名密钥的构建跳过
  「无密钥」那条安装路径）；其他 `t.Skip` 都在 `internal/*/` 与 `tools/`（
  config_test.go:789、824、log_test.go:58、install_test.go:77、autostart_test.go:17、
  tools/build/version_test.go:33）。无 `t.Skip` 掩盖 F 范围内的路径。
- 无 mock 掩盖的真实路径：`applyTo` 的 `--save` **真的**写文件（439-485），
  `cmdUpdate` **真的**打 HTTP（httptest，1220+）；被替换的只有 `updateClient`（1148-1153，
  有意注入）。

**无 Go 工具链，未做**：`go build`、`go vet`、`go test ./cmd/...`（含 `-race`）、
`-H=windowsgui` 构建产物检查、`stty`/`SetConsoleMode` 实机行为、`/proc/pid/cmdline` 暴露面实测、
代理环境变量实测、真实门户的失败计数/封禁策略。

**无法验证事项（明确列出）**：

1. 任何运行期行为（一次都没执行程序）：F-07 的分派吞命令、F-02 的真值翻转、F-06 的
   Windows Ctrl+C 后控制台模式、F-13 的代理选择，均为静态推理。
2. 门户是否按失败次数锁定账号（决定 F-03 的严重度上限）；门户「one session per address」的
   实际后果（F-11）。
3. Windows 构建下 `logging.Log.Init` 是否总能打开 `app.log`（若失败，F-01 的泄漏面退化为
   stderr 一处）；Windows 控制台代码页/`GetCommandLineW`→UTF-8 转换是否会篡改非 ANSI
   字符的口令（**无法验证**，无 Windows 运行环境，且任务书要求不修改源码）。
4. 仅凭设置页令牌是否确实能改写 `portalBaseUrl`（F-04 的跨范围证据是
   `internal/app/settings_test.go:602` 的请求体夹具，非写路径确认）。
5. 非 UTF-8 参数字节在 Linux/Unix 上的实际表现（Go 原样传字节，未见过滤；未实测）。

---

## 附录：关键代码摘录

A. 参数解析与未知选项回显（cli.go:274-293）：
```go
for i := 0; i < len(args); i++ {
    name, value, hasValue := strings.Cut(args[i], "=")
    if !knownOption(name) {
        if strings.HasPrefix(args[i], "-") && !knownWord(args[i]) {
            return opts, nil, fmt.Errorf("unknown option %q", args[i])
        }
        rest = append(rest, args[i])
        continue
    }
    if takesValue(name) && !hasValue {
        if i+1 >= len(args) || knownOption(args[i+1]) {
            return opts, nil, fmt.Errorf("%s needs a value", name)
        }
        i++
        value = args[i]
    }
    opts.set(name, value)
}
```

B. 无值开关丢弃 `=val`（cli.go:251-252）与 `--save` 的落盘（cli.go:587-597）：
```go
case "--save":
    o.save = true
...
if opts.save {
    if opts.password != nil || opts.passwordFile != nil || len(opts.accounts) > 0 {
        cfg.RememberPassword = true
    }
    if err := cfg.Save(); err != nil {
```

C. 错误进日志与 stderr（main.go:53-61）：
```go
if err != nil {
    logging.Log.Error("%v", err)
    fmt.Fprintf(os.Stderr, "%v\n\n", err)
    printUsage(os.Stderr)
    os.Exit(2)
}
```

D. 命令分派只看 `rest[0]` 是否以 `-` 开头（main.go:63-81）：
```go
if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
    os.Exit(runCommand(rest, appDir, opts))
}
for _, a := range rest {
    switch a {
    case "help", "-h", "--help", "-help": ...
    case "-cli", "--cli", "-daemon", "--daemon", "-headless":
        exitIfBadOptions(opts, "run", cmdRun, appDir)
```

E. 口令来源优先级与 systemd 凭据（cli.go:383-399）：
```go
switch {
case len(o.accounts) > 0 && credentialPasswordPath() != "": return fmt.Errorf(...)
case o.passwordFile != nil:      cfg.PasswordFile = *o.passwordFile; named = true
case o.password != nil:
case credentialPasswordPath() != "": cfg.PasswordFile = credentialPasswordPath(); named = true
}
if named { if notice := cfg.LoadPasswords(); notice != "" { return fmt.Errorf("%s", notice) } }
```

F. 无值开关的真值翻转路径（cli.go:226-265 摘要）：
```go
func (o *options) set(name, value string) bool {
    switch name {
    ... case "--save": o.save = true
    case "--json": o.json = true
    case "--install": o.install = true
    case "--no-log": o.noLog = true
    case "--quiet": o.quiet = true
```

G. 回显关闭与失败降级（password.go:23-30 / password_windows.go:17-24）：
```go
restore, hidden := echoOff(os.Stdin)
if hidden { defer fmt.Println(); defer restore() }
line, _ := in.ReadString('\n')
```
```go
if err := windows.SetConsoleMode(handle, mode&^windows.ENABLE_ECHO_INPUT); err != nil {
    return nil, false
}
return func() { _ = windows.SetConsoleMode(handle, mode) }, true
```

H. 单实例锁的两个调用点与 `run` 的失败返回（main.go:107-112 / cli.go:641-645）：
```go
func runGUI(appDir string, opts options) {
    if !platform.AcquireSingleInstance(true) { return }
```
```go
if !platform.AcquireSingleInstance(false) {
    logging.Log.Info("another instance is already running, so this one stops")
    fmt.Fprintln(os.Stderr, "another instance is already running")
    return 1
```

I. `login` 的输出与失败码（cli.go:958-984）：
```go
test := app.New(cfg).TestLogin(attempt)
...
fmt.Printf("submitted    : %s (password %d chars, entry %d of %d)\n",
    attempt.Entry(), len(attempt.Password), attempt.Index+1, len(cfg.Accounts))
...
if !test.Result.Success { return 1 }
```

J. 向导无条件打开密码保存（setup.go:88-100）：
```go
if v := prompt(in, "check interval in seconds", strconv.Itoa(cfg.CheckIntervalSeconds)); v != "" {
    if n, err := strconv.Atoi(v); err == nil && n >= 5 { cfg.CheckIntervalSeconds = n }
}
cfg.RememberPassword = true
if err := cfg.Save(); err != nil {
```

K. 自动启动项只重复 `--home`（main.go:296-308，配套测试 cli_test.go:779-833）：
```go
func autostartArguments(opts options) []string {
    if opts.home == nil || *opts.home == "" { return nil }
    dir, err := filepath.Abs(*opts.home)
    if err != nil { dir = *opts.home }
    return []string{"--home=" + dir}
}
```
