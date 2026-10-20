# D：平台集成与托盘审查报告

- 审查员：D 号（对抗性安全审查）
- 范围：`internal/platform/`（全部 18 个文件）、`internal/tray/`（全部 19 个文件）、`cmd/SDAutoLogin/` 的 4 个平台文件、`packaging/systemd`、`packaging/openwrt`、`packaging/deb/control`
- 威胁模型：① 本机提权（重点）② 本机非特权攻击者 ③ 网络与外部输入
- 证据规则：每条发现均给出 file:line + 逐字引用。凡静态阅读不能确定者，明确写「无法验证（原因）」。

**工具链更正（影响本报告的验证强度）**：任务书告知「本机无 Go 工具链，不要尝试构建」，据此我为每条结论准备了静态证据。实际执行中发现 **WSL 侧存在 go1.26.0**（`wsl.exe -e bash -lc 'command -v go'` → `/usr/bin/go`；`go version` → `go version go1.26.0 linux/amd64`；Windows 侧确实没有 go）。因此我额外做了三项**只读**验证（不修改任何源码，只写构建缓存）：

1. `go vet ./internal/platform ./internal/tray`（linux）→ 无输出（通过）
2. `CGO_ENABLED=0 GOOS=windows go vet ./internal/platform ./internal/tray ./cmd/SDAutoLogin` → 无输出（通过），与 CI 的 windows 步骤同一条命令（`tools/build/main.go:379`）
3. `go test -count=1 ./internal/platform ./internal/tray` → `ok sdautologin/internal/platform 0.250s` / `ok sdautologin/internal/tray 0.005s`；`go test -list '.*'` 得到「本平台实际存在的测试清单」（见「从未被测试执行的平台文件清单」）

未运行 `go vet`/`go test` 之外的任何构建（未产出二进制）。凡涉及**运行时行为**（Windows Win32 调用的实际返回值、DBus 通知的真实渲染、OpenWrt 上 /tmp 的挂载权限）仍为静态阅读。

## 摘要

共 13 条发现：**S1 × 2、S2 × 3、S3 × 6、S4 × 2**。

最值得先处理的两条：

- **[S1] Unix 单实例的锁与汇合套接字放在 `/tmp` 下的固定可预测路径，且无属主校验** —— 任意本机用户 `flock` 同一路径即可让别人的 SDAutoLogin 静默拒绝启动（`runGUI` 直接 `return`），且 `/tmp` 的 sticky 位使受害者无法清除攻击者的文件。这是本次审查中唯一「无需特殊前置条件、跨用户、静默、可持久」的失效路径。
- **[S1] OpenWrt 的 `/usr/bin/SDAutoLogin` 是 root 身份运行的 shell 包装脚本，它把程序解包到 `/tmp/sdautologin/` 并直接 `exec` 该路径，全程不校验属主与权限** —— 只要设备上存在任何非 root 账号，即可预先放置同名可执行文件（或把该目录做成指向自己目录的符号链接），在下次安装/升级/重启后以 root 执行。

其余为：Windows 命名对象抢占、设置页 token 进入子进程 argv、systemd 加固项缺口、锁文件符号链接/fail-open、`.desktop` 与注册表值的引号与字段码、PATH 解析、网络可控文本进入系统通知 API。**外部进程调用清单中没有任何 `cmd /c`、`sh -c`、`system()` 形式的 shell 拼接**——托盘侧只有 Linux/macOS 的两个 shell 脚本（OpenWrt 包装脚本、init 脚本），这两个才是真正的 shell 面。Windows `ImagePath`/`Run` 值**带引号、无 unquoted path 问题**，程序清单为 **`asInvoker`（不提权）**，注册表只写 HKCU。

## 外部进程调用清单

逐字命令与参数构造，按调用点逐个列出。「参数来源」列的含义：常量 / 配置（本地文件或 CLI）/ 网络（对端响应）/ 运行期生成。

| # | file:line | 逐字命令与参数构造 | 参数来源 | 经 shell? | 风险 |
|---|---|---|---|---|---|
| 1 | `internal/platform/notify_other.go:20` | `exec.Command(name, args...)`，其中 Linux `notify-send` + `args=[title, body]`；darwin `osascript` + `args=["-e", "display notification "+appleScriptString(body)+" with title "+appleScriptString(title)]`（`notify.go:8-20`） | title＝i18n 常量；**body＝i18n 常量 + `s.Message`（门户明文 HTTP 响应文本）** | 否（argv 直传，无 shell） | 通知正文为网络可控文本（S3-5）；body 含换行时 AppleScript 字面量失效 → 通知静默丢失（S4-2） |
| 2 | `internal/platform/openurl_other.go:16` | `exec.Command("open", target)` | target＝运行期生成的 `http://127.0.0.1:<port>/?token=<token>`（`app/settings.go:212`）或本地 config/log 路径 | 否 | **token 出现在子进程 argv**（S2-3）；`open` 经 PATH 解析（S3-4） |
| 3 | `internal/platform/openurl_other.go:18` | `exec.Command(os.Getenv("BROWSER"), target)` | 同上 + `$BROWSER` 环境变量 | 否 | $BROWSER 被当作可执行名（含空格/参数会失败）；PATH 劫持面（S3-4） |
| 4 | `internal/platform/openurl_other.go:20` | `exec.Command("xdg-open", target)` | 同 #2 | 否 | token 入 argv（S2-3）；PATH（S3-4） |
| 5 | `internal/platform/appwindow.go:27` | `exec.Command(name, args...)`；`appWindowCommand`（:43-48）给出 darwin `open -na <browser> --args --app=<target>`，其余 `<browser> --app=<target>` | browser＝候选查找结果；target＝运行期 token URL | 否 | token 入 argv（S2-3） |
| 6 | `internal/platform/appwindow.go:107` | `exec.LookPath(candidate)`（候选：`google-chrome`/`chromium`/`microsoft-edge`/`brave-browser` 等裸名，`appwindow.go:84-92`） | 常量名 + `$PATH` | 否 | PATH 劫持：PATH 中靠前的同名程序会被执行（S3-4） |
| 7 | `internal/platform/appwindow.go:65-72` | Windows 候选路径 `filepath.Join(base, filepath.Join("Google","Chrome","Application","chrome.exe"))`，base ∈ `%ProgramFiles%`、`%ProgramFiles(x86)%`、**`%LOCALAPPDATA%`** | 环境变量 + 常量相对路径 | 否 | LOCALAPPDATA 是用户可写目录；程序为 `asInvoker`（同完整性），故不构成提权，但属「可写目录中的可执行文件被信任」模式（S3-4 备注） |
| 8 | `internal/platform/openurl_windows.go:15` | `procShellExecuteW.Call(0, "open", target, 0, 0, SW_SHOWNORMAL)` | target＝token URL / config 路径 / log 路径（`tray_windows.go:418,422`） | 否（ShellExecuteW 走关联处理，不经 cmd.exe） | 无命令注入；但 `file` 参数交给 shell 关联解析 |
| 9 | `internal/platform/autostart_windows.go:19` | `exec.Command("reg", "query", <HKCU Run 键>, "/v", "SDAutoLogin")` | 常量；`reg` 来自 PATH | 否 | PATH（S3-4） |
| 10 | `internal/platform/autostart_windows.go:35` | `exec.Command("reg", "add", <Run键>, "/v", "SDAutoLogin", "/t", "REG_SZ", "/d", command, "/f")`，`command = autostartCommand()`＝`"<exe>"` + `--home=<dir>`（`exec.go:30-36`） | exe＝自身路径；dir＝CLI `--home`（配置） | 否 | 写入的是**注册表持久化项**；command 未转义 `"`（S3-3） |
| 11 | `internal/platform/autostart_windows.go:38` | `exec.Command("reg", "delete", <Run键>, "/v", "SDAutoLogin", "/f")` | 常量 | 否 | — |
| 12 | `cmd/SDAutoLogin/password_other.go:49` | `exec.Command("stty", arg)`，arg ∈ {`-echo`, `echo`}（常量），`cmd.Stdin = terminal` | 常量；`stty` 来自 PATH | 否 | PATH（S3-4）；子进程得到终端 stdin |
| 13 | `packaging/openwrt/root/usr/bin/SDAutoLogin:21,24,26,31,32` | `rm -f /tmp/sdautologin/SDAutoLogin-*` / `mkdir -p "$(dirname "$unpacked")"` / `gunzip -c "$archive" > "$partial"` / `chmod 0755 "$partial"` / `mv "$partial" "$unpacked"` / `exec "$unpacked" "$@"`；`unpacked=/tmp/sdautologin/SDAutoLogin-$version` | archive 与 version 为构建期常量；目标目录固定为 `/tmp` | **是（sh 脚本，root 身份）** | **root 执行 /tmp 下未校验属主的文件（S1-2）**；`dirname`/`gunzip`/`chmod`/`mv` 经 PATH（S3-4） |
| 14 | `packaging/openwrt/root/etc/init.d/sdautologin:50-69` | `set -- run "--home=$HOME_DIR"` … `--username=$username` / `--password-file=$password_file` / `--password=$password` / `--isp=$isp` / `--settings=$settings`；`procd_set_param command "$PROG" "$@"` | **UCI 配置 `/etc/config/sdautologin`**（含账号密码） | 否（procd 直接 exec，非 sh -c） | 无命令注入；但 `--password=` 使密码进入进程 argv（S3-1 备注） |
| 15 | `packaging/openwrt/control/postinst:8-9`、`prerm:13` | `/etc/init.d/sdautologin enable/start/stop`（`[ -n "${IPKG_INSTROOT}" ] || …` 守卫） | 常量 | 是（sh，root） | 无注入面（无变量拼接） |
| 16 | `packaging/deb/control/postinst:7,11,14,15,18`、`prerm:7` | `systemd-sysusers ${DPKG_ROOT:+--root="$DPKG_ROOT"} sdautologin.conf`、`systemctl --system daemon-reload`、`deb-systemd-helper unmask/enable`、`deb-systemd-invoke restart/stop sdautologin.service` | `$1`＝dpkg 动作、`$DPKG_ROOT`＝dpkg 环境 | 是（sh，root） | 无 `rm -rf`、无未加引号变量做删除；`$DPKG_ROOT` 由 dpkg 提供（S3 备注） |
| 17 | `packaging/systemd/system/sdautologin.service:45,49`、`user/sdautologin.service:38,42` | `ExecStart=/usr/bin/SDAutoLogin run --home=/var/lib/sdautologin`、`ExecReload=/bin/kill -HUP $MAINPID`；user 版 `ExecStart=%h/.local/bin/SDAutoLogin run --home=%h/.local/share/sdautologin` | 常量 / systemd 说明符 | systemd 直接 exec（非 shell） | 绝对路径、不可被写入（见「提权与自启动面清单」） |

