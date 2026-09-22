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

一条命令即可，不需要额外工具：

```bat
build.cmd
```

```sh
./build.sh
```

同样的任务也可以直接调用，交叉编译等场合用它：

```sh
go run ./tools/build            # 列出所有任务
go run ./tools/build build      # 当前平台
go run ./tools/build console    # 控制台版本，供脚本/服务使用
go run ./tools/build all        # Windows 与 Linux，下面列出的所有目标
go run ./tools/build packages   # 下面所有安装包，输出到 dist/packages/
go run ./tools/build verify     # 用各格式自己的工具校验安装包，然后再打一遍
go run ./tools/build windows    # Windows 压缩包及其安装清单
go run ./tools/build deb        # Debian 系安装包
go run ./tools/build rpm        # RPM 系安装包
go run ./tools/build pacman     # Arch 安装包
go run ./tools/build openwrt    # OpenWrt 安装包，给路由器用
go run ./tools/build test       # go vet 与 go test
go run ./tools/build icon       # 按绘图代码重绘图标
go run ./tools/build resources  # 重新生成 Windows 资源文件
go run ./tools/build clean
```

`verify` 要用到各格式自己的工具，因此是 Linux 上的任务：读 deb 与 ipk 的
`dpkg-deb`、读 rpm 的 `rpm`，以及读 Arch 包的 `bsdtar`。找不到的工具会被点名并计入
「未校验」，而不是被悄悄放过；一个包都没能校验的这一次运行会直接失败。

CMake 也驱动同一批任务：

```sh
cmake -S . -B build -G Ninja
cmake --build build                      # 编译主程序
cmake --build build --target check       # go vet 与 go test
cmake --build build --target SDAutoLogin-console
cmake --build build --target all-platforms
cmake --build build --target packages
cmake --build build --target verify
cmake --install build --prefix /usr/local
```

CMake 自身不编译任何东西：编译器是 Go 工具链，编译参数由任务运行器统一持有，
所以三种入口不会各自漂移。工程未声明任何语言，因此不需要 C 编译器；Windows 上
也就要求生成器本身不依赖 C 工具链，Ninja 是合适的选择（单个可执行文件，无需
安装程序）。

交叉编译覆盖 Windows 和 Linux 的 `amd64`、`arm64`、`386`、`armv7`、`mips` 与
`mipsle`。最后三个把 `GOARCH` 单独说不清的东西写进了文件名：`armv7` 是 32 位
ARM 版的指令集，`mips-softfloat` 与 `mipsle-softfloat` 是给路由器的，那些机器
没有浮点单元。MIPS 的两种字节序都在，是因为板子两种都有：Atheros 系（ath79、
ar71xx）是大端，MediaTek 系（ramips、mt76x8）是小端。在路由器上、以及任何没有
桌面的机器上，请用 `run`：托盘需要桌面会话，而设置页用 `--settings` 从网络上访问。

每个 Linux 目标都会构建两份。第二份文件名里带 `-notray`：它去掉了托盘，连带
D-Bus 客户端以及只有托盘才需要的那些东西，体积因此小约五分之一（`mipsle` 从
10.0 MiB 降到 7.7 MiB）。这一份是给路由器的，那里没有桌面会话可以放图标。在这
种构建里，`--app` 会直接报错而不是被忽略，因为独立窗口正是由托盘打开的。保留托盘
的那一份是默认版本，供有桌面的机器使用。

macOS 是例外：托盘依赖 cgo，必须在本机安装 Xcode 命令行工具
（`xcode-select --install`）后编译。

Windows 可执行文件内含图标、版本信息和 DPI 感知清单，由
`winres/rsrc_windows_<arch>.syso` 嵌入，一个架构一份（`amd64`、`arm64` 与
`386`），工具链也正是靠文件名判断该带上哪一份。图标和这些资源文件都是生成的
（来源是源码里的绘图代码与 `winres/winres.json`），用上面的 `icon`、`resources`
任务重新生成即可。它们都生成在 `winres/` 内、也就是各自的来源文件旁边，因此仓库
根目录只有源码。它们都是可复现的，没有改动图案时重新生成不会产生任何差异。

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

交付出去的地址只能用来打开一次；每交付一个、以及每用掉一个，都会再生成一个新的。
每个新地址生成时就写进日志，所以日志里这些行的最后一行就是当前地址。

