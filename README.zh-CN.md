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
- [部署](#部署)
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

需要 Go 1.26 或更新版本。依赖已经 vendor 到仓库里，离线也能编译。产物统一放在
`dist/`；`clean` 任务只删除其中的可执行文件，不会动设置和日志，因此在 `dist/`
里运行的构建不会丢掉账号。

最省事的方式，一条命令、不需要额外工具：

```bat
build.cmd
```

```sh
./build.sh
```

同样的任务也可以直接调用，交叉编译等场合用它更方便：

```sh
go run ./tools/build            # 列出所有任务
go run ./tools/build build      # 当前平台
go run ./tools/build console    # 控制台版本，供脚本/服务使用
go run ./tools/build all        # Windows 与 Linux，amd64 与 arm64
go run ./tools/build test       # go vet 与 go test
go run ./tools/build icon       # 按绘图代码重绘图标
go run ./tools/build resources  # 重新生成 Windows 资源文件
go run ./tools/build clean
```

如果你习惯 CMake，它也驱动同一批任务：

```sh
cmake -S . -B build -G Ninja
cmake --build build                      # 编译主程序
cmake --build build --target check       # go vet 与 go test
cmake --build build --target SDAutoLogin-console
cmake --build build --target all-platforms
cmake --install build --prefix /usr/local
```

CMake 自身不编译任何东西：编译器是 Go 工具链，编译参数由任务运行器统一持有，
所以三种入口不会各自漂移。工程未声明任何语言，因此不需要 C 编译器；Windows 上
也就要求生成器本身不依赖 C 工具链，Ninja 是合适的选择（单个可执行文件，无需
安装程序）。

交叉编译覆盖 Windows 和 Linux。macOS 是例外：托盘依赖 cgo，必须在本机安装
Xcode 命令行工具（`xcode-select --install`）后编译。

Windows 可执行文件内含图标、版本信息和 DPI 感知清单，由
`winres/rsrc_windows_amd64.syso` 嵌入；图标和该文件都是生成的（源码里的绘图代码
与 `winres/winres.json`），用上面的 `icon`、`resources` 任务重新生成即可。两者
都生成在 `winres/` 内、也就是它们的来源文件旁边，因此仓库根目录只有源码。两者
都是可复现的，没有改动图案时重新生成不会产生任何差异。

## 使用

双击 `dist\SDAutoLogin.exe`（或在其它平台运行 `./dist/SDAutoLogin`）。程序会在
通知区域放置图标并在后台持续检测。首次运行还没有保存账号时，设置页会自动打开。

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

没有托盘的机器上，可以用 `run --settings=ADDR` 让同一份设置页监听你指定的地址。
该端点立即启动，并一直保留到程序退出；当 ADDR 不是回环地址时，网页可从网络访问，
此时地址里的一次性令牌是唯一保护，请只放在可信网络内或置于隧道之后。

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
SDAutoLogin [command] [options]

run            后台运行，无托盘
status         打印一次当前会话状态后退出
login          用配置的账号登录一次并打印门户应答
config         交互式配置，写入 config.json
config --show  打印生效的配置，不含密码
version        打印版本
help           打印用法

--home=DIR         把 config.json 和 logs/ 放在 DIR 而不是可执行文件旁边，
                   供服务与容器使用
--username=NAME    用该账号替代配置中的账号
--password=SECRET  用该密码替代配置中的密码
--isp=SUFFIX       运营商后缀，例如 @zgyd；留空表示校园网本身
--save             同时把命令行给出的值写入 config.json
--settings=ADDR    run：额外在 ADDR 上提供设置页，供没有托盘、没有显示器的
                   机器使用，例如 127.0.0.1:8088
--json             status：以单行 JSON 输出，替代文本报告
```

选项也可以写成 `--name value`，但当值里含有会被 shell 处理的字符（例如运营商后缀
的 `@`）时，请使用上面 `=` 的写法。

这些选项每次运行都会读取，且只对本次运行生效，因此没有终端的环境（容器、服务）
无需经过交互式向导即可完成配置；加上 `--save` 会一并写入 `config.json`。`run` 在
缺少账号时会明确报错退出，而不是向用户提问——服务场景没有人能回答向导。

GUI 与命令行是**同一个可执行文件**，只是参数不同：不带子命令时启动托盘并常驻，
带子命令时执行一次后退出。`-cli` 等同于 `run`，`-test-login` 等同于 `login`。

Windows 上该文件按 GUI 程序链接，双击不会弹出控制台窗口；从终端启动时会自动
附接到该终端，因此输出能显示出来。如果要把输出重定向到管道，或需要脚本读取
退出码，请改编译成控制台版本（同一份源码，去掉 `-H=windowsgui` 即可）：

```sh
go build -trimpath -ldflags "-s -w" -o SDAutoLogin-console.exe ./cmd/SDAutoLogin
```

`status` 的退出码：`0` 在线，`1` 状态未知或失败，`3` 门户回答未登录。可以直接
用于监控脚本。

`status --json` 把同一次检测以单行 JSON 输出。日志走 stderr，因此 stdout 上只有
JSON，脚本无需过滤任何东西：

```sh
SDAutoLogin status --json | jq -r .state
```

```json
{"portal":"http://10.9.1.3/","state":"online","reachable":true,"known":true,"online":true,"clientIp":"203.0.113.7","account":"s1234567","onlineSince":"2026-01-01 00:00:00","detail":"online"}
```

`state` 取 `online`、`offline`、`unknown`（门户有应答但没有可用信息）或
`unreachable`（门户完全没有应答）。后两种的退出码都是 `1`，靠 `state` 区分。

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

- `config.json` - 设置
- `logs/app.log` - 日志，超过 4 MiB 时轮转为 `app.log.1`

默认情况下两者都在可执行文件旁边，所以在 `dist/` 里编译出来的程序，它的配置和
日志也在 `dist/` 里；移动可执行文件时记得把 `config.json` 一起移动。
`--home=DIR` 可以把它们改放到 DIR，下面 service 和容器就是这么做的，这样可执行
文件本身可以留在只读的 `/usr` 上。

## 部署

服务场景用 `run`：无托盘、无显示器地持续检测，日志走 stderr，收到 `SIGTERM` 后
退出。它在没有账号时会拒绝启动，因此账号要先写一次——可以在命令行上用 `--save`
一次性写入，也可以在启动时加上 `--settings`，再从设置页里填写。

### systemd

[packaging/systemd](packaging/systemd) 里有两份现成的 unit，一份是系统服务，一份
供 `systemctl --user` 使用。每个文件开头就是安装命令、写账号的步骤和查看日志的
命令。以服务器为例，简版如下：

```sh
install -m 0755 dist/SDAutoLogin-linux-amd64 /usr/local/bin/SDAutoLogin
useradd --system --create-home --home-dir /var/lib/sdautologin \
        --shell /usr/sbin/nologin sdautologin
install -m 0644 packaging/systemd/system/sdautologin.service \
        /etc/systemd/system/sdautologin.service
sudo -u sdautologin /usr/local/bin/SDAutoLogin --home=/var/lib/sdautologin \
     --username=s1234567 --password=SECRET --isp=@zgyd --save config --show
systemctl daemon-reload
systemctl enable --now sdautologin
journalctl -u sdautologin -f
```

该 unit 把设置和日志放在 `/var/lib/sdautologin`，并把进程限制在它需要的网络权限
内。如果想用浏览器配置这台机器，在 `ExecStart` 里加上 `--settings=127.0.0.1:8088`，
再用 SSH 端口转发：`ssh -L 8088:127.0.0.1:8088 the-server`，然后打开
`http://127.0.0.1:8088/`。

### 容器

[Dockerfile](Dockerfile) 编译出同一个静态二进制，装进一个小镜像里，以非 root
用户运行，用 `/data` 存放设置和日志：

```sh
docker build -t sdautologin .
docker run -d --name sdautologin --restart unless-stopped \
    -e TZ=Asia/Shanghai -v sdautologin:/data sdautologin
docker exec sdautologin SDAutoLogin --home=/data status --json
```

账号同样先写一次，用一次性运行：

```sh
docker run --rm -v sdautologin:/data sdautologin \
    --username=s1234567 --password=SECRET --isp=@zgyd --save config --show
```

`--home=/data` 写在入口点里，因此 `status` 之类的一次性命令读取的是同一份设置。
当 `status` 报告会话不在线时，容器会被标记为 unhealthy——这正是这个程序唯一值得
关注的事情。

## 界面语言

`auto` 跟随系统语言，`zh` 和 `en` 为强制指定。该设置影响托盘菜单、气泡提示和
设置页。日志、命令行和源码注释一律使用英文，这样贴出来的日志谁都能看懂。

## 安全说明

- 设置服务只监听 `127.0.0.1`，每次运行使用随机令牌鉴权，空闲后自动关闭。
- 无人值守登录必须能拿到密码，因此除非关闭 `rememberPassword`，密码会以明文
  保存在 `config.json` 中。请确保该文件仅本人可读。
- 程序从不调用注销接口，因此退出程序不会导致断网。

## 项目结构

代码采用 Go 的常规布局：`cmd/` 放可执行程序，`internal/` 放构成它的各个包。
模块之外无法引用这些包，对外的接口就只有命令行和设置页。

| 路径 | 内容 |
| --- | --- |
| `cmd/SDAutoLogin/` | 入口：模式选择、托盘与后台运行、命令行、控制台附着 |
| `internal/portal/` | Dr.COM 门户客户端 |
| `internal/service/` | 检测与重连循环、会话状态 |
| `internal/config/` | 配置文件、运营商列表、校验 |
| `internal/app/` | 托盘、设置页和命令行共享的配置与服务，以及本地设置服务 |
| `internal/webui/` | 设置页，以及由它编译进来的静态资源 |
| `internal/i18n/` | 界面文案与系统语言判定 |
| `internal/logging/` | 带轮转的日志 |
| `internal/tray/` | 各平台托盘，含 Win32 原语与图标转换 |
| `internal/platform/` | 操作系统相关：打开文件或网址、自启项、单实例 |
| `internal/brand/` | 图标绘制 |
| `winres/` | Windows 图标、版本与清单，以及由它们编译出的资源文件 |
| `tools/icogen/` | 图标生成器 |
| `tools/build/` | 任务运行器，脚本与 CMake 目标都由它执行 |
| `packaging/systemd/` | systemd unit，一份系统服务、一份 `systemctl --user` |
| `Dockerfile` | 容器镜像，用 vendor 目录里的源码编译 |
| `CMakeLists.txt` | 任务运行器的 CMake 前端 |
| `dist/` | 编译结果，以及在 `dist/` 中运行时的设置与日志 |
| `build/` | 使用 CMake 时的构建目录 |
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