调用点合计 **17 个**（含 OpenWrt/Debian 打包脚本与 systemd 的启动路径）；其中 **9 个的参数含配置或网络来源**：#1（网络）、#2/#3/#4/#5/#8（运行期 token URL 或配置/日志路径）、#7（环境变量构造的可执行路径）、#10（配置/命令行内容写入注册表）、#14（UCI 配置，含密码）。

### Shell 解释面（命令注入相关）

- 代码内**没有任何** `cmd /c`、`sh -c`、`system()`、`os.system` 式拼接：以上 #1–#12 全部是 `exec.Command(name, args...)` 或 `ShellExecuteW`（argv 直传，Go 不做 shell 解析）。
- 唯一的 shell 面是**两个随包发布的脚本**：#13（root 运行，含 `>` 重定向到 /tmp 路径）与 #14（sh，但只做 `set --` 数组构造后交给 procd，值未回送入 shell 求值）。#15/#16 为 dpkg/opkg 维护者脚本，变量均加引号或来自受信环境。

## 提权与自启动面清单

| 平台 | 机制 | 写入路径（值/命令） | 运行身份 | 权限与校验 |
|---|---|---|---|---|
| Windows | HKCU Run 值 | `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`，值名 `SDAutoLogin`，值为 `"<exe>" --home=<dir>`（`autostart_windows.go:13-15,35`） | 交互用户（清单 `requestedExecutionLevel level="asInvoker"`，`winres/rsrc_windows_amd64.syso:277`） | 只写 HKCU（非 HKLM），无需管理员；**路径带引号**（`exec.go:16` → `"`+exe+`"`），无 unquoted path 问题；写入结果用 `reg query` 复核（`autostart_windows.go:41-49`） |
| Linux | XDG autostart | `~/.config/autostart/sdautologin.desktop`（`autostart_other.go:28`），`os.WriteFile(path, …, 0o644)`（:70），目录 `MkdirAll(…, 0o755)`（:62） | 桌面会话用户 | `Exec=` 行由字符串拼接（:80-88）→ 见 S3-3；文件为 0644（会话用户可写，属主即运行者，无跨用户面） |
| macOS | LaunchAgent | `~/Library/LaunchAgents/com.sdautologin.plist`（:26），0644（:70） | 登录用户 | `ProgramArguments` 数组 + 逐项 `xmlEscape`（:90-107,111-115）——本项实现正确 |
| Linux (服务) | systemd system 单元 | `/usr/lib/systemd/system/sdautologin.service`（0644，`tools/build/pack_deb.go:78`）；数据目录 `/var/lib/sdautologin`，`StateDirectoryMode=0700` | `User=sdautologin`/`Group=sdautologin`（非 root） | 加固项：`NoNewPrivileges`、`PrivateTmp`、`PrivateDevices`、`ProtectSystem=strict`、`ProtectHome`、`ProtectKernel*`、`ProtectControlGroups`、`RestrictAddressFamilies`、`RestrictNamespaces`、`RestrictRealtime`、`LockPersonality`、`MemoryDenyWriteExecute`、`SystemCallArchitectures=native`、`UMask=0077`（:56-73）。**缺口**：无 `CapabilityBoundingSet=`、无 `SystemCallFilter=`、无 `ProtectProc=`/`ProcSubset=`、无 `PrivateUsers=`（S3-1） |
| Linux (服务) | sysusers | `/usr/lib/sysusers.d/sdautologin.conf`（0644，`pack_deb.go:82`）：`u sdautologin - "…" /var/lib/sdautologin` | 由 `postinst` 的 `systemd-sysusers` 创建（`postinst:6-8`） | 与 `StateDirectory=sdautologin` 的 0700 一致；config.json 0600 / 目录 0700（`config/config.go:507,512`）→ 高权限进程不读低权限用户可写配置 |
| Linux (用户会话) | systemd user 单元 | `~/.config/systemd/user/sdautologin.service`（0644，安装说明 `user/…service:5-6`） | 该用户 | `UMask=0077`、`NoNewPrivileges`、`RestrictAddressFamilies` 等；**无 `StateDirectory=`** → 单实例汇合点落到 `/tmp`（与 S1-1 直接相关） |
| OpenWrt | procd init | `/etc/init.d/sdautologin`（0755，`pack_openwrt.go:83`）+ `/etc/config/sdautologin`（**0600**，`pack_openwrt.go:79`；`Makefile:107` 用 `$(INSTALL_CONF)`） | **root** | 账号可经 UCI 传入 `--password=`（进 argv，任何用户可读 `/proc/<pid>/cmdline`）；配置目录 `HOME_DIR=/etc/sdautologin` 由程序 `MkdirAll(…, 0o700)` 建立 |
| OpenWrt | `/usr/bin/SDAutoLogin` 包装脚本 | `/usr/lib/sdautologin/SDAutoLogin.gz`（0644）→ `/tmp/sdautologin/SDAutoLogin-<version>`（0755） | **root**（init 脚本调起） | **无属主/权限校验** → S1-2；`/tmp` 挂载模式无法在本机验证（无 OpenWrt 环境） |
| Debian | 包维护脚本 | `postinst` 创建服务用户、启用并重启单元；`prerm` 停服务；`postrm` 屏蔽/清除 | root | **无 `rm -rf $VAR` 模式**（三个脚本内无任何 `rm`）；无 `chmod/chown` 到过宽权限；未信任 `$1` 之外的输入 |

## 发现

### [S1] Unix 单实例锁位于 /tmp 固定路径，任意本机用户 flock 同一路径即可让别人的程序静默拒绝启动