没有托盘的机器上，可以用 `run --settings=ADDR` 让同一份设置页监听指定的地址。
该端点立即启动，并一直保留到程序退出；当 ADDR 不是回环地址时，网页可从网络访问，
此时地址里的一次性令牌是唯一保护，请只放在可信网络内或置于隧道之后。要打开的是日志
里最新的那个地址，它前面那个已经被打开它的浏览器用掉了。

**常规**

- 登录账号列表：一个账号一条，每条自带该账号可用的服务类型，即校园网、中国移动
  （`@zgyd`）、中国联通（`@cucc`）与中国电信（`@ctc`）。拖动（或用方向键）调整
  顺序，顺序就是尝试的优先级，详见下一节。
- 密码是否写入配置文件。
- 界面语言。
- 掉线后自动登录。
- 开机自动运行。
- 检测间隔，以及连通性检测地址。

**高级**

- 门户首页地址和认证接口地址。
- 账号前缀，它与账号、服务类型一起拼成最终提交的账号。
- 请求超时和 `jsVersion` 参数。
- 详细日志。

修改后页面会标记为未保存；点击**保存**会写入 `config.json`、重启检测循环，
并立刻执行一次检测，效果马上可见。每条账号上的测试按钮要等设置保存后才可用：
能问程序的，只有它正在使用的那份列表。

## 账号与尝试顺序

门户对一个客户端 IP 只允许一个会话，因此多个账号不可能同时在线。这张列表要解决的
是另一种情况：首选账号本来能用，但已经不能用了，比如欠费、改了密码、被停用。此时程序
会顺着列表往下，用下一个能用的账号登录。

```
1  s1234567   先中国移动，再中国联通
2  s7654321   校园网
3  s0000003   中国电信                        （已停用）
```

1. 从上往下依次尝试；一条账号按它自己列出的服务类型顺序各提交一次：上例第 1 条会
   先试 `s1234567@zgyd`，再试 `s1234567@cucc`，都失败才轮到第 2 条。
2. 「问题出在账号本身」的应答，比如密码错误、认证失败，会把该条暂时移出尝试队列
   十分钟，并继续试下一条。十分钟既不至于拿一个错密码反复撞门户，也足够短，
   修好之后很快会被发现；保存设置或手动点一次「立即检测」也会立刻解除。
3. 「问题出在门户」的应答，比如系统忙、请求超时、应答读不出来，会保留该条并结束本轮，
   因为门户繁忙时顺着列表走会把所有账号一次全判成坏的。
4. 设置页会标出会话当前用的是哪一条，以及每条被跳过的条目、原因和剩余等待时间。

已停用的条目完全不参与，这样就能在不删掉密码的前提下把它移出队列。

## 命令行

适合服务器、SSH 或脚本环境。命令行输出保持英文。

```text
SDAutoLogin [command] [options]

run            后台运行，无托盘
status         打印一次当前会话状态后退出
login          用配置的账号登录一次并打印门户应答（门户已显示本机在线时不提交）
config         交互式配置，写入 config.json
config --show  打印生效的配置，不含密码
version        打印版本
help           打印用法

--home=DIR         把 config.json 和 logs/ 放在 DIR 而不是可执行文件旁边，
                   供服务与容器使用
--no-log           日志只留在控制台，不写 logs/app.log，供 flash 很小的机器
                   使用
--account=USER:PASSWORD[@SERVICE[+SERVICE...]]
                   登录列表里的一条；可重复给出多条，顺序就是尝试顺序
--username=NAME    用该账号作为列表里唯一的一条
--password=SECRET  用该密码替代配置中的密码
--isp=SUFFIX       运营商后缀，例如 @zgyd；留空表示校园网本身
--save             同时把命令行给出的值写入 config.json
--settings=ADDR    run：额外在 ADDR 上提供设置页，供没有托盘、没有显示器的
                   机器使用，例如 127.0.0.1:8088
--json             status：以单行 JSON 输出，替代文本报告
--app[=ADDR]       把设置页放在一个独立窗口里打开（没有浏览器外框），地址为
                   ADDR，省略则用回环地址
```

选项也可以写成 `--name value`，但当值里含有会被 shell 处理的字符（例如运营商后缀
的 `@`）时，请使用上面 `=` 的写法。`--app` 是例外：它的地址是可选的，且必须用
`=` 写，所以桌面场景就是光秃秃的 `--app`。

这些选项每次运行都会读取，且只对本次运行生效，因此没有终端的环境（容器、服务）
无需经过交互式向导即可完成配置；加上 `--save` 会一并写入 `config.json`。`run` 在
缺少账号时会明确报错退出，而不是向用户提问，因为服务场景没有人能回答向导。

