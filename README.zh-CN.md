![校园网自动登录：圆章里的挂锁](logo.png)

这是个托盘程序，支持 Windows、Linux 和 macOS，用来让校园网会话一直在线。它每隔
几秒问一次门户「我还在线吗」，掉线就自动补上登录，从不主动注销。每个平台一个可执行
文件，不用装依赖。

[English](README.md) | **简体中文**

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
- [界面语言](#界面语言)
- [安全说明](#安全说明)
- [项目结构](#项目结构)
- [常见问题](#常见问题)
- [部署](#部署)
- [许可](#许可)

## 功能

- Windows、Linux 和 macOS 上的托盘图标，按会话状态着色。
- 设置页是本机上的一个网页，只监听回环地址，用浏览器打开。
- 中文或英文界面，默认跟随系统语言；日志保持英文。
- 选项分「常规」「高级」「关于」三页：「常规」是日常开关，「高级」是门户参数，
  「关于」是版本、更新检查和日志尾部。
- 没有桌面的机器可以用命令行模式。
- 登录成功或失败时通知：Windows 上是气泡，其它平台用系统通知。
- 开机自启，每个平台一个开关（注册表、桌面项、LaunchAgent）。
- 每次检测都写日志，日志会自动滚动，密码永远不写进日志。
- 可以查找新版本，默认关闭。能自己替换自己的副本会升级，包管理器装的只提示。

通知覆盖三种情况：登录成功、登录失败，门户拒绝把会话换到首选服务类型；检测失败
在同一段故障里只报一次，故障持续期间最多每五分钟提醒一次。程序里没有关闭通知的
设置，托盘菜单里也没有，只有操作系统自己的通知设置能关掉它。

## 工作原理

用的是门户登录页自己调的那几个接口：

| 步骤 | 请求 | 用途 |
| --- | --- | --- |
| 1 | `GET /` | 从门户首页读 `v4ip`，即门户看到的那个 IP（是 NAT 之后的，不是你本机的） |
| 2 | `GET ?c=Portal&a=online_list` | 问这个 IP 有没有通过认证；这个接口在校内、会话已掉时也照样应答 |
| 3 | `GET ?c=Portal&a=login` | 提交 `user_account` 和 `user_password`，带 `login_method=1` |

只有第 2 步说「门户认为你*不*在线」时才会尝试登录。门户连不上、或应答读不出可用
信息时，状态就是未知，什么都不提交。登录之后还会再问一次，「门户说接受了但仍显示
离线」按失败处理。

连通性探测打开后，结果只用于状态显示；要不要登录，只看门户本身。

打开 `checkForUpdates` 后，程序还会去发布页问有没有新版本；「关于」页上的按钮和
`update` 命令可以随时问一次，都不看这个开关。发布页连不上时只写一行日志，登录
检测照常。

## 编译

需要 Go 1.26 或更新。依赖全部 vendor 进来，离线也能编译。产物统一落在 `dist/`；
`clean` 只清掉那里的可执行文件。

一条命令：

```bat
build.cmd
```

```sh
./build.sh
```

也可以直接调用这些任务：

```sh
go run ./tools/build            # 列出所有任务
go run ./tools/build build      # 当前平台
go run ./tools/build console    # 控制台版，给脚本和服务用
go run ./tools/build all        # Windows 与 Linux 的全部目标
go run ./tools/build packages   # 下面所有安装包，输出到 dist/packages/
go run ./tools/build verify     # 用各格式自己的工具检查安装包，再重新打一遍
go run ./tools/build windows    # Windows 压缩包及其清单
go run ./tools/build deb        # Debian 安装包
go run ./tools/build rpm        # RPM 安装包
go run ./tools/build pacman     # Arch 安装包
go run ./tools/build openwrt    # OpenWrt 安装包，给路由器用
go run ./tools/build test       # go vet，对 Windows 也做一遍，再 go test
go run ./tools/build icon       # 按绘图代码重画图标
go run ./tools/build resources  # 重新生成 Windows 资源文件
go run ./tools/build clean
```

CMake 驱动同一批任务：

```sh
cmake -S . -B build -G Ninja
cmake --build build                      # 编译主程序
cmake --build build --target check       # go vet，对 Windows 也做一遍，再 go test
cmake --build build --target SDAutoLogin-console
cmake --build build --target all-platforms
cmake --build build --target packages
cmake --build build --target verify
cmake --install build --prefix /usr/local
```

CMake 自己不编译任何东西：编译器是 Go 工具链，构建参数都归任务入口管。

交叉编译和各目标的文件名：[docs/DEPLOYMENT.zh-CN.md](docs/DEPLOYMENT.zh-CN.md)。

## 使用

双击 `dist\SDAutoLogin.exe`，其它平台运行 `dist/SDAutoLogin`。程序会在托盘区显示
图标，在后台持续检测；首次运行、还没有保存账号时，设置页自己打开，页面前面还有一
个三步向导，依次问界面语言、账号密码和服务类型。点「以后再说」就把它关掉，它后面
的设置页上每一项也都能设。

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

`run` 模式下，进程收到 `SIGHUP` 也重读 `config.json`、重启检测循环。

## 设置

设置页在 `127.0.0.1` 的一个随机端口上提供服务，用一次性 token 保护，token 只能
由托盘发出。15 分钟没有请求，它就自己关掉、把端口还回去（空闲检查一分钟一轮，所以
最迟一分钟内关闭），下次打开页面时再启动。地址只能用**一次**。它不会写进日志：日志
比 token 活得久、而且会被到处复制，把地址留在那里，等于留下一条比这次打开更长寿的
入口。托盘会把地址交给它替你打开的浏览器；没有托盘的机器则从控制台拿到它。

没有托盘的机器上，用 `run --settings=ADDR` 让同一个设置页在你指定的地址上提供服
务，直到程序退出。要打开的地址会在设置页启动时打印到控制台。ADDR 不是回环地址时，
网络里就能访问，唯一的保护就是地址里的 token：留在可信网络里，或者走隧道访问。

**常规**

- 登录列表：每个条目是一个账号，连同它可以使用的服务类型：校园网本身、中国移动
  （`@zgyd`）、中国联通（`@cucc`）、中国电信（`@ctc`）。拖动条目或用方向键调整尝
  试顺序，见下一节。
- 密码是否写入配置文件。
- 界面语言。
- 掉线后自动登录。
- 启动时把已在线的会话换成该账号首选的服务类型。在条目卡片上可以改用该条目列出的
  任何其它服务类型；门户拒绝切换之后，卡片上会多出一个打开门户页面的按钮。
- 开机自动运行。
- 检测间隔，以及连通性探测。
- 是否查找新版本，以及允许升到哪些版本（`stable`，或 `prerelease` 提前试用）。

**高级**

- 门户首页地址和认证接口地址。
- 账号前缀，它与账号、服务类型一起拼成最终提交的账号。
- 请求超时和 `jsVersion` 参数。
- 详细日志。

**关于**

- 版本、平台、项目地址和许可证。
- `config.json` 和日志所在的位置，以及打开它们的按钮。
- 手动跑一次更新检查，以及上一次查到的版本。
- 日志的最后几行。

改动会把页面标记为未保存；**保存**会写入 `config.json`、重启检测循环，并立刻跑一
次检测。条目上的测试按钮要保存之后才可用。

## 账号与尝试顺序

门户对一个客户端地址只允许一个会话，多个账号不可能同时在线。这张列表是为第一个账
号失效的那天准备的（余额用完、密码被改、账号注销）：到那天程序会接着往下走，用下
一个能用的条目登录：

```
1  s1234567   中国移动，其次中国联通
2  s7654321   校园网
3  s0000003   中国电信                      （已停用）
```

1. 条目自上而下尝试，每条按它自己列出的顺序、对每个服务类型各提交一次：上面第 1
   行会先试 `s1234567@zgyd`，再试 `s1234567@cucc`，然后才轮到 `s7654321`。
2. 指出账号本身有问题的应答（密码错、认证失败）会把该条目搁置十分钟，然后继续下
   一条。保存配置或手动检测会立刻取消这个等待。
3. 针对门户而非账号的应答（门户忙、请求超时、应答读不出来）会保留那条条目，当轮
   到此为止。
4. 门户的 `ret_code` 决定应答归哪一组：`1` 和 `7` 算账号自己的问题，其余都算门
   户的问题，`11` 和程序没见过的码也在其中。所以只有 `1` 和 `7` 能让列表往下走：
   账号的问题用别的码报出来时，那条条目留在原位，后面的条目永远轮不到。
5. 页面会标出你的会话正在使用哪条；搁置的条目还会显示原因和剩余时间。

停用的条目完全不参与，可以停用它而不删掉密码。

**把已在线的会话换到首选服务类型。** 程序登录时按条目里服务类型的顺序依次尝试。
程序启动时会话已经在线（你手工登录的，或上一次运行留下的），门户不会自动换。一个
地址只允许一个会话，要换类型就得重新登录一次：用该条目的首选服务类型再登录一遍，
会话仍停在原处的话，一小时后再试一次。会话在线时门户会拒绝再次登录，这时程序会如
实说明，并保持原样，不停用该条目；会话仍然在线，设置页、顶部胶囊和托盘显示「需处
理」，不显示错误。

## 命令行

服务器、SSH、脚本场景用得上。命令行输出保持英文。

```text
SDAutoLogin [command] [options]

run            run in the background, no tray
status         print the current session state once and exit
update         look for a newer release and print what was found; --install
               installs it when this copy can replace itself, --json prints the
               same as one line for a script
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
--quiet            leave out the per-check detail, for a service whose console
                   goes into the system log
--account=USER:PASSWORD[@SERVICE[+SERVICE...]]
                   one entry of the login list; repeat the option to write
                   several, in the order they are to be tried
--username=NAME    use this account, as the single entry of the list
--password=SECRET  use this password instead of the configured one, which on its
                   own is the password of the first entry of the list
--password-file=PATH
                   read the passwords from PATH instead, in the two shapes the
                   passwordFile setting takes; a secret on the command line is
                   in the process list of every user on the machine, so this is
                   the form to use from a script, a service or a container
--isp=SUFFIX       service suffix such as @zgyd, empty for the campus network
--save             write what was given here into config.json as well
--settings=ADDR    run: also serve the settings page on ADDR, for a machine
                   with no tray and no display, for example 127.0.0.1:8088;
                   the address to open is printed on the console
--json             status: print one line of JSON instead of the report
--app[=ADDR]       open the settings page in a window of its own, without the
                   browser around it, on ADDR or on loopback
```

选项也可以写成 `--name value`，但值里含有 shell 会当作语法的字符（比如服务类型前
面的 `@`）时，请用上面那种写法。`--app` 是例外：它的地址可选，而且必须用 `=` 写；桌面
上直接写 `--app` 就行。

选项每次运行都会重新读取，只对这一次运行生效；加上 `--save` 才写进
`config.json`。`run` 在缺账号时会报错退出（除非用 `--settings` 给了它一个可以配置
的页面），不会来问你：没有终端的机器答不了问题。

账号相关的选项描述的是整张列表，不会往里追加，脚本可以反复执行同一条命令：

```sh
# 一条账号，服务与容器场景就是这样
SDAutoLogin run --account=s1234567:SECRET@zgyd --save

# 多条，顺序即尝试顺序；一条也可以写多个服务类型，用 @ 分隔
SDAutoLogin run --account=s1234567:SECRET@zgyd+cucc --account=s7654321:OTHER --save
```

密码里带 `@` 的没法用这种形式写（`@` 是服务类型的分隔符），得用 `--username` 和
`--password`。

把它当服务部署时，密码还有更合适的来源：命令行会出现在机器上每个用户的进程列表
里。`--password-file=PATH` 从文件里取密码，形态与下面配置段的 `passwordFile`
相同；systemd 单元用
`LoadCredential=password:/etc/sdautologin/password` 把密码交过来时，一个选项都不
用加：没有别的地方指定密码时，程序会自己去读 `$CREDENTIALS_DIRECTORY/password`。

同一个可执行文件有两种模式：不带子命令启动时，程序常驻托盘；带子命令时，做完那一
件事就退出。`-cli` 等同于 `run`，`-test-login` 等同于 `login`。

`--app` 保留托盘，但会在一个不带地址栏、标签页和菜单的浏览器窗口里打开设置页。程
序会启动 Chrome、Edge、Chromium 或 Brave，窗口里不再有其它浏览器部件；一个都没
装、或找到的那个起不来，就改用默认浏览器打开，并在日志里说明。

Windows 上该文件按 GUI 程序链接，双击不弹控制台窗口；从终端启动时，输出会接到该
终端。要把输出重定向到管道、或让脚本读取退出码，编译控制台版：同一份源码，去掉
`-H=windowsgui`。

```sh
go run ./tools/build console
```

`status` 的退出码：`0` 在线、`1` 状态未知或失败、`3` 门户说本机未登录。
`status --json` 把同一次检测打成一行 JSON，日志走 stderr，stdout 上只有 JSON：

```sh
SDAutoLogin status --json | jq -r .state
```

```json
{"portal":"http://10.9.1.3/","state":"online","reachable":true,"known":true,"online":true,"clientIp":"203.0.113.7","account":"s1234567","onlineSince":"2026-01-01 00:00:00","detail":"online"}
```

`state` 取 `online`、`offline`、`unknown`（门户应答了，但没有任何可用信息）或
`unreachable`（完全没有应答）。后两个共用退出码 `1`，靠这个字段区分。

## 配置文件

`config.json` 就在程序旁边，首次运行时按默认值创建。

| 字段 | 默认值 | 含义 |
| --- | --- | --- |
| `accounts` | `[]` | 登录列表，按尝试顺序排列 |
| `accounts[].username` | | 学号、用户名或邮箱 |
| `accounts[].password` | | 密码，仅在 `rememberPassword` 为真时写入 |
| `accounts[].disabled` | 无 | 存在且为 true 就停用该条目 |
| `accounts[].isps` | `[""]` | 形如 `@zgyd` 的服务类型后缀，按尝试顺序排列 |
| `rememberPassword` | `true` | 是否把密码写进文件 |
| `passwordFile` | | 密码从这个文件里读 |
| `portalBaseUrl` | `http://10.9.1.3/` | 门户首页，用来读客户端 IP |
| `eportalUrl` | `http://10.9.1.3:801/eportal/` | Dr.COM eportal 根地址 |
| `accountPrefix` | `,0,` | 门户页在浏览器登录时用的前缀 |
| `checkIntervalSeconds` | `15` | 多久检测一次；低于 5 秒或高于一天会被收回这个区间 |
| `httpTimeoutSeconds` | `6` | 请求超时；低于 2 秒或高于 15 分钟会被收回这个区间 |
| `autoLogin` | `true` | 会话断线时是否自动登录 |
| `switchToPreferredService` | `true` | 把已在线的会话换成其条目的首选服务类型 |
| `checkInternet` | `true` | 是否同时探测外网 |
| `verboseLog` | `true` | 记录每次检测的诊断信息 |
| `logToFile` | `true` | 是否同时写 `logs/app.log`，见 `--no-log`；这一项不在设置页上，闪存小的机器只需要定一次 |
| `probeUrls` | 见下 | 连通性检测地址 |
| `jsVersion` | `3.3.3` | 门户页面提交的参数 |
| `language` | `auto` | `auto`、`zh` 或 `en` |
| `checkForUpdates` | `false` | 是否去发布页查找新版本 |
| `updateChannel` | `stable` | `stable`，或 `prerelease` 提前试某个版本 |

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

`passwordFile` 指向一个文件，密码从那里读，不用写进 `config.json`。每行都没有
冒号时，整份文件就是一个密码，属于登录列表的第一条；有冒号时，每行一个
`account:password`，按第一个冒号切开，填进它点到的条目，没点到的沿用
`config.json` 里的密码。名字对不上任何条目时，程序写进日志，不去猜。密码本身只丢
掉行尾换行，它可能以空格结尾。读到的密码不会写回 `config.json`；加 `--save` 时，
写下来的是路径。

密码的来源，先有答案的那个生效：

1. `--password=SECRET`，第一条，仅本次运行。
2. `--password-file=PATH`，整个列表，仅本次运行。
3. `$CREDENTIALS_DIRECTORY/password`，systemd 凭据。
4. 配置里的 `passwordFile`。
5. `config.json` 里的 `password`。

命令行上的 `--password` 更具体，压过凭据。

程序把旧版本写的文件（单个 `username`、`password` 和 `accountSuffix`）读成列表里
的一条，下次保存时按新格式写回。

默认的探测地址是 `http://connect.rom.miui.com/generate_204`、
`http://www.baidu.com`、`http://www.msftconnecttest.com/connecttest.txt`。探测地
址正常应答、且不含门户特征时才算成功，门户拦出来的「假联网」不算外网可用。

自启项：Windows 上是注册表键
`HKCU\Software\Microsoft\Windows\CurrentVersion\Run`；Linux 上是
`~/.config/autostart/sdautologin.desktop`；macOS 上是
`~/Library/LaunchAgents/com.sdautologin.plist`。

## 文件位置

- `config.json`：设置。
- `logs/app.log`：日志，放在自己的目录里，超过 4 MiB 滚动到 `app.log.1`。
  `--no-log` 和 `logToFile` 都能不要这个文件，给闪存小的机器（路由器）用，同样
  的行仍然进控制台。

两者默认都在程序旁边：`dist/` 里的构建就把设置和日志留在那儿，移动可执行文件时得
把 `config.json` 一起搬。`--home=DIR` 把它们放进 DIR，systemd 单元和容器都这么
用。

## 界面语言

`auto` 跟随系统语言，`zh` 和 `en` 各自强制一种；这个设置作用于托盘菜单、通知和设
置页。日志、命令行、源码注释和提交信息一律保持英文，贴出来的日志或提交记录谁都能
读。

## 安全说明

### 设置服务

设置服务只监听 `127.0.0.1`，每次运行都生成一个新的随机 token，空闲即关闭。请求的
每个阶段都有超时；比较 token 的耗时固定，不会泄露已经对上了几个字节；每个应答
（包括拒绝的）都带安全头。

那个 token 会随托盘给出的地址一起传给浏览器：浏览器自己发起的导航请求没法带自定
义头。页面发出去之前，token 就换成了会话 cookie，服务端把浏览器送到不带它的同一
页面，地址栏、历史记录和页面链出去的请求里，`Referer` 都不带 token。之后的每个
请求对着 cookie 校验，页面上自己加载的脚本、样式表和图标也一样：没有拿到过地址的
程序，从这个端口上读不到任何东西。改动性的请求还必须把 cookie 另放进一个头里，而
地址本身只能用一次。

### 密码放在哪里

无人值守登录要求程序读得到密码。除非关掉 `rememberPassword`，它就明文存在
`config.json` 里。这个文件和日志（日志里也写着账号）都是 `0600`，放在 `0700`
目录里。旧版本或拷贝留下的「别人可读」的文件会在启动时收窄，日志里说明发现了什
么、做了什么；Windows 上权限位不起作用，改成给它一份只列出程序运行账号的访问列
表。systemd 单元设了 `UMask=0077`，系统服务还把状态目录保持在 `0700`。

密码可以放在 `config.json` 里，可以放在 `passwordFile` 指向的文件里，
也可以两边都不放，只由设置页或某次运行的命令行给出。密码文件让密码不进那个会被
拷贝、同步、贴进 issue 的文件。它不给 `config.json` 加密：登录时没有人守着，
程序只能自己读到密码。它挡不住以同一用户身份运行的进程，也挡不住 root，
密码还在文件系统上，就在被指到的那个文件里。重启之后什么都不用重做：
文件一直在；systemd 凭据的源文件也一直在，每次启动服务时由 systemd 复制进
一个私有的 tmpfs。`config.json` 里的 `password` 字段照旧支持，不做迁移。

程序不把密码交给操作系统自己的凭据库，这个结论是量出来的。发布的六种形态里，只
有 Windows 桌面不靠别的东西就能用：DPAPI 是系统的一部分。macOS 的钥匙串在
launchd 守护进程或 SSH 会话里是锁着的；Linux 桌面要有正在运行的 secret service
提供者，还得装上 `libsecret-tools`；Linux 服务在用户会话之前就启动了；容器和
OpenWrt 路由器则没有会话总线。它还会跟程序的分发方式打架：文件夹拷到另一台机器，
带过去的是一个存在却读不出来的密码。

### 设置页知道什么

设置页只写密码字段、从不读它：页面只知道密码有没有设置过，密码本身从不发过去，
也不在页面加载的脚本或它发出的请求里。字段留空保存，密码就还是原来那个；新加的
条目、或改过账号名的条目本来就没有密码可保留，需要重新填。

### 更新

只有先证明「这份发布出自本项目」，程序才会安装它。`SHA256SUMS` 用 ed25519 签名，
签名拿编译进程序的公钥校验，压缩包的摘要再从这份验过的清单里读出来。两样都成立之
前，不往程序文件上写任何东西。

公钥写在 `internal/update/publickey.go`，由 `go run ./tools/build keygen` 生
成。公钥为空的构建（刚克隆下来、还没跑过它的）照旧查找新版本，把结果写进日志，
什么都不装。

私钥由同一条命令写到用户配置目录下的 `signing.key`，或者写到
`SDAUTOLOGIN_SIGNING_KEY_FILE` 指定的文件，不会写进仓库。签名发布时只从环境变量
`SDAUTOLOGIN_SIGNING_KEY` 读取：工作区里的任何文件都不读，所以能读到一份源码并不
等于能签出一份所有已安装副本都认可的发布。发布流程见
[docs/DEPLOYMENT.zh-CN.md](docs/DEPLOYMENT.zh-CN.md)。

### 退出登录

程序从不调用注销接口，退出不会让机器掉线。

## 项目结构

常规的 Go 布局：`cmd/` 放可执行文件，`internal/` 放它用到的各个包，模块之外 import
不到。对外接口只有命令行和设置页。

| 路径 | 内容 |
| --- | --- |
| `cmd/SDAutoLogin/` | 入口：模式选择、托盘与无界面运行器、命令行、控制台挂接 |
| `internal/portal/` | Dr.COM eportal 客户端 |
| `internal/service/` | 检测与重连循环、会话状态 |
| `internal/config/` | 配置文件、运营商列表、校验 |
| `internal/app/` | 托盘、页面与命令行共用的配置和状态，以及本地设置服务 |
| `internal/webui/` | 设置页及其嵌入的资源 |
| `internal/i18n/` | 界面文案与系统语言判断 |
| `internal/logging/` | 带滚动的日志器 |
| `internal/tray/` | 各平台的托盘，含 Win32 原语与图标转换 |
| `internal/platform/` | 各平台适配：打开文件、URL 或独立窗口，自启项，单实例 |
| `internal/brand/` | 图标绘制 |
| `winres/` | Windows 图标、版本与清单，以及由它们生成的资源文件 |
| `tools/icogen/` | 图标生成器 |
| `tools/build/` | 任务入口，脚本与 CMake 目标都用它 |
| `packaging/systemd/` | systemd 单元，系统服务与 `systemctl --user` 各一份 |
| `packaging/openwrt/` | OpenWrt 包、它的 procd 启动脚本和 UCI 配置 |
| `docs/` | 部署指南，以及 wiki 里不复制仓库文件的页面：首页与侧栏 |
| `.gitea/workflows/` | 打标签时 Gitea Actions 执行的任务：测试、打包以及对这些包的检查 |
| `.gitea/scripts/` | 上面这些任务调用的脚本：wiki 页面生成与 release 附件清理 |
| `Dockerfile` | 容器镜像，从 vendor 的源码构建 |
| `CMakeLists.txt` | 任务入口的 CMake 前端 |
| `dist/` | 构建产物，以及在那里启动的构建所用的设置与日志 |
| `build/` | 使用 CMake 时的构建目录 |
| `vendor/` | 依赖 |

## 常见问题

- **掉线之后什么都没发生。** 看 `logs/app.log` 里的
  `session is down, reconnect attempt #N` 那几行，以及紧随其后的门户应答。
  `ret_code` 为 `1` 表示账号或密码不对，`3` 表示门户忙。
- **图标一直是灰的。** 门户首页没有应答，状态是未知，程序什么都没提交。通常是这
  台机器已经不在校园网里了。
- **某次检测报未知。** 门户应答了，但应答里没有 `result` 字段，比如被强制门户拦
  了。最后一次应答就写在日志里。
- **门户换了地址。** 在「高级」页更新门户首页地址和认证接口地址。
- **Windows 完全起不来这个程序**，提示被某个应用程序控制策略拦下，或者双击之后什么都
  没发生。Windows 11 可能开着 Smart App Control，它会拒绝一个自己没见过的程序：发布
  出来的是**没有用 Windows 代码签名证书签过名**的，所以刚下载的那份没有信誉可供判定。
  同一个文件夹里跑过一次的副本通常之后就能启动——这也解释了为什么就地构建的那份一直
  能用、而把它复制到别处就不行。根治办法是给发布签名；替代办法是关掉 Smart App
  Control。

## 部署

安装包、systemd、容器与 OpenWrt：
[docs/DEPLOYMENT.zh-CN.md](docs/DEPLOYMENT.zh-CN.md)。

发布由 [.gitea/workflows/ci.yml](.gitea/workflows/ci.yml) 负责；systemd 单元在
[packaging/systemd](packaging/systemd)，容器在 [Dockerfile](Dockerfile)。
[dedupe-release-assets.sh](.gitea/scripts/dedupe-release-assets.sh)
用来清理重复挂上的 release 附件。

## 许可

MIT，见 [LICENSE](LICENSE)。