- 类别：单实例 / 本地拒绝服务（跨用户）
- 位置：`internal/platform/singleinst_unix.go:55-74, 105, 143-159`；`cmd/SDAutoLogin/main.go:110-114`
- 证据：
  ```go
  // singleinst_unix.go:65-66
  instanceDirPath = os.TempDir()
  if dirIsWritable(instanceDirPath) { return }
  // :105-106
  func lockPath() string   { return filepath.Join(instanceDir(), "sdautologin.lock") }
  // :143,152,158
  f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
  if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
      logging.Log.Info("another instance is already running, not starting a second one")
      ...
      return nil, nil, false
  ```
  ```go
  // cmd/SDAutoLogin/main.go:113
  if !platform.AcquireSingleInstance(true) { return }
  ```
- 攻击路径：前置条件＝目标以非 systemd 单元方式启动（用户会话、托盘自启、或按单元注释手工 `sudo -u sdautologin …`）而 `STATE_DIRECTORY` 为空，于是汇合目录是 `/tmp`；攻击者只需一个本机普通账号。
  ```sh
  # 攻击者（任意低权限用户，无需 root）
  flock -n /tmp/sdautologin.lock sleep 1000000   # 或 python3: fcntl.flock(open(p,'a+'), LOCK_EX)
  ```
  受害者随后启动 SDAutoLogin → `Flock` 返回 `EWOULDBLOCK` → 记录 "another instance is already running" → `AcquireSingleInstance` 返回 false → `runGUI` 立即 `return`，**托盘不出现、也不做任何登录检查**。`/tmp` 的 sticky 位（1777）保护攻击者创建的文件：受害者无法删除 `/tmp/sdautologin.lock` 来恢复（`os.Remove` 会 `EPERM`，且代码里根本没有删除逻辑）。
- 影响：可用性。跨用户、静默（只写一条 Debug/Info 日志）、可持续（攻击者常驻进程或自己的 autostart 即可长期占位；无需 root）。机密性/完整性不受影响。
- 修复：把汇合点放到“只有本人可写的目录”而不是共享 `/tmp`——首选 `os.UserCacheDir()/sdautologin`（0600/0700），或 `$XDG_RUNTIME_DIR`（已是 0700）；`os.TempDir()` 只作为最后手段，且取用前校验目录属主等于当前 uid 且权限不含 group/other 的写位（`stmt.Sys().(*syscall.Stat_t).Uid == uint32(os.Getuid()) && stmt.Mode().Perm()&0o022 == 0`）。锁文件名建议加入 uid（如 `sdautologin-<uid>.lock`），使跨用户互不干扰。
- 置信度：高（代码路径与 `main.go` 的提前 return 均为逐字确认；「跨用户」依赖 `/tmp` 共享与 sticky 位，为该平台的既定语义）。**无法验证**：在真实多用户主机上的端到端复现（本机为单用户 WSL、且 Windows 侧无法执行 Linux 二进制）。

### [S1] OpenWrt 包装脚本以 root 身份 exec /tmp 下未校验属主与权限的程序

- 类别：本地提权（root 代码执行）
- 位置：`packaging/openwrt/root/usr/bin/SDAutoLogin:13-35`
- 证据：
  ```sh
  unpacked=/tmp/sdautologin/SDAutoLogin-$version

  if [ ! -x "$unpacked" ]; then
  	rm -f /tmp/sdautologin/SDAutoLogin-*
  	mkdir -p "$(dirname "$unpacked")" || exit 1
  	partial="$unpacked.$$"
  ...
  exec "$unpacked" "$@"
  ```
- 攻击路径：前置条件＝设备上存在任意非 root 账号（OpenWrt 默认只有 root，故此项前置不默认成立；一旦有普通账号或攻击者已取得非 root 代码执行，则是 root 提权）。
  ```sh
  # 攻击者（非 root，任意时刻，只要服务还没跑过、/tmp/sdautologin 尚不存在）
  ln -s /tmp/evildir /tmp/sdautologin          # 或 mkdir /tmp/sdautologin（属主＝攻击者）
  mkdir -p /tmp/evildir
  printf '#!/bin/sh\ncp /bin/sh /tmp/r; chmod 4755 /tmp/r\n' \
      > /tmp/evildir/SDAutoLogin-1.3.0-alpha.2   # 版本号公开可知（Makefile:24）
  chmod 755 /tmp/evildir/SDAutoLogin-1.3.0-alpha.2
  ```
  随后管理员安装/升级软件包（`postinst` 立即 `start`）、或设备重启后 procd 拉起服务 → root 执行 `[ ! -x "$unpacked" ]` 判定为“已解包” → `exec "$unpacked" "$@"` 以 root 运行攻击者的脚本。
  三个叠加缺陷：① `/tmp` 是全局可写的 tmpfs（`mode=1777` —— **无法验证**：本机无 OpenWrt 环境，无法读其挂载参数）；② `mkdir -p` 对**已存在的符号链接指向的目录**不报错，因此路径可被重定向；③ 最终检查只有 `-x`（可执行位），不校验属主/权限/是否由本次 gunzip 产出。
- 影响：完整性/机密性——root 代码执行（可持久化：改 init 脚本、写 /usr）。需要本机非 root 账号；若设备无普通账号则不可利用。
- 修复：解包目录由包安装时以 root 建立并 `chmod 0700`（或在脚本里用 `mkdir -m 0700` 并对已存在目录校验 `[ "$(stat -c %u:%g /tmp/sdautologin)" = "0:0" ]` 且拒绝符号链接 `[ -L /tmp/sdautologin ] && exit 1`）；`gunzip | mv` 完成后用 `chown root:root` + `chmod 0755` 落到目标名，且始终 `exec` 一个“本次由自己创建并 `mv` 到位”的名字（例如把 `$partial` 交给 `exec`，或先 `rm -f "$unpacked"` 再 `mv`）。
- 置信度：高（脚本逐字确认）。**无法验证**：`/tmp` 的挂载模式（1777 是 OpenWrt 常见默认，但我无法在本机确认）。

### [S2] Unix 设置页套接字无对端身份校验，且权限完全由 umask 决定、名字可被抢占

- 类别：单实例 / 本机 UI 劫持
- 位置：`internal/platform/singleinst_unix.go:105-106, 161-186, 195-222, 226-256`
- 证据：
  ```go
  listener, err := net.Listen("unix", path)      // :174 权限由 bind(2) 决定 = 0777 &^ umask
  ...
  func readSettingsRequest(conn net.Conn) bool { // :250
  	_ = conn.SetReadDeadline(time.Now().Add(settingsRequestTimeout))
  	buf := make([]byte, 1)
  	n, _ := conn.Read(buf)
  	return n > 0                               // 只看到 1 个字节就调用 f()，不校验对端 UID/PID
  }
  ```
  标准库证据（WSL `go1.26.0` 源码）：`/usr/lib/go-1.26/src/net/unixsock_posix.go:16-50` 创建 socket 后直接 `bind`，整个 `net` 包内**没有任何 `chmod`/`umask` 处理**（`grep -rn -i umask /usr/lib/go-1.26/src/net/` 无匹配）→ 套接字模式就是 `0777 &^ umask`。
- 攻击路径：
  ```sh
  # a) 任何能连接该套接字的进程（同用户；或 umask 允许 0002/0000 时的同机其他用户）
  python3 -c 'import socket,time
  while True:
      s=socket.socket(socket.AF_UNIX); s.connect("/tmp/sdautologin.sock"); s.send(b"\x01"); s.close(); time.sleep(0.2)'
  # b) 抢占名字，使合法通道永久失效（受害者不再能通过第二次启动打开设置页）
  python3 -c 'import socket,time
  s=socket.socket(socket.AF_UNIX); s.bind("/tmp/sdautologin.sock"); s.listen(8); time.sleep(10**6)'
  ```
  对 (b)，`listenSettingsSocket` 的恢复分支（`net.Dial` 探测 → `os.Remove` → 重新 `net.Listen`）给了攻击者一个可竞争的窗口：只要在 `os.Remove` 与第二次 `bind` 之间重新 `bind`，胜利者就永久失去套接字（代码随后只记录一条 Warn 并继续运行）。