账号选项描述的是**整张列表**，而不是往列表里追加，因为脚本要的就是整张列表：

```sh
# 一条账号，服务与容器场景就是这样
SDAutoLogin run --account=s1234567:SECRET@zgyd --save

# 多条，顺序即尝试顺序；一条也可以写多个服务类型，由 @ 引出
SDAutoLogin run --account=s1234567:SECRET@zgyd+cucc --account=s7654321:OTHER --save
```

密码里含 `@` 时无法用这种写法，因为 `@` 就是引出服务类型的那个字符；这种情况用
`--username` 与 `--password`。

GUI 与命令行是**同一个可执行文件**，只是参数不同：不带子命令时启动托盘并常驻，
带子命令时执行一次后退出。`-cli` 等同于 `run`，`-test-login` 等同于 `login`。

`--app` 保留托盘，但不再把设置页当作网页：它在一个没有地址栏、没有标签页、没有
菜单的浏览器窗口里打开，看起来就是一个独立的应用程序。只有 Chromium 内核能这么
做，因此会去启动 chrome/edge/chromium/brave。一个都没装、或找到的那个启动失败时，
设置页改交给默认浏览器，并在日志里说明。

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
| `accounts` | `[]` | 登录列表，数组顺序就是尝试顺序 |
| `accounts[].username` | | 学号、用户名或邮箱 |
| `accounts[].password` | | 密码，仅在 `rememberPassword` 为真时写入 |
| `accounts[].disabled` | 不存在 | 出现且为真表示该条不参与尝试 |
| `accounts[].isps` | `[""]` | 运营商后缀，例如 `@zgyd`，数组顺序就是尝试顺序 |
| `rememberPassword` | `true` | 是否把密码保存在文件中 |
| `portalBaseUrl` | `http://10.9.1.3/` | 门户首页，用于读取客户端 IP |
| `eportalUrl` | `http://10.9.1.3:801/eportal/` | Dr.COM 认证接口根地址 |
| `accountPrefix` | `,0,` | 门户登录页使用的账号前缀 |
| `checkIntervalSeconds` | `15` | 检测间隔，最小 5 秒 |
| `httpTimeoutSeconds` | `6` | 请求超时，最小 2 秒 |
| `autoLogin` | `true` | 掉线后自动登录 |
| `checkInternet` | `true` | 是否同时探测外网 |
| `verboseLog` | `true` | 记录每次检测的诊断信息 |
| `logToFile` | `true` | 是否同时写 `logs/app.log`，见 `--no-log` |
| `probeUrls` | 见下 | 连通性检测地址 |
| `jsVersion` | `3.3.3` | 门户页面提交的参数 |
| `language` | `auto` | `auto`、`zh` 或 `en` |

```json
{
  "accounts": [
    { "username": "s1234567", "password": "SECRET", "isps": ["@zgyd", "@cucc"] },
    { "username": "s7654321", "password": "OTHER", "isps": [""] },
    { "username": "s0000003", "password": "SECRET", "disabled": true, "isps": ["@ctc"] }
  ],
  "accountPrefix": ",0,",
  "checkIntervalSeconds": 15
}
```

旧版本写下的文件（单个 `username`、`password`、`accountSuffix`）会被读成列表里的
一条，并在下次保存时写成新结构，不需要手工改动。

默认探测地址：`http://connect.rom.miui.com/generate_204`、
`http://www.baidu.com`、`http://www.msftconnecttest.com/connecttest.txt`。
只有正常返回且不含门户特征时才算探测成功，因此被强制门户劫持时不会被误判为
网络正常。

自启项位置：Windows 为注册表
`HKCU\Software\Microsoft\Windows\CurrentVersion\Run`，Linux 为
`~/.config/autostart/sdautologin.desktop`，macOS 为
`~/Library/LaunchAgents/com.sdautologin.plist`。

## 文件位置

- `config.json` - 设置，仅本人可读
- `logs/app.log` - 日志，放在自己的目录里，两者都仅本人可读；超过 4 MiB 时轮转为
  `app.log.1`。`--no-log` 与 `logToFile` 设置可以让它不生成，flash 很小的机器
  （例如路由器）就是这么做的：同样的日志行无论如何都会输出到控制台。

