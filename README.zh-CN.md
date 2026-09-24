# 校园网自动登录

一个小托盘程序，让你的校园网会话一直在线。它每隔几秒问一次门户"我还在线吗"，掉线了
就自动替你补上登录。它从不主动注销——那是你的事。

[English](README.md) | **简体中文**

每个平台一个自带全部依赖的可执行文件，不用安装。拷过去、双击，完事。

## 目录

- [功能](#功能)
- [工作原理](#工作原理)
- [编译](#编译)
- [使用](#使用)
- [设置](#设置)
- [账号与尝试顺序](#账号与尝试顺序)
- [命令行](#命令行)
- [配置文件](#配置文件)
- [文件位置](#文件位置)
- [部署](#部署)
- [界面语言](#界面语言)
- [安全说明](#安全说明)
- [项目结构](#项目结构)
- [常见问题](#常见问题)

## 功能

- Windows、Linux、macOS 上的托盘图标，由程序自己绘制，颜色跟着会话状态走。
- 设置页是一个本地网页，服务在回环地址上、在你的浏览器里打开：不链接任何 GUI 工具包，
  三个平台看到的是同一个窗口。
- 中文或英文界面，默认跟随系统语言。日志保持英文。
- 选项分成「常规」和「高级」，日常要碰的开关（账号、间隔、探测）不会和门户接口地址
  混在一起。
- 给没有桌面的机器准备了一套命令行模式。
- 重新登录成功、或登录失败时给个通知：Windows 上是气泡，其它平台用系统自己的通知。
- 开机自启，每个平台一个开关（注册表、桌面项、LaunchAgent）。
- 每次检测都写日志并滚动，掉线之后可以回查。密码永远不会写进日志。

## 工作原理

走的是门户自己的登录页用的那几个接口：

| 步骤 | 请求 | 用途 |
| --- | --- | --- |
| 1 | `GET /` | 从门户首页读 `v4ip`，即门户看到的那个 IP（是 NAT 之后的，不是你本机的） |
| 2 | `GET ?c=Portal&a=online_list` | 问这个 IP 有没有通过认证；这个接口在校内、会话已掉时也照样应答 |
| 3 | `GET ?c=Portal&a=login` | 提交 `user_account` 和 `user_password`，带 `login_method=1` |

只有第 2 步说"门户认为你*不*在线"时才会尝试登录。门户连不上、或者应答读不出可用信息
时，状态记为未知、什么都不提交——一次网络故障不该变成一串没意义的请求。登录之后还会
再问一次，所以"门户说接受了但仍显示离线"会被报成失败，而不是成功。

打开连通性探测后，它只喂给状态显示。要不要登录，永远只看门户本身。

## 编译

需要 Go 1.26 或更新。依赖全部 vendor 进来，离线也能编译。产物统一落在 `dist/`；
`clean` 只清掉那里的可执行文件，不碰设置和日志，所以从 `dist/` 启动的构建不会丢账号。

一条命令，不需要额外工具：

```bat
build.cmd
```

```sh
./build.sh
```

也可以直接调这些任务，两个脚本和 CMake 目标走的就是它们：

```sh
go run ./tools/build            # 列出所有任务
go run ./tools/build build      # 当前平台
go run ./tools/build console    # 控制台版，供脚本和服务使用
go run ./tools/build all        # Windows 与 Linux 的全部目标
go run ./tools/build packages   # 下面所有安装包，输出到 dist/packages/
go run ./tools/build verify     # 用各格式自己的工具检查安装包，再重新打一遍
go run ./tools/build windows    # Windows 压缩包及其清单
go run ./tools/build deb        # Debian 安装包
go run ./tools/build rpm        # RPM 安装包
go run ./tools/build pacman     # Arch 安装包
go run ./tools/build openwrt    # OpenWrt 安装包，给路由器用
go run ./tools/build test       # go vet 与 go test
go run ./tools/build icon       # 按绘图代码重画图标
go run ./tools/build resources  # 重新生成 Windows 资源文件
go run ./tools/build clean
```

`verify` 需要各格式的工具，所以是个 Linux 任务：deb 和 ipk 用 `dpkg-deb`，rpm 用
`rpm`，Arch 包用 `bsdtar`。找不到的工具会被点名并计数，而不是跳过去；一个包都没能
检查的运行会失败。

CMake 驱动同一批任务：

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

CMake 自己不编译任何东西——编译器是 Go 工具链，编译参数由任务入口统一持有，所以三种
入口不会各自漂移。工程不声明任何语言，因此不需要 C 编译器；在 Windows 上也就不该要求
生成器依赖 C 工具链，Ninja（单个可执行文件、不用装）是个好选择。

交叉编译覆盖 Windows 和 Linux，架构包括 `amd64`、`arm64`、`386`、`armv7`、`mips` 与
`mipsle`。后三个把 `GOARCH` 说不清的东西写进了文件名：`armv7` 是 32 位 ARM 版的指令集，
`mips-softfloat` / `mipsle-softfloat` 给路由器用，那些机器没有浮点单元。MIPS 两种字节序
都在，是因为两种板子都有：Atheros 系（ath79、ar71xx）是大端，MediaTek 系（ramips、
mt76x8）是小端。在路由器上、以及任何没有桌面的机器上，请用 `run`：托盘需要桌面会话，
而设置页靠 `--settings` 从网络上访问。

每个 Linux 目标都会编译两份。第二份文件名里带 `-notray`：去掉托盘，连带去掉 D-Bus
客户端和其余只有托盘才需要的东西，体积小约五分之一（`mipsle` 从 10.0 MiB 降到 7.7 MiB）。
这份是给路由器的。在这种构建里 `--app` 会直接报错而不是被忽略，因为那个独立窗口本来就是
托盘打开的。保留托盘的那份是默认版本，给有桌面的机器。

macOS 是例外：它的托盘依赖 cgo，必须在装了 Xcode 命令行工具的 macOS 上编译
（`xcode-select --install`）。

Windows 可执行文件内含图标、版本信息和 DPI 感知清单，由
`winres/rsrc_windows_<arch>.syso` 嵌入，一个架构一份（`amd64`、`arm64`、`386`）——
工具链正是靠文件名判断该带哪一份。图标和这些资源文件都是生成的（来源是源码里的绘图
代码和 `winres/winres.json`），用上面的 `icon`、`resources` 任务重新生成即可。它们都
写在 `winres/` 里、各自来源文件的旁边，而且可复现：没改图案时重新生成不会有任何差异。

## 使用

双击 `dist\SDAutoLogin.exe`，其它平台运行 `dist/SDAutoLogin`。程序会放上托盘图标并在
后台持续检测。首次运行、还没有保存账号时，设置页会自己打开。

托盘菜单：

| 菜单项 | 作用 |
| --- | --- |
| 状态 | 当前状态，悬浮提示里也是它 |
| 上次登录 | 最近一次自动登录的时间 |
| 立即检测并登录 | 立刻跑一次检测 |
| 设置… | 在浏览器里打开设置页 |
| 开机自动运行 | 添加或移除自启项 |
| 重新加载配置 | 重读 `config.json` 并重启检测循环 |
| 打开配置文件 | 用默认编辑器打开 `config.json` |
| 打开日志 | 打开 `logs/app.log` |
| 退出 | 退出程序；校园网上的会话保持在线 |

单击图标打开设置页，右键打开菜单。

## 设置

设置页服务在 `127.0.0.1` 的一个随机端口上，靠一个只有托盘能交出去的一次性 token 保护，
所以本机其它程序读不到也改不了你的配置。15 分钟没有请求它就自己关掉、把端口还回去，
下次打开页面时再启动。

交出去的地址只能用**一次**：每发出一个地址、每用掉一个地址，都会再生成一个新的，而且
生成时就写进日志。所以当前可用的地址永远是这些行里最新的那条。

没有托盘的机器上，用 `run --settings=ADDR` 把同一个页面服务在你指定的地址上。这个端点
启动即生效，一直到程序退出；当 ADDR 不是回环地址时，网络里就能访问它，而唯一保护就是
地址里的 token，所以请留在可信网络里，或者挂在隧道后面。

**常规**

- 登录列表：一条条目一个账号，各带它可以使用的服务类型——校园网本身、中国移动
  （`@zgyd`）、中国联通（`@cucc`）、中国电信（`@ctc`）。拖动条目，或用方向键移动，
  就是它们被尝试的顺序；详见下一节。
- 密码是否写入配置文件。
- 界面语言。
- 掉线后自动登录。
- 启动时把已在线的会话换成其账号的首选服务类型；以及在该会话所属条目的卡片上，可以换成
  该条目列出的任一其它服务类型的控件——被门户拒绝切换之后，其中还包括门户自己的页面。
- 开机自动运行。
- 检测间隔，以及连通性探测。

**高级**

- 门户首页地址和认证接口地址。
- 账号前缀，它与账号、服务类型一起拼成最终提交的账号。
- 请求超时和 `jsVersion` 参数。
- 详细日志。

改动任何东西都会把页面标记为未保存；**保存**会写入 `config.json`、重启检测循环并立刻
跑一次检测，效果马上可见。条目上的测试按钮要等保存之后才可用：能问的只有程序正在使用
的那份列表。

## 账号与尝试顺序

门户对一个客户端地址只允许一个会话，所以多个账号不可能同时在线。这张列表是为了那一天：
第一个账号本来是你想用的那个，却不好使了——余额用完、密码被改、账号注销。于是程序往下
走，用下一个能用的条目登录：

```
1  s1234567   中国移动，其次中国联通
2  s7654321   校园网
3  s0000003   中国电信                      （已停用）
```

1. 条目自上而下尝试，每条按它自己列出的顺序、对每个服务类型各提交一次：上面第 1 行会先试
   `s1234567@zgyd`，再试 `s1234567@cucc`，然后才轮到 `s7654321`。
2. 说账号本身有问题的应答——密码错、认证失败——会把那条条目停用十分钟，然后继续下一条。
   十分钟够长，不会拿着一个错密码反复砸门户；也够短，密码改好了能及时接上。保存配置或
   手动检测会立刻清掉这个等待。
3. 说的是门户而不是账号的应答——门户忙、请求超时、应答读不出来——会保留那条条目、当轮
   到此为止。门户一忙就把整张列表走一遍，会把所有账号一次标记成坏的。
4. 页面上会标出你的会话正在使用哪条，以及每条被搁置的条目（含原因和剩余时间）。

停用的条目完全不参与，所以你可以把它移出尝试队列而不删掉它的密码。

**把已在线的会话换到首选服务类型。** 条目里服务类型的顺序，就是程序自己登录时遵循的顺序。
而程序启动时已经在线的会话——你手工登录的，或上一次运行留下的——门户不会替你换。一个
地址只允许一个会话，意味着要换类型只能靠一次替换登录，所以程序会以该条目首选的服务类型
重新登录一次，并在会话仍停在原处时一小时后再问一次。

门户在会话在线时拒绝再次登录，就照实说明并保持原样，也不会因此把该条目移出尝试队列——
被拒绝的不是账号。你的会话在线可用，所以设置页、顶部胶囊和托盘说的是「需处理」，而不是
报一个错误。在用账号的卡片上就有对应控件：会话现在在哪种服务类型上、该条目的首选是哪种，
以及用该条目列出的任意其它类型重新登录——上面的选项开不开都不影响它。一旦出现过拒绝，
卡片还会多出一个用浏览器打开门户页面的按钮：门户若是在会话在线时一律拒绝第二次登录，
只能去那边把会话注销掉。

## 命令行

服务器、SSH、脚本场景用得上。命令行输出保持英文。

```text
SDAutoLogin [command] [options]

run            run in the background, no tray
status         print the current session state once and exit
login          submit one login and print the portal reply (nothing is submitted
               while the portal already shows this client online)
config         interactive setup, writes config.json
config --show  print the effective configuration, password never included
version        print the version
help           print the usage text

--home=DIR         keep config.json and logs/ in DIR instead of next to the
                   executable, for a service or a container
--no-log           keep the log on the console alone, with no logs/app.log, for
                   a machine with a small flash
--account=USER:PASSWORD[@SERVICE[+SERVICE...]]
                   one entry of the login list; repeat the option to write
                   several, in the order they are to be tried
--username=NAME    use this account, as the single entry of the list
--password=SECRET  use this password instead of the configured one, which on its
                   own is the password of the first entry of the list
--password-file=PATH
                   read the password from the first line of PATH instead; a
                   secret on the command line is in the process list of every
                   user on the machine, so this is the form to use from a
                   script, a service or a container
--isp=SUFFIX       service suffix such as @zgyd, empty for the campus network
--save             write what was given here into config.json as well
--settings=ADDR    run: also serve the settings page on ADDR, for a machine
                   with no tray and no display, for example 127.0.0.1:8088;
                   the address to open is the one the log prints
--json             status: print one line of JSON instead of the report
--app[=ADDR]       open the settings page in a window of its own, without the
                   browser around it, on ADDR or on loopback
```

选项也可以写成 `--name value`，但值里含有 shell 会当真的字符（比如服务类型前面的 `@`）
时，请用上面那种写法。`--app` 是例外：它的地址可选，而且必须用 `=` 写，所以桌面上直接写
一个光秃秃的 `--app` 就行。

选项每次运行都会重新读取，只对这一次运行生效，所以没有终端的容器或服务可以不用向导就
配置好——加上 `--save` 则顺便写进 `config.json`。`run` 在缺账号时会报错退出，而不是开口
问你要；服务场景没有人在那儿回答。

账号相关的选项描述的是整张列表，而不是往里追加，这正是脚本期望的行为：

```sh
# 一条账号，服务与容器场景就是这样
SDAutoLogin run --account=s1234567:SECRET@zgyd --save

# 多条，顺序即尝试顺序；一条也可以写多个服务类型，由 @ 引出
SDAutoLogin run --account=s1234567:SECRET@zgyd+cucc --account=s7654321:OTHER --save
```

密码里带 `@` 的没法用这种形式写（`@` 是服务类型的分隔符），得用 `--username` 和
`--password`。

把它当服务部署时，密码还有更合适的来源。命令行会出现在机器上每个用户的进程列表里，所以
用 `--password-file=PATH` 从文件第一行读；而 systemd 单元用
`LoadCredential=password:/etc/sdautologin/password` 把密码交过来时，一个选项都不用加：
没有别的地方指定密码时，程序会自己去读 `$CREDENTIALS_DIRECTORY/password`。两种方式都
只丢掉行尾换行，因为密码本身可能以空格结尾。

同一个可执行文件承担两种模式：不带子命令启动就放上托盘图标常驻，带子命令就做完那一件事
退出。`-cli` 等同于 `run`，`-test-login` 等同于 `login`。

`--app` 保留托盘，但不再把设置页当网页看：它在一个没有地址栏、没有标签页、没有菜单的
浏览器窗口里打开，看着就是一个独立的应用。只有 Chromium 内核能这么做，所以会启动
Chrome、Edge、Chromium 或 Brave，并去掉其余浏览器部件；一个都没装、或找到的那个起不来，
就把页面交给你的默认浏览器，并在日志里说明。

Windows 上该文件按 GUI 程序链接，双击不会弹出控制台窗口；从终端启动时它会附接到该终端，
输出在那里能看见。需要把输出重定向到管道、或让脚本读取退出码时，改编译控制台版——同一份
源码，去掉 `-H=windowsgui` 即可：

```sh
go run ./tools/build console
```

`status` 的退出码：`0` 在线、`1` 状态未知或失败、`3` 门户说本机未登录。用在监控脚本里
正合适。`status --json` 把同一次检测打成一行 JSON，而日志走 stderr，所以 stdout 上只有
JSON：

```sh
SDAutoLogin status --json | jq -r .state
```

```json
{"portal":"http://10.9.1.3/","state":"online","reachable":true,"known":true,"online":true,"clientIp":"203.0.113.7","account":"s1234567","onlineSince":"2026-01-01 00:00:00","detail":"online"}
```

`state` 取 `online`、`offline`、`unknown`（门户应答了但没说任何可用的）或 `unreachable`
（压根没应答）。后两个共用退出码 `1`，区分它们的就是这个字段。

## 配置文件

`config.json` 就在程序旁边，首次运行时按默认值创建。

| 字段 | 默认值 | 含义 |
| --- | --- | --- |
| `accounts` | `[]` | 登录列表，按条目被尝试的顺序排列 |
| `accounts[].username` | | 学号、用户名或邮箱 |
| `accounts[].password` | | 密码，仅在 `rememberPassword` 为真时写入 |
| `accounts[].disabled` | 无 | 存在且为 true 则把该条目移出尝试队列 |
| `accounts[].isps` | `[""]` | 形如 `@zgyd` 的服务类型后缀，按尝试顺序排列 |
| `rememberPassword` | `true` | 是否把密码写进文件 |
| `portalBaseUrl` | `http://10.9.1.3/` | 门户首页，用来读客户端 IP |
| `eportalUrl` | `http://10.9.1.3:801/eportal/` | Dr.COM eportal 根地址 |
| `accountPrefix` | `,0,` | 门户页在浏览器登录时用的前缀 |
| `checkIntervalSeconds` | `15` | 多久检测一次，最少 5 秒、最多一天 |
| `httpTimeoutSeconds` | `6` | 请求超时，最少 2 秒、最多 15 分钟 |
| `autoLogin` | `true` | 会话掉了是否自动登录 |
| `switchToPreferredService` | `true` | 把已在线的会话换成其条目的首选服务类型 |
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

旧版本写的文件（单个 `username`、`password` 和 `accountSuffix`）会被读成列表里的一条，
并在下次保存时按新格式写回，所以什么都不用手动做。

默认的探测地址是 `http://connect.rom.miui.com/generate_204`、`http://www.baidu.com`、
`http://www.msftconnecttest.com/connecttest.txt`。探测地址正常应答、且不含门户特征时才算
成功，这样被门户拦截的"假联网"不会被当成外网可用。

自启项：Windows 上是注册表键
`HKCU\Software\Microsoft\Windows\CurrentVersion\Run`；Linux 上是
`~/.config/autostart/sdautologin.desktop`；macOS 上是
`~/Library/LaunchAgents/com.sdautologin.plist`。

## 文件位置

- `config.json` —— 设置，只有主人能读；Windows 上没有权限位可用，改成给它一份只列出程序
  运行账号的访问列表。
- `logs/app.log` —— 日志，放在自己的目录里，同样只有主人能读，超过 4 MiB 滚动到
  `app.log.1`。`--no-log` 和 `logToFile` 都能不要这个文件，这也正是闪存很小的机器
  （比如路由器）想要的：两种情况下同样的行都会进控制台。

两者默认都在程序旁边，所以 `dist/` 里的构建就把设置和日志留在那儿；移动可执行文件时
得把 `config.json` 一起搬。`--home=DIR` 把它们放进 DIR，下面的服务和容器就是这么用的，
好让可执行文件待在只读的 `/usr` 上。

## 部署

用 `run` 模式：它没有托盘也没有显示，持续检测、日志写标准错误、收到 `SIGTERM` 退出。
没有账号时它拒绝启动，所以先把账号写下来——命令行加 `--save`，或在服务带 `--settings`
启动后从设置页填。

### 安装包

`go run ./tools/build packages` 会为程序支持的每个发行版打包：

| 发行版 | 安装包 | 安装方式 |
| --- | --- | --- |
| Debian、Ubuntu | `sdautologin_1.0.0-1_amd64.deb` | `apt install ./...` |
| Fedora、RHEL、openSUSE | `sdautologin-1.0.0-1.x86_64.rpm` | `dnf install` 或 `rpm -i` |
| Arch | `sdautologin-1.0.0-1-x86_64.pkg.tar.zst` | `pacman -U` |
| OpenWrt | `sdautologin_1.0.0-1_mipsel_24kc.ipk` | `opkg install` |
| Windows | `SDAutoLogin-1.0.0-windows-amd64.zip` | 解压即可 |

每个包装的东西一样，只是按各发行版的方式摆放：不带托盘的构建、运行它的 systemd 单元、
声明 `sdautologin` 用户的文件、许可证和文档。设置与日志放在 `/var/lib/sdautologin`，
服务由安装包启用；每一个都得先给账号才站得住，因为服务场景没人回答配置向导。OpenWrt 是
唯一不同的：它没有 systemd、也没有 `/var/lib`，细节见它自己那一节。

安装包由构建任务自己写出来，只用标准库：都不需要装打包工具链，deb 和 ipk 更是什么都不
需要。Arch 包是例外——Go 不会写 zstd 也不会写 xz，所以有 `zstd` 就用它，没有就退到 `xz`
或 `gzip`，输出里会说明用了哪个。

全都没有签名，本地安装没问题：`dpkg -i`、`rpm -i`、`pacman -U` 都接受，pacman 只在配置
要求签名且 `LocalFileSigLevel` 不是 `Optional` 时才抱怨。要自建仓库就需要一把密钥，而密钥
不是构建任务能凭空造出来的。任务写在包旁边的是 `SHA256SUMS`：按 `sha256sum` 的格式一行
一个文件，下载到的发布产物可以拿它核对。

`go run ./tools/build verify` 会用各个格式自己的工具把包读回来——deb 和 ipk 用 `dpkg-deb`，
rpm 用 `rpm -K` 和 `rpm -qpl`，Arch 包用 `bsdtar` 和它自带的文件清单——把 Windows 压缩包
与它所依据的编译产物比对，再确认 Scoop 与 winget 清单里的摘要指向的是当前这个压缩包。最后
把所有包再打一遍，只要有一个字节不同就报错：同一个版本打两次，本来就必须是同一个发布产物。

以上这些只在推送标签时由 [.gitea/workflows/ci.yml](.gitea/workflows/ci.yml) 跑一遍，连同
`go vet`、`go test` 与 `gofmt` 检查，所以装不上的包会在发布之前被发现，而不是发布之后。
除此之外没有别的触发方式：分支上的改动在本地用同一批任务检查，而不是占用 runner。

推标签即发布 release——同一次运行会创建它，并把各个包连同 `SHA256SUMS` 挂上去，两个安装
清单指向的正是那里。版本号就是从这条标签读的，因此包的命名和清单指向的附件天然一致：标签
是 `v` 加上版本号，1.0.0 对应 `v1.0.0`。同一个版本号还会写进程序本身（它连同构建所用的
commit 一起报告），以及 Windows 构建的版本资源里；源码里也写着这个号，供解压出来、没有
检出元数据的源码树使用，并有测试把它和标签对齐。已经存在的 release 会直接沿用，因此同一条
标签推两次不会让运行失败——每个文件是**替换**同名附件而不是再挂一个，因为本实例对同名附件
是各存一条、并不覆盖。运行发现 release 上已经有附件时会明说：标签被重推通常意味着它被移动
过，也就是一个新版本顶着旧版本的版本号。
[.gitea/scripts/dedupe-release-assets.sh](.gitea/scripts/dedupe-release-assets.sh) 用来
收拾此前已经挂成两份的 release，保留每个名字下最新的那份附件。

### Debian、Fedora、RHEL、openSUSE 与 Arch

三个步骤，各发行版一样：装包、写账号、重启服务。

```sh
# Debian 与 Ubuntu
sudo apt install ./dist/packages/sdautologin_1.0.0-1_amd64.deb

# Fedora、RHEL、openSUSE
sudo rpm -i dist/packages/sdautologin-1.0.0-1.x86_64.rpm

# Arch
sudo pacman -U dist/packages/sdautologin-1.0.0-1-x86_64.pkg.tar.zst

# 下面两条在任何一种上都一样
sudo -u sdautologin /usr/bin/SDAutoLogin --home=/var/lib/sdautologin \
     --username=s1234567 --password=SECRET --isp=@zgyd --save config --show
sudo systemctl restart sdautologin
```

安装包会创建 `sdautologin` 用户、启用并启动服务；没有账号时它重试几次就会停下，
postinst 在发现还没有 `config.json` 时也会这么说。`/usr/share/doc/sdautologin/
README.Debian` 在机器上写着同样的步骤。卸载会留下设置和日志，purge 也一样：里面的账号
不是安装包有权扔掉的东西。

想看看装了什么：`rpm -K` 检查摘要、`rpm -qi` 看头信息，`rpm -e sdautologin` 卸载并留下
`/var/lib/sdautologin`；`pacman -Qi` 和 `pacman -Qkk` 作用于 Arch 包，后者会拿包里的文件
清单逐个核对。

### Windows

压缩包里只有程序、README 和许可证：没有安装程序，因为本来也没什么可安装的。解压到打算
长期放置的目录，双击运行即可；设置会写在程序旁边，所以这个目录就是要留住的东西。

`dist/packages/manifests/` 里放的是两个能安装这种压缩包的包管理器所要的东西：
`scoop/sdautologin.json` 与 winget 清单的三个文件。两者都把发布产物写成下载地址
`<项目地址>/releases/download/v<版本>/`，也就是本项目所在的 Gitea 存放发布附件的路径；
如果发布挪到别处，改 `tools/build/pack_win.go` 里的 `releaseURL`，两个清单都会跟着变。
winget 命令行本身就能校验它们
（`winget validate --manifest dist/packages/manifests/winget`）；Scoop 清单把设置和日志
声明为持久化，因此升级不会丢账号。

### systemd

[packaging/systemd](packaging/systemd) 里有两份现成的 unit，一份是系统服务，一份供
`systemctl --user` 使用；用户级那份开头就是它自己的安装步骤。系统级那份就是安装包里放的
文件，下面的步骤就是它做的事：

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

这份 unit 把设置和日志放在 `/var/lib/sdautologin`，并把进程限制在它需要的网络上。要用
浏览器配置那台机器，就在 `ExecStart` 里加上 `--settings=127.0.0.1:8088`，再用 SSH 转发
端口：`ssh -L 8088:127.0.0.1:8088 the-server`。然后打开日志里打印的那个地址（
`journalctl -u sdautologin` 能看到），而不是裸端口——地址里带着打开页面用的一次性 token，
不带它的请求会被拒绝。这个地址只能用一次，每生成一个新的都会进日志，所以最新的那行才是
能用的。

### 容器

[Dockerfile](Dockerfile) 编译同一个静态二进制，放进一个小镜像里以非特权用户运行，`/data`
放设置和日志：

```sh
docker build -t sdautologin .
docker run -d --name sdautologin --restart unless-stopped \
    -e TZ=Asia/Shanghai -v sdautologin:/data sdautologin
docker exec sdautologin SDAutoLogin --home=/data status --json
```

账号同样方式写下来，跑一次一次性命令即可：

```sh
docker run --rm -v sdautologin:/data sdautologin \
    --username=s1234567 --password=SECRET --isp=@zgyd --save config --show
```

`--home=/data` 是入口点的一部分，所以 `status` 之类的一次性命令和服务读的是同一份设置。
`status` 报会话不在线时，容器会被标记为不健康。

### OpenWrt

两个 MIPS 构建都会打包，一个字节序一个：

| 安装包 | 适用 |
| --- | --- |
| `sdautologin_<版本>-1_mips_24kc.ipk` | 大端板子，例如 Atheros 系（ath79） |
| `sdautologin_<版本>-1_mipsel_24kc.ipk` | 小端板子，例如 MediaTek 系（ramips、mt76x8） |

路由器没有 systemd，所以这个包安装一个 procd 启动脚本到 `/etc/init.d/sdautologin`，以及
一份 UCI 配置到 `/etc/config/sdautologin`。路由器的闪存也最少，所以程序以压缩形式装成
`/usr/lib/sdautologin/SDAutoLogin.gz`，`/usr/bin/SDAutoLogin` 是一个短脚本，第一次启动时
把它解到 `/tmp` 再从那里运行：闪存占 2.7 MiB 而不是 7.7，代价是运行时占用同样的内存。
`/tmp` 里的副本每次开机、每次升级各解一次，而且解出来的文件名里带版本号，所以新的包解
自己的程序，而不会是上一个包留下的那个。

```sh
go run ./tools/build openwrt
scp dist/packages/sdautologin_*_mipsel_24kc.ipk root@router:/tmp/
ssh root@router opkg install /tmp/sdautologin_*_mipsel_24kc.ipk
```

安装会启用服务，此后它随路由器一起起来。给它账号：

```sh
uci set sdautologin.main.username=s1234567
uci set sdautologin.main.password=SECRET
uci set sdautologin.main.isp=@zgyd
uci commit sdautologin
/etc/init.d/sdautologin restart
```

留空的选项不会被传下去，这时由 `config.json` 决定，也就是设置页写的那份。没有显示器的
路由器可以打开设置页，并从它所服务的网络访问：

```sh
uci set sdautologin.main.settings=0.0.0.0:8088
uci commit sdautologin
/etc/init.d/sdautologin restart
```

然后打开 `logread -e SDAutoLogin` 打印的地址，把里面的 `127.0.0.1` 换成路由器应答的地址
——页面在所有接口上都服务，但日志里写的是回环那个。只给端口会被拒绝：地址里带着一次性
token，而且只能打开一次，所以最新的那行才是能用的。地址不是回环时，token 就是唯一的保护，
请留在可信网络里或用隧道访问。

`/etc/config/sdautologin` 被声明为配置文件，所以升级会保住里面的账号；`config.json` 放在
`/etc/sdautologin`，那在 overlay 上，重启和升级都留得住。

日志文件在这里默认关闭，因为 `/etc` 在闪存上：程序把同样的行写进控制台，procd 交给
`logd`，`logread -e SDAutoLogin` 再从内存里读出来。想落成文件就打开它，代价是日志长到上限
后最多占八兆闪存：

```sh
uci set sdautologin.main.log_file=1
uci commit sdautologin
/etc/init.d/sdautologin restart
```

#### 编进固件镜像

路由器也可以出厂就带着它，而不是事后安装。`packaging/openwrt/Makefile` 就是为此准备的
OpenWrt 包，它需要的 Go 构建系统来自 packages 源：

```sh
cd $OPENWRT
./scripts/feeds update packages
./scripts/feeds install golang
mkdir -p package/sdautologin
cp -r /path/to/SDAutoLogin/packaging/openwrt/. package/sdautologin/
make menuconfig          # Network -> sdautologin
```

它把程序**不压缩**装进去：squashfs 会把里面的东西压一遍，而且压得比 gzip 好（这个程序
1.9 MiB 对 2.7），在那里再压一次只会给镜像白添一兆。原样放着还让它保持为只读文件系统里的
一个文件，而不是内存里的一份拷贝。启动脚本和 UCI 配置与上面那个包安装的是同样两个文件。

## 界面语言

`auto` 跟随系统语言，`zh` 和 `en` 各自强制一种。这个设置作用于托盘菜单、通知和设置页。
日志、命令行和源码注释一律保持英文，这样贴出来的日志谁都能读。

## 安全说明

- 设置服务只监听 `127.0.0.1`，要求一个每次运行新生成的随机 token，空闲即关闭。请求的每个
  阶段都有超时，token 的比较时间与"有多少字节相同"无关，每个应答（包括拒绝的）都带安全头。
- 那个 token 会随托盘交出的地址一起走，因为浏览器自己发起的导航请求没法带自定义头。页面
  发出去之前它就换成了会话 cookie，浏览器被送到不带它的同一页面，所以地址栏、历史记录、
  页面链出去的东西的 `Referer` 里都没有 token。之后的每个请求对着 cookie 校验，改动性的
  请求还必须把它另放进一个头里，而地址本身只能用一次。
- 无人值守登录要求密码在那里，所以除非关掉 `rememberPassword`，它以明文保存在
  `config.json` 里。这个文件和日志（日志里也写着账号）都是 `0600`，放在 `0700` 目录里。
  旧版本或拷贝留下的"别人可读"的文件会在启动时收窄，日志里说明发现了什么、做了什么；
  Windows 上权限位不起作用，改成给它一份只列出程序运行账号的访问列表。systemd 单元设了
  `UMask=0077`，系统服务还把状态目录保持在 `0700`。
- 设置页只写密码字段、从不读它：页面被告知"是否已设置密码"，而不是密码本身。所以密码既
  不在浏览器里，也不在页面加载的脚本里，也不在页面发出的请求里。字段留空保存就保留原来
  那个，这也是你没有动过的条目所提交的内容；只有本来就没有密码的条目（新加的，或改了
  账号名的）才需要你重新填。
- 程序从不调用注销接口，所以退出不会把机器留在离线状态。

## 项目结构

常规的 Go 布局：`cmd/` 放可执行文件，`internal/` 放构成它的包。模块之外什么都 import
不了，所以对外接口就是命令行和设置页。

| 路径 | 内容 |
| --- | --- |
| `cmd/SDAutoLogin/` | 入口：模式选择、托盘与无界面运行器、命令行、控制台附接 |
| `internal/portal/` | Dr.COM eportal 客户端 |
| `internal/service/` | 检测与重连循环、会话状态 |
| `internal/config/` | 配置文件、运营商列表、校验 |
| `internal/app/` | 托盘、页面与命令行共用的配置和服务，以及本地设置服务 |
| `internal/webui/` | 设置页及其嵌入的资源 |
| `internal/i18n/` | 界面文案与系统语言判断 |
| `internal/logging/` | 带滚动的日志器 |
| `internal/tray/` | 各平台的托盘，含 Win32 原语与图标转换 |
| `internal/platform/` | 系统胶水：打开文件、URL 或独立窗口，自启项，单实例 |
| `internal/brand/` | 图标绘制 |
| `winres/` | Windows 图标、版本与清单，以及由它们生成的资源文件 |
| `tools/icogen/` | 图标生成器 |
| `tools/build/` | 任务入口，脚本与 CMake 目标都用它 |
| `packaging/systemd/` | systemd 单元，系统服务与 `systemctl --user` 各一份 |
| `packaging/openwrt/` | OpenWrt 包、它的 procd 启动脚本和 UCI 配置 |
| `.gitea/workflows/` | 推送时 Gitea Actions 跑的东西：测试、打包以及对这些包的检查 |
| `Dockerfile` | 容器镜像，从 vendor 的源码构建 |
| `CMakeLists.txt` | 任务入口的 CMake 前端 |
| `dist/` | 构建产物，以及从那里启动的构建所用的设置与日志 |
| `build/` | 使用 CMake 时的构建目录 |
| `vendor/` | 依赖 |

## 常见问题

- **掉线之后什么都没发生。** 看 `logs/app.log` 里的 `session is down, reconnect attempt #N`
  那几行，以及紧随其后的门户应答。`ret_code` 为 `1` 表示账号或密码不对，`3` 表示门户忙。
- **图标一直是灰的。** 门户首页没有应答，状态是未知，程序什么都没提交。通常是因为这台
  机器已经不在校园网里了。
- **某次检测报未知。** 门户应答了，但应答里没有 `result` 字段——比如被强制门户拦了。最后
  一次应答就写在日志里。
- **门户换了地址。** 在「高级」页更新门户首页地址和认证接口地址。

## 许可

MIT，见 [LICENSE](LICENSE)。