- 影响：可用性/完整性（UI 层）：受害者桌面被强制打开设置页（页面内容仍是本机回环页面，攻击者拿不到内容）；受害者失去“第二次启动即打开设置页”的能力。**跨用户可达性取决于 umask**：systemd 两个单元都设 `UMask=0077`（→0700，其他用户不可写），而手工从 shell 启动时套接字为 `0777&^会话umask`（022/002 → 0755/0775，其他用户通常无写位）；`0777 &^ umask` 且 umask=0 或 0002 且组含他人时其他用户即可连接。
- 修复：`net.ListenUnix` 后用 `os.Chmod(path, 0o600)`（或 `ListenConfig` + `syscall.Umask(0o177)` 包住 bind）；`Accept` 后用 `conn.(*net.UnixConn).SyscallConn()` + `syscall.GetsockoptUcred(fd, SOL_SOCKET, SO_PEERCRED)` 校验 `Ucred.Uid == os.Getuid()`，不匹配则丢弃；`os.Remove(path)` 前校验 `Lstat` 是套接字且属主是自己。
- 置信度：高（代码与标准库行为均逐字确认）。**无法验证**：真实 umask 下的套接字模式端到端观测（我未运行二进制；`python3` 探针因最终平台差异未执行）。

### [S2] Windows 命名对象可被同会话低完整性进程预创建（互斥体抢占 + 事件伪造）

- 类别：单实例 / 本机拒绝服务
- 位置：`internal/platform/singleinst_windows.go:15-16, 48, 54-65, 74-81, 85-99`
- 证据：
  ```go
  instanceMutexName     = `Local\SDAutoLogin`
  showSettingsEventName = `Local\SDAutoLoginShowSettings`
  ...
  h, _, lastErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(UTF16Ptr(instanceMutexName))))
  if errno, ok := lastErr.(syscall.Errno); ok && uint32(errno) == errorAlreadyExists {
      logging.Log.Info("another instance is already running, not starting a second one")
  ```
  `ERROR_ALREADY_EXISTS` 的判断**存在**（这一点是正确的），但名字固定可预测、`CreateMutexW` 未传安全描述符、得到句柄后也不校验对象属主。
- 攻击路径（同一交互会话内，例如 AppContainer/低完整性进程、或该用户自己的任意进程）：
  ```powershell
  # a) 抢占互斥体 → 受害者（同会话用户）的 SDAutoLogin 永远认为"已在运行"并立即退出
  $m = [System.Threading.Mutex]::new($false,'Local\SDAutoLogin'); Start-Sleep -Seconds 86400
  # b) 伪造"请求打开设置页"事件 → 反复在其桌面弹出设置窗口
  $e = [System.Threading.EventWaitHandle]::new($false,'ManualReset','Local\SDAutoLoginShowSettings')
  while ($true) { $e.Set(); Start-Sleep -Milliseconds 300 }
  ```
  `Local\` 是会话命名空间，因此另一登录会话（fast user switching）里的用户不可达 —— 前置被限定为“同会话/同用户（或该用户的低完整性进程）”。
- 影响：可用性——程序静默不启动（自动登录失效）或被反复弹窗骚扰。无提权（受害者进程本身也是该用户的普通完整性），但构成“低完整性→中完整性”的对象抢占边界。
- 修复：名字中加入不可预测成分（如 `Local\SDAutoLogin-<用户SID或随机串>`，并把名字写入本人 0600 的状态文件供第二次启动读取）；创建后核对既有对象的属主（`GetSecurityInfo`/`OpenMutex` + 比较 SID）；对 `showSettingsEvent` 同样处理，并且只在“本用户 SID 相同”时才 `SetEvent`。
- 置信度：中（静态阅读：Windows 侧无法在本机执行；`Local\` 的会话作用域与 `.NET` 名称映射语义为 Windows 既定行为，但未在 Windows 上实测）。

### [S2] 设置页一次性 token 经子进程 argv 传递，同机其他用户可从 /proc 读取并抢用

- 类别：隐私 / 单实例（跨用户凭据泄漏，竞态）
- 位置：`internal/platform/openurl_other.go:16-21`、`internal/platform/appwindow.go:43-48`、`internal/platform/openurl_windows.go:15`
- 证据：
  ```go
  cmd = exec.Command("xdg-open", target)                       // openurl_other.go:20
  ...
  return "open", []string{"-na", browser, "--args", "--app=" + target}   // appwindow.go:45
  ```
  其中 `target` 由 `internal/app/settings.go:212` 生成：`fmt.Sprintf("http://%s:%s/?token=%s", host, port, token)`；服务端**没有任何对端身份校验**（`grep RemoteAddr|loopback` 在 `internal/app/settings.go` 只命中 `listener` 地址判断与 `hostForURL`，无请求来源校验），token 保护的是“回到回环端口”的访问，而回环端口对同机所有用户开放。
- 攻击路径：
  ```sh
  # 攻击者（同机其他用户）轮询进程表，抓取带 token 的 URL
  while :; do for p in /proc/[0-9]*/cmdline; do tr '\0' ' ' < "$p" 2>/dev/null; echo; done \
     | grep -o 'http://127.0.0.1:[0-9]*/?token=[A-Za-z0-9_-]*'; done
  # 抢在浏览器取用之前兑换（页面明文含账号；可读写配置——见 E/F 号模块）
  curl -sS "http://127.0.0.1:<port>/?token=<stolen>"
  ```
  端口可从被抓到的 URL 或 `/proc/net/tcp`（世界可读）得到。token 的生命周期终止于浏览器首次取用，因此这是一个竞态；但攻击者可高频轮询，且浏览器未安装/启动失败时 URL 会一直有效直到下一次 `open()`（“every opening gets an address of its own: this spends the one handed out last time”）。
- 影响：机密性/完整性（跨用户）：可读取他人配置页面（含明文账号，见 E/F 模块对页面内容的结论），并可通过页面写入配置。仅需本机其他用户账号。
- 修复：不要用命令行参数传 token——改为把 URL 交给浏览器时**同时绑定来源**（例如生成一个只写入 0600 文件的“短链”，或在页面侧校验 cookie 来自同一 UID 的交换）；最小改动是缩短 token 有效期（页面加载失败即作废）并在服务端拒绝非本用户来源连接。实务上更彻底的做法是用 Unix 域套接字 + `SO_PEERCRED` 承载设置页。
- 置信度：中（`target` 进入 argv 与 `/proc/<pid>/cmdline` 的世界可读性确定；“竞态能否稳定取胜”未验证；页面内容与写权限属 E/F 模块）。

### [S3] systemd 单元缺少 CapabilityBoundingSet / SystemCallFilter / ProtectProc 等加固项，且文档示例把密码放上命令行

- 类别：本地提权（加固缺口）/ 隐私
- 位置：`packaging/systemd/system/sdautologin.service:36-73`、`packaging/systemd/user/sdautologin.service:35-55`；示例命令见 `system/sdautologin.service:6-7`、`user/sdautologin.service:12-13`
- 证据：
  ```
  6	#   sudo -u sdautologin /usr/bin/SDAutoLogin --home=/var/lib/sdautologin \
  7	#        --username=s1234567 --password=SECRET --isp=@zgyd --save config --show
  ...
  56	UMask=0077
  57	NoNewPrivileges=yes
  ```
  （`[Service]` 段落中没有 `CapabilityBoundingSet=`、`SystemCallFilter=`、`ProtectProc=`、`PrivateUsers=`、`ProtectClock=`、`ProtectHostname=`。）
- 攻击路径：非提权链，属纵深防御缺口。任何未来获得该服务上下文代码执行的攻击面（例如 S1-2 的 /tmp 执行、或配置解析缺陷）都少了一层 capability 与系统调用限制；`--password=SECRET` 的示例若被照抄，密码会出现在进程表与 `systemctl show sdautologin -p ExecStart`（对系统单元，普通用户可读）中。
- 影响：完整性/机密性——降低既有漏洞的可利用性与爆炸半径（S3 加固）；示例形式泄漏账号（隐私，多用户主机上可被同机用户读到）。
- 修复：加 `CapabilityBoundingSet=`（空）、`AmbientCapabilities=`（空）、`SystemCallFilter=@system-service`、`ProtectProc=invisible`、`ProcSubset=pid`；把示例改为只演示 `LoadCredential=password:/etc/sdautologin/password`（单元注释 :15-20 已给出该形式），删除 `--password=SECRET` 的示例。
- 置信度：高（单元逐行阅读；`systemctl show` 的可见性为 systemd 既定行为，未在本机验证）。

### [S3] 锁文件以 O_CREATE|O_RDWR 打开（无 O_EXCL/O_NOFOLLOW），且打不开时 fail-open

- 类别：单实例 / 符号链接
- 位置：`internal/platform/singleinst_unix.go:143-151`
- 证据：
  ```go
  f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
  if err != nil {
      logging.Log.Warn("could not open the lock file %s: %v; this run cannot tell whether another instance is already running", lockPath, err)
      return nil, nil, true
  ```
- 攻击路径：① 攻击者在 `/tmp/sdautologin.lock` 放置符号链接指向受害者可打开的文件（例如 `~/.bashrc`）→ 受害者以自身身份打开并 `flock` 该文件（内容是受害者自己的文件，不会被写入或截断，因此不是任意写）；若该文件恰是另一个程序的锁文件，可造成那个程序异常。② 攻击者把 `sdautologin.lock` 造成普通文件并设为 `0000`（属主＝攻击者）→ 受害者 `OpenFile` 失败 → 代码 `return nil, nil, true`：**继续启动且不做单实例判定**，于是同一用户可以跑起多份实例（每个都监听失败、都连不上真正的汇合点）。
- 影响：可用性/完整性——(1) 由于只有 `flock` 没有 `O_NOFOLLOW`，可信进程会被引导去打开攻击者选择的路径；(2) fail-open 使“单实例”在攻击者可控条件下退化为“无单实例”，多实例并发写同一个 config/log（写入方是本人，故不越权）。
- 修复：`os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)`，`OpenFile` 成功后用 `f.Stat()` 确认 `mode.IsRegular() && Uid==getuid()`，失败则视为“不可判定”并按 fail-closed（拒绝启动）或换用 `$XDG_RUNTIME_DIR`。
- 置信度：高（代码逐字确认；影响面受“攻击者只影响他人进程的行为、不写入数据”限制）。

### [S3] `.desktop` 的 Exec= 与 Windows Run 值只处理空格，不转义 `"`，也不过滤 `%` 字段码