默认情况下两者都在可执行文件旁边，所以在 `dist/` 里编译出来的程序，它的配置和
日志也在 `dist/` 里；移动可执行文件时要连 `config.json` 一起移动。
`--home=DIR` 可以把它们改放到 DIR，下面 service 和容器就是这么做的，这样可执行
文件本身可以留在只读的 `/usr` 上。

## 部署

服务场景用 `run`：无托盘、无显示器地持续检测，日志走 stderr，收到 `SIGTERM` 后
退出。它在没有账号时会拒绝启动，因此账号要先写一次：可以在命令行上用 `--save`
一次性写入，也可以在启动时加上 `--settings`，再从设置页里填写。

### 安装包

`go run ./tools/build packages` 会为每个发行版各打一个包：

| 发行版 | 安装包 | 安装方式 |
| --- | --- | --- |
| Debian、Ubuntu | `sdautologin_1.0.0-1_amd64.deb` | `apt install ./...` |
| Fedora、RHEL、openSUSE | `sdautologin-1.0.0-1.x86_64.rpm` | `dnf install` 或 `rpm -i` |
| Arch | `sdautologin-1.0.0-1-x86_64.pkg.tar.zst` | `pacman -U` |
| OpenWrt | `sdautologin_1.0.0-1_mipsel_24kc.ipk` | `opkg install` |
| Windows | `SDAutoLogin-1.0.0-windows-amd64.zip` | 解压即用 |

每个包装的东西是一样的，只是按各家的规矩摆放：无托盘那份程序、运行它的 systemd
unit、声明 `sdautologin` 用户的那个文件，以及许可证和文档。设置与日志都在
`/var/lib/sdautologin`；服务由安装包启用；每个包都得先给一个账号，否则服务起不来，
因为服务面前没有人能回答配置向导。OpenWrt 是唯一的例外，那里既没有 systemd 也没有
`/var/lib`，细节见它自己那一节。

这些包由构建任务自己写出，只用标准库：机器上不需要任何打包工具链，ipk 和 deb 更是
什么都不需要。Arch 包是唯一的例外，因为 Go 没有 zstd 或 xz 的写入器：装了 `zstd`
就用它，否则用 `xz` 或 `gzip`，任务会说明用的是哪一个。

这些包都没有签名。本地安装不成问题：`dpkg -i`、`rpm -i` 与 `pacman -U` 都接受
未签名的文件，pacman 只有在配置要求签名、而 `LocalFileSigLevel` 不是 `Optional` 时
才会拒绝。要做成自己的仓库则需要一把密钥，而密钥不是构建任务能凭空造出来的。
任务写在包旁边的是 `SHA256SUMS`：按 `sha256sum` 工具的格式一行一个文件，下载到的
发布产物可以拿它核对。

`go run ./tools/build verify` 会用各个格式自己的工具把包读回来：deb 和 ipk 用
`dpkg-deb`，rpm 用 `rpm -K` 与 `rpm -qpl`，Arch 包用 `bsdtar` 和它自己携带的那份
文件清单；并把 Windows 压缩包与它所依据的编译产物逐字节比对，再确认 Scoop 与
winget 清单里的摘要指向的是当前这个压缩包。最后它会把所有包再打一遍，只要有一个
字节不同就报错：同一个版本打两次，本来就必须是同一个发布产物。

以上这些在每次 push 时都由
[.gitea/workflows/ci.yml](.gitea/workflows/ci.yml) 跑一遍，连同 `go vet`、`go test`
与 `gofmt` 检查，所以装不上的包会在发布之前被发现，而不是发布之后。

打标签推送时会发布 release：同一次运行会创建它，并把各个包连同 `SHA256SUMS`
作为附件挂上去，两个安装清单指向的正是那里。标签必须是 `v` 加上源码里的版本号，
1.0.0 对应 `v1.0.0`；两者对不上时这一步会拒绝发布，否则清单会指向并不存在的
文件。已经存在的 release 会直接沿用，因此同一条标签推两次不会让运行失败。

### Debian 与 Ubuntu

```sh
sudo apt install ./dist/packages/sdautologin_1.0.0-1_amd64.deb
sudo -u sdautologin /usr/bin/SDAutoLogin --home=/var/lib/sdautologin \
     --username=s1234567 --password=SECRET --isp=@zgyd --save config --show
sudo systemctl restart sdautologin
```

安装包会建好 `sdautologin` 用户、启用并启动服务；没有账号时它重试几次就会停下，
postinst 在发现还没有 `config.json` 时也会这么说。机器上的
`/usr/share/doc/sdautologin/README.Debian` 写的是同样的步骤。卸载会留下设置和日志，
purge 也一样：里面的账号不是安装包有权扔掉的。

