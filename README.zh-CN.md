# 校园网自动登录

一个常驻通知区域的小程序，用来维持校园网会话。它每隔几秒查询一次 Dr.COM
门户，一旦发现掉线就立刻用保存的账号重新登录。除此之外不做任何事：程序不会
注销当前会话。

[English](README.md) | **简体中文**

编译结果是每个平台一个独立的可执行文件，运行时不需要任何依赖，复制到别的机器
上双击即可使用。

## 目录

- [功能](#功能)
- [工作原理](#工作原理)
- [编译](#编译)
- [使用](#使用)
- [设置](#设置)
- [命令行](#命令行)
- [配置文件](#配置文件)
- [文件位置](#文件位置)
- [界面语言](#界面语言)
- [安全说明](#安全说明)
- [项目结构](#项目结构)
- [常见问题](#常见问题)

## 功能

- Windows / Linux / macOS 通知区域图标由程序自己绘制，颜色跟随会话状态。
- 设置界面是运行在本机回环地址上的网页，用默认浏览器打开，因此不链接任何 GUI
  库，各平台的界面完全一致。
- 界面支持中文和英文，默认跟随系统语言；日志始终为英文。
- 设置分为「常规」和「高级」两页，日常开关（账号、间隔、探测地址）不会和门户
  地址这类参数混在一起。
- 另有无界面版本，方便服务器使用。
- 重新登录成功或失败时弹出气泡提示。
- 开机自启一个开关搞定（注册表 / desktop 文件 / LaunchAgent）。
- 每次检测都有详细日志并按大小轮转，掉线后可以事后排查。日志中不会出现密码。

## 工作原理

程序访问的就是门户登录页面自己使用的接口：

| 步骤 | 请求 | 作用 |
| --- | --- | --- |
| 1 | `GET /` | 从门户首页读取 `v4ip`，也就是门户看到的客户端 IP（NAT 之后的地址，不是本机地址） |
| 2 | `GET ?c=Portal&a=online_list` | 查询该 IP 是否已认证。即使已经掉线，这个接口在校内依然可以访问 |
| 3 | `GET ?c=Portal&a=login` | 以 `login_method=1` 提交 `user_account` 和 `user_password` |

只有当第 2 步明确回答「该客户端未在线」时才会尝试登录。如果门户不可达，或者
返回内容无法解析，则视为状态未知，本轮不登录，这样在网络中断时不会反复无效请求
门户。登录之后程序会再查询一次，所以「接口返回成功但实际仍离线」会被如实报成
失败，而不是成功。

程序还可以探测几个公网地址，但探测结果只用于状态显示，是否重连始终以门户的
回答为准。

## 编译

需要 Go 1.26 或更新版本。依赖已经 vendor 到仓库里，离线也能编译。

Windows：

```bat
build.cmd
```

Linux 和 macOS：

```sh
./build.sh
```

也可以直接敲命令：

```sh
# Windows，不带控制台窗口
go build -trimpath -ldflags "-s -w -H=windowsgui" -o SDAutoLogin.exe .

# Linux / macOS
go build -trimpath -ldflags "-s -w" -o SDAutoLogin .
```

Windows 和 Linux 都可以交叉编译，例如：

```sh
GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o SDAutoLogin .
```

macOS 是例外：托盘依赖 cgo，必须在本机安装 Xcode 命令行工具
（`xcode-select --install`）后编译。

Windows 可执行文件内含图标、版本信息和 DPI 感知清单，这些资源位于
`rsrc_windows_amd64.syso`，由 `winres/winres.json` 和 `winres/icon.ico`
生成，两者都可以重新生成：

```sh
go run tools/icogen/main.go winres/icon.ico   # 重绘图标
go-winres make --in winres/winres.json --out rsrc --arch amd64
```

## 使用

双击 `SDAutoLogin.exe`（或运行 `./SDAutoLogin`）。程序会在通知区域放置图标并
在后台持续检测。首次运行还没有保存账号时，设置页会自动打开。

程序已在运行时再次启动，不会多出一个托盘图标，而是让正在运行的实例打开
设置页。

托盘菜单：

| 菜单项 | 作用 |
| --- | --- |
| 状态 | 当前状态，同时显示在悬停提示里 |
| 上次登录 | 最近一次自动登录的时间 |
| 立即检测并登录 | 马上执行一次检测 |
| 设置… | 在浏览器中打开设置页 |
| 开机自动运行 | 添加或删除自启项 |
| 重新加载配置 | 重新读取 `config.json` 并重启检测 |
| 打开配置文件 | 用默认编辑器打开 `config.json` |
| 打开日志 | 打开 `logs/app.log` |
| 退出 | 退出程序，校园网会话保持在线 |

单击图标即可打开设置页，右键打开菜单。

## 设置

设置页监听 `127.0.0.1` 的随机端口，并使用只有托盘才能给出的一次性令牌鉴权，
其他本机程序无法读取或修改配置。15 分钟没有请求后服务自动关闭并释放端口，
下次打开设置页时会再次启动。

**常规**

- 账号：学号、用户名或完整邮箱都可以。
- 密码，以及是否写入配置文件。
- 服务类型：校园网、中国移动（`@zgyd`）、中国联通（`@cucc`）、
  中国电信（`@ctc`）。
- 界面语言。
- 掉线后自动登录。
- 开机自动运行。
- 检测间隔，以及连通性检测地址。

**高级**

- 门户首页地址和认证接口地址。
- 账号前缀与后缀，二者会拼成最终提交的账号。
- 请求超时和 `jsVersion` 参数。
- 详细日志。

修改后页面会标记为未保存；点击**保存**会写入 `config.json`、重启检测循环，
并立刻执行一次检测，效果马上可见。

## 命令行

适合服务器、SSH 或脚本环境。命令行输出保持英文。

```text
SDAutoLogin [command]

run            后台运行，无托盘、无设置页
status         打印一次当前会话状态后退出
login          用配置的账号登录一次并打印门户应答
config         交互式配置，写入 config.json
config --show  打印生效的配置，不含密码
version        打印版本
help           打印用法
```

不带子命令时启动托盘。`-cli` 等同于 `run`，`-test-login` 等同于 `login`。

`status` 的退出码：`0` 在线，`1` 状态未知或失败，`3` 门户回答未登录。可以直接
用于监控脚本。

## 配置文件

`config.json` 与可执行文件放在同一目录，首次运行会以默认值创建。

| 字段 | 默认值 | 含义 |
| --- | --- | --- |
| `username` | | 学号、用户名或邮箱 |
| `password` | | 密码，仅在 `rememberPassword` 为真时写入 |
| `rememberPassword` | `true` | 是否把密码保存在文件中 |
| `portalBaseUrl` | `http://10.9.1.3/` | 门户首页，用于读取客户端 IP |
| `eportalUrl` | `http://10.9.1.3:801/eportal/` | Dr.COM 认证接口根地址 |
| `accountPrefix` | `,0,` | 门户登录页使用的账号前缀 |
| `accountSuffix` | | 运营商后缀，例如 `@zgyd` |
| `checkIntervalSeconds` | `15` | 检测间隔，最小 5 秒 |
| `httpTimeoutSeconds` | `6` | 请求超时，最小 2 秒 |
| `autoLogin` | `true` | 掉线后自动登录 |
| `checkInternet` | `true` | 是否同时探测外网 |
| `verboseLog` | `true` | 记录每次检测的诊断信息 |
| `probeUrls` | 见下 | 连通性检测地址 |
| `jsVersion` | `3.3.3` | 门户页面提交的参数 |
| `language` | `auto` | `auto`、`zh` 或 `en` |

默认探测地址：`http://connect.rom.miui.com/generate_204`、
`http://www.baidu.com`、`http://www.msftconnecttest.com/connecttest.txt`。
只有正常返回且不含门户特征时才算探测成功，因此被强制门户劫持时不会被误判为
网络正常。

自启项位置：Windows 为注册表
`HKCU\Software\Microsoft\Windows\CurrentVersion\Run`，Linux 为
`~/.config/autostart/sdautologin.desktop`，macOS 为
`~/Library/LaunchAgents/com.sdautologin.plist`。

## 文件位置

以下两个都在可执行文件旁边：

- `config.json` - 设置
- `logs/app.log` - 日志，超过 4 MiB 时轮转为 `app.log.1`

## 界面语言

`auto` 跟随系统语言，`zh` 和 `en` 为强制指定。该设置影响托盘菜单、气泡提示和
设置页。日志、命令行和源码注释一律使用英文，这样贴出来的日志谁都能看懂。

## 安全说明

- 设置服务只监听 `127.0.0.1`，每次运行使用随机令牌鉴权，空闲后自动关闭。
- 无人值守登录必须能拿到密码，因此除非关闭 `rememberPassword`，密码会以明文
  保存在 `config.json` 中。请确保该文件仅本人可读。
- 程序从不调用注销接口，因此退出程序不会导致断网。

## 项目结构

| 路径 | 内容 |
| --- | --- |
| `main.go` | 入口、模式选择、后台运行 |
| `portal.go` | Dr.COM 门户客户端 |
| `service.go` | 检测与重连循环 |
| `config.go` | 配置 |
| `appstate.go` | 托盘、设置页和命令行共享的状态 |
| `websettings.go` | 本地设置服务及其 JSON 接口 |
| `webui/` | 设置页，编译进可执行文件 |
| `i18n.go`、`i18n_web.go` | 界面文案 |
| `locale_windows.go`、`locale_other.go` | 系统语言判定 |
| `internal/brand/` | 图标绘制 |
| `tray_windows.go`、`tray_unix.go`、`tray_other.go` | 各平台托盘 |
| `cli.go`、`setup.go` | 命令行 |
| `log.go` | 带轮转的日志 |
| `win32_windows.go`、`icon_windows.go`、`openurl_*.go`、`autostart_*.go` | 平台相关代码 |
| `winres/`、`rsrc_windows_amd64.syso` | Windows 图标、版本、清单 |
| `tools/icogen/` | 图标生成器 |
| `vendor/` | 依赖库 |

## 常见问题

- **掉线后没有反应。** 查看 `logs/app.log` 中的
  `session is down, reconnect attempt #N` 以及随后的门户应答。`ret_code` 为
  `1` 表示账号或密码不对，`3` 表示门户繁忙。
- **图标一直是灰色。** 门户首页没有响应，状态未知，因此不会尝试登录。通常是
  机器已经不在校园网内。
- **检测结果为未知。** 门户有响应但返回内容里没有 `result` 字段，例如被强制
  门户劫持。最后一次应答会写进日志。
- **门户地址变了。** 在「高级」页修改门户首页地址和认证接口地址。

## 许可

MIT，详见 [LICENSE](LICENSE)。