- 类别：自启动持久化
- 位置：`internal/platform/exec.go:30-49`、`internal/platform/autostart_other.go:80-88`、`internal/platform/autostart_windows.go:35-36`
- 证据：
  ```go
  out := executablePath()
  for _, arg := range autostartArguments {
      out += " " + quoteArgument(arg)
  }
  ...
  func quoteArgument(arg string) string {
      if strings.ContainsAny(arg, " \t") { return `"` + arg + `"` }
      return arg
  }
  ```
  ```go
  "Exec=" + autostartCommand() + "\n"      // autostart_other.go:85
  ```
- 攻击路径：`autostartArguments` 只含 `--home=<dir>`（`main.go:26-31` 注释所述），其值来自用户自己的 CLI 或默认数据目录。若该值含 `"`（合法目录名，如 `A "quoted" dir`），生成的 `Exec="…" --home="/home/u/A "quoted" dir"` 会被 freedesktop 解析器切成不同参数；若含 `%`，`Exec` 值中的字段码会被启动器展开（`%i` 会插入 `--icon <name>` 两个参数、`%k` 展开为 desktop 文件路径、`%u/%U` 展开为 URL），从而把 `--home=` 变成别的值或注入额外选项；Windows Run 值同理（引号未转义 → 命令行被重解析，可能指向不同程序）。
- 影响：完整性/可用性（自启动项静默失效或启动到非预期参数）。执行身份仍是写入者本人，**不构成跨用户提权**；但若管理员以含特殊字符的 `--home` 为系统单元准备自启动项，则错误位置会更难察觉。
- 修复：`quoteArgument` 补 `"` 与 `\` 的转义（按 freedesktop 规范转义 `"`、反引号、`$`、`\`），并对 `%` 做 `%%` 转义（desktop 规范中 `%%` 表示字面 `%`）；Windows 侧用单一“整条命令行”字符串时按 `CreateProcess` 规则转义引号。
- 置信度：高（代码逐字确认；freedesktop 字段码与桌面实现行为为规范既定，未在本机端到端验证）。

### [S3] 所有外部程序一律按 PATH 解析（reg / stty / xdg-open / notify-send / osascript / 浏览器名 / 包装脚本工具）

- 类别：PATH 劫持
- 位置：`autostart_windows.go:19,35,38`、`password_other.go:49`、`openurl_other.go:16,18,20`、`notify_other.go:20`、`appwindow.go:107`、`packaging/openwrt/root/usr/bin/SDAutoLogin:21,24,26,31,32`
- 证据：
  ```go
  cmd := exec.Command("reg", "query", winRunKey, "/v", winRunValue)     // autostart_windows.go:19
  cmd := exec.Command("stty", arg)                                     // password_other.go:49
  cmd = exec.Command("xdg-open", target)                               // openurl_other.go:20
  ```
- 攻击路径：若 `$PATH`（或 Windows 的当前目录/搜索顺序）中先命中攻击者放置的同名程序，该程序将以用户身份执行——其中 `reg` 被调用时**会写入 HKCU Run 持久化项**，`stty` 会拿到终端 stdin。前置＝攻击者已能影响该用户的 PATH/环境（或包装脚本的 PATH，root 身份时更严重：`gunzip`/`chmod`/`mv`/`dirname`/`rm` 均来自 PATH）。这本身不是权限边界跨越（能改 PATH 者本就能以该用户身份执行代码），但它是“最后一个防线”，且 OpenWrt 的 root 包装脚本使其成为提权链的一环。
- 影响：完整性（纵深防御）：一旦 PATH 或工具目录被污染，程序会代为执行攻击者程序并（Windows）写入持久化项。
- 修复：改用绝对路径或在启动时确定一次并校验属主/权限（Windows：`%SystemRoot%\System32\reg.exe`；Linux：`/usr/bin/notify-send`、`/usr/bin/xdg-open`、`/bin/stty`；OpenWrt 包装脚本：`/bin/gunzip`、`/bin/chmod`、`/bin/mv`、`/bin/rm`、`/usr/bin/dirname`）。找不到绝对路径再按名字回落也可以，但应记录。
- 置信度：高（调用点逐字确认）。**无法验证**：各发行版/OpenWrt 上这些工具的绝对路径与 busybox 链接（未在本机检查目标系统文件树）。

### [S3] 通知与提示文本来自门户明文 HTTP 响应，未净化即送入系统通知 API

- 类别：隐私 / 欺骗（网络输入）
- 位置：`internal/tray/tray_unix.go:250`、`internal/tray/tray_windows.go:302`、`internal/platform/notify_other.go:15-26`、`internal/platform/notify.go:8-20`
- 证据：
  ```go
  platform.Notify(i18n.L("app.name"), i18n.L("balloon.reconnectFailed")+": "+s.Message)   // tray_unix.go:250
  a.notify(i18n.L("app.name"), i18n.L("balloon.reconnectFailed")+": "+s.Message)          // tray_windows.go:302
  ```
  `s.Message` 的来源（跨模块，见 E/F）：`internal/portal/portal.go:246-247` → `if msg := unescapeJSON(firstMatch(reMsg, body)); msg != "" { result.Message = Truncate(msg) }`，即**门户响应体里的文本被逐字回显**；而登录请求走明文 HTTP（`c.get(c.plain, …)`，`portal.go:224`）。
- 攻击路径：在校园网路径上做中间人（或控制/劫持门户）→ 让登录响应携带任意 `message` 文本 → 该文本经 `Snapshot.Message` 进入：Linux `notify-send` 的第二个 argv、macOS `osascript -e 'display notification "<body>" …'`、Windows 气泡提示（`SzInfo`/`SzTip`）。攻击者可写入任意可见文本（含换行、全角字符）用于钓鱼（如“请打开 http://…”）。我逐条验证了注入面：`notify-send` 的 body 恒有固定前缀（`"…: "`），**无法**让 body 以 `-` 开头去劫持 `notify-send` 选项；AppleScript 字面量对 `"` 与 `\` 已转义（`notify.go:24-36`），**无法**闭合字符串注入代码。
- 影响：完整性/隐私——桌面通知内容被网络攻击者控制（欺骗、社工），非代码执行。附带可用性：AppleScript 字符串字面量不能跨行，正文含换行会使 `osascript` 报语法错误，通知静默丢失。
- 修复：对进通知的文本做白名单化/单行化（去控制字符、换行折叠为空格、限制长度并按 UTF-8 边界截断）；在页面/日志/通知三处统一使用同一个“安全单行”函数；更根本的是把门户交互改为 HTTPS 或对响应文本做严格模式校验（该项属 E/F）。
- 置信度：高（数据流逐字确认）。**无法验证**：真实桌面上 DBus/Windows 气泡的渲染效果。