### Fedora、RHEL 与 openSUSE

```sh
sudo rpm -i dist/packages/sdautologin-1.0.0-1.x86_64.rpm
sudo -u sdautologin /usr/bin/SDAutoLogin --home=/var/lib/sdautologin \
     --username=s1234567 --password=SECRET --isp=@zgyd --save config --show
sudo systemctl restart sdautologin
```

`rpm -K` 校验包内各项摘要，`rpm -qi` 显示头部里写的描述，`rpm -e sdautologin` 卸载
时不会动 `/var/lib/sdautologin`。

### Arch

```sh
sudo pacman -U dist/packages/sdautologin-1.0.0-1-x86_64.pkg.tar.zst
sudo -u sdautologin /usr/bin/SDAutoLogin --home=/var/lib/sdautologin \
     --username=s1234567 --password=SECRET --isp=@zgyd --save config --show
sudo systemctl restart sdautologin
```

`pacman -Qi` 与 `pacman -Qkk` 都能用，后者会拿包内那份清单逐个校验已安装的文件。

### Windows

压缩包里只有程序、README 和许可证：没有安装程序，因为本来也没什么可安装的。解压到
打算长期放置的目录，双击运行即可；设置会写在程序旁边，所以这个目录就是要留住的东西。

`dist/packages/manifests/` 里放的是两个能安装这种压缩包的包管理器所要的东西，即
`scoop/sdautologin.json` 与 winget 清单的三个文件。两者都把发布产物写成下载地址
`<项目地址>/releases/download/v<版本>/`，也就是本项目所在的 Gitea 存放发布附件的
路径；如果发布挪到别处，改 `tools/build/pack_win.go` 里的 `releaseURL`，两个
清单都会跟着变。winget 命令行本身就能校验它们
（`winget validate --manifest dist/packages/manifests/winget`）；Scoop 清单把设置和
日志声明为持久化，因此升级不会丢账号。

### systemd

[packaging/systemd](packaging/systemd) 里有两份现成的 unit，一份是系统服务，一份
供 `systemctl --user` 使用；用户级那份开头就是它自己的安装步骤。系统级那份就是安装
包里放的文件，下面的步骤就是它做的事：

```sh
install -m 0755 dist/SDAutoLogin-linux-amd64 /usr/bin/SDAutoLogin
useradd --system --create-home --home-dir /var/lib/sdautologin \
        --shell /usr/sbin/nologin sdautologin
install -D -m 0644 packaging/systemd/system/sdautologin.service \
        /usr/lib/systemd/system/sdautologin.service
sudo -u sdautologin /usr/bin/SDAutoLogin --home=/var/lib/sdautologin \
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
当 `status` 报告会话不在线时，容器会被标记为 unhealthy。

### OpenWrt

两个 MIPS 构建各打成一份安装包，每种字节序一个：

| 安装包 | 适用 |
| --- | --- |
| `sdautologin_<version>-1_mips_24kc.ipk` | 大端板子，比如 Atheros 系（ath79） |
| `sdautologin_<version>-1_mipsel_24kc.ipk` | 小端板子，比如 MediaTek 系（ramips、mt76x8） |

路由器上没有 systemd，因此这个包装的是 procd 启动脚本
`/etc/init.d/sdautologin` 和 UCI 配置 `/etc/config/sdautologin`。

路由器最缺的是 flash，所以程序是以压缩形式装到
`/usr/lib/sdautologin/SDAutoLogin.gz` 的，而 `/usr/bin/SDAutoLogin` 是一个短小的
包装脚本，首次启动时把它解到 `/tmp` 再从那里运行。这样 flash 占用从 7.7 MiB 降到
2.7 MiB，代价是运行时占用 7.7 MiB 内存。`/tmp`
里的那份每次开机、每次升级各解一次，而且解出来的文件名里带着版本号，所以新包会解出
自己的程序，而不是继续运行上一个包留在内存里的那份。

```sh
go run ./tools/build openwrt
scp dist/packages/sdautologin_*_mipsel_24kc.ipk root@router:/tmp/
ssh root@router opkg install /tmp/sdautologin_*_mipsel_24kc.ipk
```

安装时会启用该服务，此后随路由器一起启动。接着写账号：

```sh
uci set sdautologin.main.username=s1234567
uci set sdautologin.main.password=SECRET
uci set sdautologin.main.isp=@zgyd
uci commit sdautologin
/etc/init.d/sdautologin restart
```

留空的选项不会被传下去，此时由 `config.json` 决定，设置页写的就是它。没有显示器
的路由器可以把设置页打开，从它所服务的网络里访问：

```sh
uci set sdautologin.main.settings=0.0.0.0:8088
uci commit sdautologin
/etc/init.d/sdautologin restart
```

然后打开 `http://10.9.1.3:8088/`，或者路由器实际的地址。地址不是回环时，交付出去的
那个地址里的令牌是唯一的保护，因此请只在可信网络里开着，或者用隧道访问。