### [S3] 设置页可监听 0.0.0.0 且 token 化地址写入日志/journal

- 类别：隐私 / 架构
- 位置：`internal/app/settings.go:164-165, 237-246`（跨模块，见 E/F）；本模块相关：`packaging/systemd/system/sdautologin.service:9-13`（注释指示用 `--settings=127.0.0.1:8088`）、`internal/platform/openurl_other.go:16-21`
- 证据：
  ```go
  if host, _, err := net.SplitHostPort(listener.Addr().String()); err == nil && !isLoopback(host) {
      logging.Log.Warn("the settings page is reachable from the network on %s; the token in its address is the only protection, so keep it on a trusted network or behind a tunnel", listener.Addr())
  ```
  单元注释同时说明日志里会打印带 token 的地址（`the address that "journalctl -u sdautologin" prints`），而 `logging.Log.Info("settings page at %s", url)` 会把地址写进日志文件（0600）与 journal。
- 攻击路径：管理员按注释在系统单元里加 `--settings=0.0.0.0:8088` → 页面在全网可达，唯一保护是 URL 中的 token；同时 token 地址被写入 journal，任何能读该单元日志的用户（`systemd-journal`/`adm` 组）可取得地址。若管理员为方便把页面地址也贴到某处或被日志采集系统收集，token 即外泄（页面明文含账号，见 E/F）。
- 影响：机密性——在“日志可被同机其他用户读取”或“0.0.0.0 且网络可信假设失败”时，账号配置可被读取/修改。
- 修复：默认强制回环（对非回环绑定要求显式二次确认选项）；token 化地址只打印到 stdout 而不写持久日志（或打码 token，仅显示 port）；文档默认示例改为 `127.0.0.1` + `LoadCredential`。
- 置信度：中（`--settings` 的取值路径与日志内容为跨模块结论，见 E/F；本模块只确认了 URL 会被交给系统外部程序与日志）。

### [S4] 多处 Win32 返回值未检查

- 类别：原生API
- 位置：`internal/tray/tray_windows.go:288, 316, 385, 391, 384`；`internal/tray/win32_windows.go:133-136, 145-147`
- 证据：
  ```go
  procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&a.nid)))   // :288 与 :316，返回值丢弃
  h, _, _ := procGetModuleHandleW.Call(0)                                // win32_windows.go:134
  procSetForegroundWindow.Call(uintptr(a.hwnd))                          // :385
  ```
  对比：`nimAdd`（:243）、`RegisterClassExW`（:209）、`CreateWindowExW`（:214）、`CreateDIBSection`/`CreateBitmap`/`CreateIconIndirect`（`icon_windows.go:40,52,61`）**都有**检查。
- 攻击路径：无直接攻击路径；后果是失败时静默——气泡提示不出现（`nimModify` 失败）、托盘窗口类注册得到 0 句柄导致后续 `CreateWindowExW` 一并失败、菜单定位失败等，都只表现为“功能不工作”，排查困难。
- 影响：可用性/可观测性（无内存安全问题：`&a.nid` 与 `msg` 均为 Go 侧固定布局结构，已在 `win32_windows_test.go` 校验尺寸且指针在调用期间存活）。
- 修复：对 `nimModify`/`GetModuleHandleW`/`SetForegroundWindow`/`GetCursorPos` 记录一次 Warn（可限速），与既有 `nimAdd` 的处理保持一致。
- 置信度：高（逐字确认）。**无法验证**：这些调用在真实 Windows 会话中的失败频率（未在 Windows 运行）。

### [S4] `putUTF16` 的截断可能切断 UTF-16 代理对

- 类别：原生API（显示层）
- 位置：`internal/tray/utf16.go:11-20`；调用点 `internal/tray/tray_windows.go:242, 287, 313-315`
- 证据：
  ```go
  u := utf16.Encode([]rune(s))
  if len(u) > len(dst)-1 { u = u[:len(dst)-1] }     // 可能在代理对中间切开
  copy(dst, u)
  ```
- 攻击路径：`SzTip`(128)/`SzInfo`(256)/`SzInfoTitle`(64) 恰好落在代理对之间时，尾部留下孤立代理，Windows 显示为替换字符。`tray_unix.go` 的 macOS/Linux 侧不走这条路径。该项目已在 `utf16_test.go:57-70` 覆盖“完整字符存活”，但未覆盖“需要截断且恰好切在代理对中间”的情形。
- 影响：显示层（无内存越界、无注入）。触发者可以是网络（门户文本长度可控）。
- 修复：截断后回退一个单位，若末位是高位代理（`0xD800-0xDBFF`）则再截一位。
- 置信度：高（代码逐字确认；Win32 对孤立代理的渲染为既定行为）。

## 从未被测试执行的平台文件清单

先给出**实测证据**（WSL，`go1.26.0`）：

- `go test -list '.*' ./internal/platform ./internal/tray` 在本机（linux）列出的测试共 33 个；其中**不含** `internal/platform/win32_windows_test.go`（2 个）与 `internal/tray/win32_windows_test.go`（3 个）的任何测试名 → 这 5 个测试在 Linux CI 上永远不执行。本机实际执行的测试名（`go test -list` 输出）：
  `internal/platform`：`TestAppWindowCommandAsksTheBrowserForAWindowOfItsOwn`、`TestFindAppBrowserTakesTheFirstOneThatIsThere`、`TestAppBrowserCandidatesFollowTheSystem`、`TestTheAutostartCommandIsTheExecutableAndTheArguments`、`TestAnArgumentIsQuotedOnlyWhereItNeedsToBe`、`TestNotifyCommandFollowsTheSystem`、`TestAppleScriptStringQuotesWhatWouldEndTheLiteral`、`TestTheDirectoryProbeTellsWhatCanBeWritten`、`TestTheLosingCopyDoesNotBindTheSocket`、`TestTheLosingCopyLeavesTheWinnersSocketAlone`、`TestTheRequestWaitsForASocketThatIsNotThereYet`、`TestASecondCopyCanAskTheRunningOneForItsPage`、`TestListenSettingsSocketKeepsALiveOneAndReplacesADeadOne`、`TestTheStateDirectoryWinsOverTheTemporaryOne`、`TestNoStateDirectoryFallsBackToTheTemporaryOne`、`TestTheStateDirectoryTakesTheFirstOneItCanWrite`
  `internal/tray`：`TestAnOutageIsAnnouncedOnceAndThenRemindedEveryFewMinutes`、`TestTheSessionComingBackEndsTheOutage`、`TestStatesOutsideTheOutageAreNotFailures`、`TestTheTooltipNamesAReleaseWorthMovingTo`、`TestTheFirstClickOpensTheSettingsPage`、`TestTheSecondClickOfADoubleClickIsRefused`、`TestAClickAfterTheWindowOpensThePageAgain`、`TestARefusedClickDoesNotMoveTheWindow`、`TestAnIntervalTheSystemCouldNotReportStillDebounces`、`TestTheHeaderIsTheSizeTheOSReadsFirst`、`TestTheHeaderDescribesATopDown32bppBitmap`、`TestThePixelsAreStoredAsBGRAFromTheTopDown`、`TestTheAndMaskIsAnEmptyBitmapOfPaddedRows`、`TestTheFixedBufferIsClearedAndTerminated`、`TestAnEmptyTooltipClearsTheField`、`TestALongerTooltipIsCutToWhatTheFieldHolds`、`TestACharacterOutsideTheBasicPlaneSurvivesWhole`
- `GOOS=darwin CGO_ENABLED=0 go vet ./internal/tray` **失败**（`vendor/fyne.io/systray/systray.go:92:2: undefined: setInternalLoop` 等，因 darwin 后端需要 cgo）→ **macOS 的托盘代码在任何 Linux/Windows CI 上都无法编译**。同一命令对 `./internal/platform` **通过**（darwin 的 `autostart_other.go` 可交叉编译）。
- CI 实际执行（`.gitea/workflows/ci.yml:77-78` → `go run ./tools/build test`）：`tools/build/main.go:370` `go vet ./...`（本平台）、`:379` `CGO_ENABLED=0 GOOS=windows go vet ./...`、`:385` `go test -race ./...`。**没有 darwin 步骤，也没有任何“运行测试”的 Windows 步骤** —— 即 Windows 专用代码只会被**类型检查**（编译通过），永不运行；macOS 专用代码连类型检查都没有。

### A. 任何平台都从未被编译过的文件

| 文件 | 构建标签 | 原因 |
|---|---|---|
| `internal/platform/singleinst_other.go` | `!windows && !linux && !darwin` | CI 只 vet `linux` 与 `windows`；darwin 也未覆盖 → 该文件在本仓库任何自动化步骤中都不参与编译 |
| `internal/tray/bus_darwin.go` | `darwin && !notray` | darwin+cgo 无法在 Linux 宿主编译（已实测报错）；`GOOS=darwin` 也不在 CI 中 |
| `internal/tray/tray_unix.go` 的 darwin 语义 | `(linux \|\| darwin) && !notray` | 文件在 linux 下会编译，但 darwin 语义（systray 的 macOS 后端、`open -na` 路径）从不被编译/执行 |

### B. 编译过（CI 会 vet）但从未被任何测试**执行**的文件

| 文件 | 说明 |
|---|---|
| `internal/platform/singleinst_windows.go` | **连测试文件都没有**（无 `singleinst_windows_test.go`）。互斥体/事件抢占（S2）、`ERROR_ALREADY_EXISTS` 分支、`WaitForSettingsRequest` 循环全部未被执行 |
| `internal/platform/win32_windows.go` | `UTF16Ptr` 仅由 `win32_windows_test.go`（windows 标签）覆盖 → 在 Linux CI 上从不执行 |
| `internal/platform/win32_windows_test.go` | windows 标签；`go test -list` 实测在 Linux 不出现 |
| `internal/platform/openurl_windows.go` | 无测试；`ShellExecuteW` 调用从未执行 |
| `internal/platform/notify_windows.go` | 空实现，无测试 |
| `internal/platform/autostart_windows.go` | 无测试；`reg` 的 `query/add/delete` 三条外部进程调用从未执行 |
| `internal/tray/tray_windows.go` | 无测试；493 行的窗口过程、菜单、托盘图标、通知、设置页请求复用全部未执行 |
| `internal/tray/win32_windows.go` | 结构体布局/常量由 `win32_windows_test.go` 覆盖，但该测试在 Linux 不执行；`LoadCursorW`/`MessageBoxW` 调用从未执行 |
| `internal/tray/icon_windows.go` | 无测试；`makeIcon` 的 GDI 路径（`CreateDIBSection`/`CreateBitmap`/`CreateIconIndirect`）从未执行（`dib.go` 的纯函数被 `dib_test.go` 覆盖，但那不是 GDI 路径） |
| `internal/tray/win32_windows_test.go` | windows 标签；实测在 Linux 不出现 |
| `cmd/SDAutoLogin/console_windows.go`、`console_other.go`、`password_windows.go`、`password_other.go` | 本模块内**无任何测试**引用 `attachConsole`/`echoOff`/`stty`（grep 确认只出现在自身文件与 `main.go:42`/`password.go:23` 的调用处）；Windows 侧 `GetConsoleMode`/`SetConsoleMode` 从未执行，Linux 侧 `stty -echo` 分支（含信号恢复协程）从未执行 |
| `internal/platform/openurl_other.go`、`notify_other.go`、`autostart_other.go`、`singleinst_unix.go` 的**带副作用入口** | 这些文件在 linux 会编译，`notify_test.go`/`autostart_test.go`/`singleinst_unix_test.go` 只覆盖纯函数（`notifyCommand`/`appleScriptString`/`autostartCommand`/`quoteArgument`/`acquireInstance`/`listenSettingsSocket`/`stateDirectory`/`dirIsWritable`）。真正的 `Notify`、`OpenExternal`、`SetAutostart`（写 .desktop/plist 的路径）、`AutostartEnabled`、`AcquireSingleInstance` 入口从未被测试调用 |
| `internal/tray/bus_linux.go` | `sessionBusError` 从未被测试调用（仅由 `tray_unix.go:28` 在生产路径调用） |

### C. `t.Skip` 评估

本范围内只有一处跳过：`internal/platform/autostart_test.go:17` `t.Skipf("this program cannot name its own executable: %v", err)` —— 只在 `os.Executable()` 失败时跳过，属于合理防御，**未掩盖真实缺口**。范围内没有 `testing.Short()` 分支，也没有任何测试用 skip 隐藏平台缺口。真正的缺口不是 skip，而是**构建标签使整个文件不参与编译/不参与执行**（A、B 两节）。

### D. 结论

「Windows 专用代码在 Linux CI 上从未被编译验证过」这一常见假设**在本仓库不成立**：`tools/build/main.go:379` 的 `GOOS=windows go vet ./...` 覆盖了所有 windows 标签文件（含 `_test.go`）的类型检查——我在 WSL 复现该命令并通过。真实缺口是三层：

1. **执行层**：所有 windows 标签测试在本机不出现（实测），Windows 行为零运行时验证；
2. **macOS 层**：darwin 托盘代码连编译都没有（实测报错），无 darwin 步骤；
3. **平台不可达文件**：`singleinst_other.go` 在任何自动化步骤中都不编译。

## 架构与设计层面问题