`/etc/config/sdautologin` 已声明为配置文件，升级时会保留里面写好的账号；`config.json`
放在 overlay 上的 `/etc/sdautologin`，重启和升级都不会丢。

日志文件在这里默认关闭，因为 `/etc` 就在 flash 上：同样的日志行程序本来就会写到
控制台，procd 把它们交给 `logd`，用 `logread -e SDAutoLogin` 从内存里读回。想要留下
文件可以打开它，代价是日志长满后最多占八个 MiB 的 flash：

```sh
uci set sdautologin.main.log_file=1
uci commit sdautologin
/etc/init.d/sdautologin restart
```

#### 编进固件镜像

路由器也可以出厂就带着这个程序，而不是事后安装。`packaging/openwrt/Makefile` 就是
为此准备的 OpenWrt 包，它需要的 Go 构建系统来自 packages 源：

```sh
cd $OPENWRT
./scripts/feeds update packages
./scripts/feeds install golang
mkdir -p package/sdautologin
cp -r /path/to/SDAutoLogin/packaging/openwrt/. package/sdautologin/
make menuconfig          # Network -> sdautologin
```

它装的是未压缩的程序，因为在那里压缩省不下东西：squashfs 会压缩它保存的所有内容，
而且压得比 gzip 更好（本程序 1.9 MiB 对 2.7 MiB）。不压缩也让程序保持为只读文件
系统里的一个文件，而不是内存里的一份拷贝。启动脚本和 UCI 配置与上面那个安装包装的
是同两个文件。

## 界面语言

`auto` 跟随系统语言，`zh` 和 `en` 为强制指定。该设置影响托盘菜单、气泡提示和
设置页。日志、命令行和源码注释一律使用英文，这样贴出来的日志谁都能看懂。

## 安全说明

- 设置服务只监听 `127.0.0.1`，每次运行使用随机令牌鉴权，空闲后自动关闭。一次请求
  的每个阶段都有期限；令牌的比较不取决于它匹配了多少字节；每个应答都带上安全响应
  头，被拒绝的应答也一样。
- 托盘交付的地址里带着令牌，因为导航是浏览器自己发起的唯一一种请求，也是唯一无法带上
  请求头的一种。它会在页面送出之前被换成一个会话 Cookie，浏览器被送到同一个页面上、
  地址里不带它，于是地址栏、历史记录以及页面所链接的任何东西的 `Referer` 里都没有
  令牌。此后每个请求都以这个 Cookie 校验，会改动东西的请求还必须在请求头里重复它，
  而地址本身只能用来打开一次。
- 无人值守登录必须能拿到密码，因此除非关闭 `rememberPassword`，密码会以明文
  保存在 `config.json` 中。该文件按"仅本人可读"写入，日志也一样（日志里同样会
  出现账号），并且日志住在自己的目录里：两个文件 `0600`，目录 `0700`。旧版本留下
  的、所有人都能读的文件会在下次写入时被收紧。两个 unit 都设 `UMask=0077`，
  系统服务还把 state 目录定为 `0700`。
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
| `internal/platform/` | 操作系统相关：打开文件、网址或应用窗口，自启项，单实例 |
| `internal/brand/` | 图标绘制 |
| `winres/` | Windows 图标、版本与清单，以及由它们编译出的资源文件 |
| `tools/icogen/` | 图标生成器 |
| `tools/build/` | 任务运行器，脚本与 CMake 目标都由它执行 |
| `packaging/systemd/` | systemd unit，一份系统服务、一份 `systemctl --user` |
| `packaging/openwrt/` | OpenWrt 包，以及它的 procd 启动脚本和 UCI 配置 |
| `.gitea/workflows/` | push 时由 Gitea Actions 执行的内容：测试、打好的包，以及对包的校验 |
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