1. **进程间汇合点用“全局可预测的文件系统路径 + 命名内核对象”承载，而不是“用户私有目录 + 对端身份绑定”。** Unix 侧是 `/tmp/sdautologin.{lock,sock}`（`singleinst_unix.go:65,105-106`），Windows 侧是 `Local\SDAutoLogin{,ShowSettings}`（`singleinst_windows.go:15-16`）。两处都没有“这个名字现在属于谁”的校验，于是“抢占”在两种平台上都成立，只是权限边界不同（Unix 是机器级 `/tmp`，跨用户；Windows 是会话级 `Local\`，跨完整性）。设计上应把“身份”作为汇合协议的一部分：Unix 用 `SO_PEERCRED` + 本人 0700 目录，Windows 用随机名 + 对象属主比对。
2. **“拒绝启动”是单实例失败的唯一处理方式，且失败原因只进日志。** `main.go:113-114` 的 `return` 让整机上的另一个用户（或同会话的低完整性进程）拥有“让目标用户的自动登录永不发生”的开关，且用户界面上没有任何提示。至少应在“锁被他人持有且属主/权限异常”时走一条显式告警路径（日志 + 托盘通知），而不是静默退出。
3. **平台差异只在 build tag 层隔离，没有平台矩阵验证。** CI 只有 linux 宿主：linux 上跑测试 + 两平台 vet。任何“只有 Windows/macOS 才能暴露”的缺陷（Win32 返回值、菜单/消息循环、GDI 图标、LaunchAgent 行为、systray darwin 后端）在本仓库没有任何自动化防线。建议至少加 `GOOS=darwin` 的交叉 vet（托盘部分因 cgo 不可行，则把纯逻辑再抽出一层）以及 Windows runner 上的 `go test`。
4. **网络可控文本直接进入系统 API（通知、提示气泡、tooltip），中间没有净化层。** 当前实现**未**导致命令注入（`notify-send` 的参数前缀与 AppleScript 的转义都逐条验证过），但这依赖“前缀恰好不是 `-`”这种偶然性质；把门户文本当作“内部字符串”使用是一种易碎的设计。
5. **OpenWrt 的“压缩存放 + 运行时解包到 /tmp”把 root 的执行路径放进了全局可写目录。** 这是本次审查中唯一把“低权限可写”与“高权限执行”直接连起来的结构（`/usr/bin/SDAutoLogin` 包装脚本）。若必须保留该优化，目标目录与目标文件的所有权校验是必需项，而不是可选加固。
6. **打包脚本本身质量良好**：Debian 三个维护者脚本无 `rm -rf $VAR` 模式、无过宽 `chmod/chown`；OpenWrt 的 UCI 配置以 0600 安装（`pack_openwrt.go:79`、`Makefile:107`），与“文件比命令行安全”的注释一致；systemd 系统单元已经有相当完整的加固清单。这一点值得在交付说明里保留。

## 覆盖声明

**逐文件逐行读完（范围内）**：

- `internal/platform/`：`singleinst_unix.go`(256)、`singleinst_unix_test.go`(261)、`singleinst_windows.go`(100)、`singleinst_other.go`(10)、`appwindow.go`(118)、`appwindow_test.go`(160)、`autostart_other.go`(115)、`autostart_windows.go`(59)、`autostart_test.go`(56)、`win32_windows.go`(35)、`win32_windows_test.go`(43)、`exec.go`(49)、`notify.go`(36)、`notify_windows.go`(7)、`notify_other.go`(26)、`notify_test.go`(45)、`openurl_windows.go`(23)、`openurl_other.go`(29)
- `internal/tray/`：`tray_windows.go`(493)、`tray_unix.go`(281)、`tray_other.go`(19)、`win32_windows.go`(148)、`win32_windows_test.go`(129)、`icon_windows.go`(79)、`icon.go`(26)、`dib.go`(71)、`dib_test.go`(101)、`utf16.go`(20)、`utf16_test.go`(71)、`labels.go`(22)、`options.go`(13)、`alert.go`(49)、`alert_test.go`(101)、`click.go`(42)、`click_test.go`(79)、`bus_linux.go`(13)、`bus_darwin.go`(8)
- `cmd/SDAutoLogin/`：`console_windows.go`(46)、`console_other.go`(7)、`password_windows.go`(25)、`password_other.go`(52)
- `packaging/`：`systemd/system/sdautologin.service`、`systemd/user/sdautologin.service`、`systemd/sysusers.d/sdautologin.conf`、`openwrt/root/etc/init.d/sdautologin`、`openwrt/root/usr/bin/SDAutoLogin`、`openwrt/root/etc/config/sdautologin`、`openwrt/control/postinst`、`openwrt/control/prerm`、`openwrt/Makefile`、`deb/control/postinst`、`prerm`、`postrm`

**为理解调用关系而读（不属于我，不在此报告内提改动建议）**：`cmd/SDAutoLogin/main.go`（第 26–45、110–114、150–175 行的调用点）、`internal/app/settings.go`（`openExternal`/`OpenURL`/`urlFor`/`hostForURL`/token 与监听地址相关行）、`internal/app/payload.go`（`Message` 字段来源）、`internal/service/service.go`（`snap.Message` 赋值点，含 `:778`/`:811`）、`internal/portal/portal.go:218–277`（`result.Message` 来自门户响应体）、`internal/config/config.go:507,512,525,536` 与 `protect_other.go:27`（权限位，用于核对权限一致性）、`internal/logging/log.go:22-23,64,109`、`tools/build/main.go:56-74,167-385`（CI 实际执行内容）、`tools/build/pack_openwrt.go:60-123`、`tools/build/pack_deb.go:74-114`、`winres/rsrc_windows_amd64.syso:277`（清单 `asInvoker`）、`.gitea/workflows/ci.yml`

**未读**：`vendor/`（仅通过编译报错间接观察 `fyne.io/systray` 的 darwin 后端）、`internal/update`、`internal/i18n`、`internal/brand`、`internal/webui`、其余 `tools/build/*` 细节、`docs/` 与 `dist/`

**工具链**：任务书称无 Go 工具链；实测 WSL 有 `go1.26.0`（`/usr/bin/go`），Windows 侧无。因此我**自己执行**了 `go vet`（linux + `CGO_ENABLED=0 GOOS=windows`）与 `go test`（`./internal/platform ./internal/tray`，全部通过），未做任何其他构建、未产出二进制、未修改任何源码。

**明确无法验证的事项**：

1. Windows 侧一切运行时行为（Win32 返回值、`Shell_NotifyIcon`、GDI、命名对象的实际抢占效果、注册表 ACL 的实际继承）——仅静态阅读；本机源码在 WSL 且无 Windows go 工具链，无法构建运行。
2. macOS 托盘/darwin 代码的任何编译与运行（已实测在 Linux 宿主交叉编译失败）。
3. `/tmp` 的挂载模式与 sticky 位（无 OpenWrt 环境）、OpenWrt 实机上包装脚本的攻击链端到端。
4. 真实 umask 下 Unix 套接字的最终模式（我从 Go 1.26 标准库源码确认 `net` 包不做 `chmod`/`umask`，因此模式＝`bind(2)` 的 `0777 &^ umask`，但未运行程序实测）。
5. 设置页 token 竞态窃取的实战成功率（页面内容与写权限属 E/F 模块）。
6. systemd `systemctl show -p ExecStart` 对普通用户的可读性、`MemoryDenyWriteExecute` 与 Go 运行时的实际兼容性（未运行）。

## 附录：关键代码摘录

### 1) Unix 单实例：目录选择、锁与套接字

```go
// internal/platform/singleinst_unix.go:61-73
if state := stateDirectory(); state != "" {
	instanceDirPath = state
	return
}
instanceDirPath = os.TempDir()
if dirIsWritable(instanceDirPath) {
	return
}
if cache, err := os.UserCacheDir(); err == nil && dirIsWritable(cache) {
	instanceDirPath = cache
}
```
```go
// internal/platform/singleinst_unix.go:174-185
listener, err := net.Listen("unix", path)
if err == nil {
	return listener, nil
}
if conn, derr := net.Dial("unix", path); derr == nil {
	_ = conn.Close()
	return nil, err
}
if rerr := os.Remove(path); rerr != nil {
	return nil, err
}
return net.Listen("unix", path)
```

### 2) Windows 命名对象

```go
// internal/platform/singleinst_windows.go:15-16
	instanceMutexName     = `Local\SDAutoLogin`
	showSettingsEventName = `Local\SDAutoLoginShowSettings`
// :48,54,59-65
	if ev, _, err := procCreateEventW.Call(0, 1, 0, uintptr(unsafe.Pointer(UTF16Ptr(showSettingsEventName)))); ev != 0 { … }
	h, _, lastErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(UTF16Ptr(instanceMutexName))))
	if errno, ok := lastErr.(syscall.Errno); ok && uint32(errno) == errorAlreadyExists {
```

### 3) 自启动命令构造

```go
// internal/platform/exec.go:11-17, 30-36, 44-49
func executablePath() string {
	exe, err := os.Executable()
	if err != nil || exe == "" { return "" }
	return `"` + exe + `"`
}
func autostartCommand() string {
	out := executablePath()
	for _, arg := range autostartArguments { out += " " + quoteArgument(arg) }
	return out
}
func quoteArgument(arg string) string {
	if strings.ContainsAny(arg, " \t") { return `"` + arg + `"` }
	return arg
}
```
```go
// internal/platform/autostart_other.go:80-88
func linuxDesktopEntry() string {
	return "[Desktop Entry]\n" + "Type=Application\n" + "Name=SDAutoLogin\n" +
		"Comment=Campus network auto login\n" + "Exec=" + autostartCommand() + "\n" + …
```

### 4) OpenWrt 包装脚本（root）

```sh
# packaging/openwrt/root/usr/bin/SDAutoLogin:13-35
version=@VERSION@
archive=/usr/lib/sdautologin/SDAutoLogin.gz
unpacked=/tmp/sdautologin/SDAutoLogin-$version
if [ ! -x "$unpacked" ]; then
	rm -f /tmp/sdautologin/SDAutoLogin-*
	mkdir -p "$(dirname "$unpacked")" || exit 1
	partial="$unpacked.$$"
	if ! gunzip -c "$archive" > "$partial"; then … fi
	chmod 0755 "$partial" || exit 1
	mv "$partial" "$unpacked" || exit 1
fi
exec "$unpacked" "$@"
```

### 5) 通知构造（网络文本 → 系统 API）

```go
// internal/platform/notify.go:8-20
	case "darwin":
		return "osascript", []string{"-e",
			"display notification " + appleScriptString(body) +
				" with title " + appleScriptString(title)}
	default:
		return "notify-send", []string{title, body}
```
```go
// internal/tray/tray_unix.go:250
		platform.Notify(i18n.L("app.name"), i18n.L("balloon.reconnectFailed")+": "+s.Message)
```

### 6) 设置页 URL 进入外部程序（token 入 argv）

```go
// internal/app/settings.go:212（来源）；internal/platform/openurl_other.go:16-21（去向）
	return fmt.Sprintf("http://%s:%s/?token=%s", host, port, token)
…
	cmd = exec.Command("xdg-open", target)
```
