# 部署指南

[English](DEPLOYMENT.md) | **简体中文**

部署用 `run` 模式：没有托盘也没有显示，一直检测，日志写到标准错误，收到 `SIGTERM`
就退出。没有账号时它拒绝启动，账号得提前写好：在命令行加 `--save`，或者让服务带
`--settings` 启动，再从设置页填。

一次发布包含的是同一个程序，只是不带托盘：每个发行版一个安装包、一个 Windows 压缩包、
包旁边的 `SHA256SUMS` 和 `SHA256SUMS.sig`，以及用
[Dockerfile](Dockerfile) 构建的容器镜像。

## 目录

- [交叉编译](#交叉编译)
- [安装包](#安装包)
- [Debian、Fedora、RHEL、openSUSE 与 Arch](#debianfedorarhelopensuse-与-arch)
- [Windows](#windows)
- [systemd](#systemd)
- [容器](#容器)
- [OpenWrt](#openwrt)

## 交叉编译

交叉编译覆盖 Windows 和 Linux，架构包括 `amd64`、`arm64`、`386`、`armv7`、`mips`
与 `mipsle`。`armv7` 指 32 位 ARM 版用的指令集；`mips-softfloat` 和
`mipsle-softfloat` 给没有浮点单元的路由器用。MIPS 两种字节序都构建了：Atheros 系
（ath79、ar71xx）是大端，MediaTek 系（ramips、mt76x8）是小端。

每个 Linux 目标都会编译两份。第二份的文件名里带 `-notray`：去掉托盘，也连带去掉
D-Bus 客户端和其余只有托盘才需要的东西，体积小约五分之一（`mipsle` 从 10.2 MiB
降到 8.3 MiB）。这份给路由器用，这种构建里 `--app` 会直接报错。保留托盘的那份是
默认版本，给有桌面的机器。

在路由器上，以及任何没有桌面的机器上，都用 `run`：托盘需要桌面会话，设置页要用
`--settings` 从网络上访问。

macOS 是例外：它的托盘依赖 cgo，必须在装了 Xcode 命令行工具的 macOS 上编译
（`xcode-select --install`）。

Windows 可执行文件里带着图标、版本信息和 DPI 感知清单，它们由
`winres/rsrc_windows_<arch>.syso` 嵌入，一个架构一套（`amd64`、`arm64`、
`386`）。图标和这些资源文件都是生成的，来自源码里的绘图代码和
`winres/winres.json`；用 `icon`、`resources` 任务就能重新生成，文件都写在 `winres/` 里。

## 安装包

`go run ./tools/build packages` 会为程序支持的每个发行版打包：

| 发行版 | 安装包 | 安装方式 |
| --- | --- | --- |
| Debian、Ubuntu | `sdautologin_1.0.0-1_amd64.deb` | `apt install ./...` |
| Fedora、RHEL、openSUSE | `sdautologin-1.0.0-1.x86_64.rpm` | `dnf install` 或 `rpm -i` |
| Arch | `sdautologin-1.0.0-1-x86_64.pkg.tar.zst` | `pacman -U` |
| OpenWrt | `sdautologin_1.0.0-1_mips_24kc.ipk`、`..._mipsel_24kc.ipk` | `opkg install`，见 [OpenWrt](#openwrt) |
| Windows | `SDAutoLogin-1.0.0-windows-amd64.zip` | 解压即可 |

每个包装的东西一样，只是按各发行版的方式摆放：不带托盘的构建、运行它的 systemd
单元、声明 `sdautologin` 用户的文件、许可证和文档。设置与日志放在
`/var/lib/sdautologin`，安装包会启用服务，得先有账号它才能正常工作。
OpenWrt 不一样：没有 systemd，也没有 `/var/lib`，见它自己那一节。

这些都不需要装打包工具链。Arch 包是例外：有 `zstd` 就用它，没有就退到 `xz` 或
`gzip`，输出里会说明用了哪个。

包都没有签名。本地安装不受影响：`dpkg -i`、`rpm -i`、`pacman -U` 都接受，pacman
只在配置要求签名且 `LocalFileSigLevel` 不是 `Optional` 时才抱怨。要自建仓库，得
自己准备一把密钥，构建任务不管这件事。包旁边还会写一份 `SHA256SUMS`：按
`sha256sum` 的格式一行一个文件。

这份清单会签名，签名验过，程序才装。发布时用 ed25519 私钥签 `SHA256SUMS`，把
`SHA256SUMS.sig` 挂在旁边；程序拿编译进去的公钥验签名，再从这份验过的清单里
读压缩包的摘要。构建里没有公钥时，程序只报出新版本，不装。

### 签名密钥

密钥只做一次，做在一台会长期保存它的机器上：

```sh
go run ./tools/build keygen
```

它把私钥写到用户配置目录下的 `signing.key`（`0600`，Linux 上是
`~/.config/sdautologin`，Windows 上是 `%AppData%\sdautologin`），或者写到
`SDAUTOLOGIN_SIGNING_KEY_FILE` 指定的文件；公钥写进
`internal/update/publickey.go`，后者随改动一起提交。私钥刻意放在仓库外面：挨着源码
的密钥等于树里每个副本、每份编辑器临时文件和每次备份里都有一份。私钥文件的内容填进
Gitea 仓库密钥 `SDAUTOLOGIN_SIGNING_KEY`，打标签时由发布任务读取。

签名只从 `SDAUTOLOGIN_SIGNING_KEY` 读私钥，别的都不读。工作区里没有任何文件会被读
取，所以能读到一份源码并不等于能签出一份所有已安装副本都认可的发布。

签名这一步每次打标签都会跑，读到的密钥和 `publickey.go` 里的公钥不是一对时就让整
个运行失败：密钥没配好的标签什么都不发。发出一个没人能校验的版本，比不发更糟。这
一步也可以从 Actions 页面手动跑一次，那次会打包、校验并签名，在发布之前停下。
编译时还没有密钥的构建报出新版本，什么都不装。

每一版 release 都用同一把密钥签名。密钥已经存在时 `keygen` 拒绝运行；待签的私钥和
`publickey.go` 里的公钥不是一对时，`sign` 也拒绝，所以不会发出一个没人能装的
release。

签名是最后一个动 `dist/packages` 的步骤，`verify` 跑在它后面：`sign` 自己会把
`SHA256SUMS` 重写一遍，好让签名覆盖目录里真实的那些包，两步之间不能再打包。签完之
后又重写过的清单不会留着那份签名——它会被删掉，而不是跟一份自己对不上的文件一起发
出去——而 `verify` 碰到对不上的清单和签名就会失败。顺序就是关键：`verify` 拿磁盘上
紧挨着清单的那份签名去比对，所以只有签名在场时它才真的检查到了东西。跑在签名步之前
时它根本找不到签名，于是照样通过——这正是它现在排在签名之后的原因，也是「清单里没有
签名」现在算失败、而不是算一句提醒的原因。

轮换：把旧的 `signing.key` 移开，重新做一对，提交新的公钥，把新的私钥内容填进
仓库密钥。之后构建的程序会拒绝旧密钥签的 release，拒绝信息里两把密钥的编号都写
出来。反过来一样：还带着旧公钥的副本装不了新 release，这些机器要手工升一次。

`go run ./tools/build verify` 会用各个格式自己的工具把包读回来（deb 和 ipk 用
`dpkg-deb`，rpm 用 `rpm -K` 和 `rpm -qpl`，Arch 包用 `bsdtar` 和它自带的文件
清单），把 Windows 压缩包和生成它的构建结果比对，再确认 Scoop 与 winget 清单里的
摘要指向的是当前这个压缩包。最后把所有包再打一遍，只要有一个字节不同就报错。各格
式自己的工具得先装上，所以它只在 Linux 上跑：缺哪个会列出来，一个包都没检查成功
的运行会失败。

以上这些只在推送标签时由 [.gitea/workflows/ci.yml](.gitea/workflows/ci.yml) 跑
一遍，连同 `go vet`、`go test` 与 `gofmt` 检查。除此之外没有别的触发方式：
分支上的改动在本地用同一批任务检查。

推标签就是发布 release：同一次运行会创建它，并把各个包连同 `SHA256SUMS`、
`SHA256SUMS.sig` 挂上去，两个安装清单都指向那里。版本号从这条标签读出来，标签
是 `v` 加上版本号，1.0.0 对应 `v1.0.0`。同一个版本号还会写进程序本身（它连同
构建所用的 commit 一起报告），以及 Windows 构建的版本资源里；源码里也写着这个
号，解压出来、没有检出元数据的源码树就用它，也有测试把它和标签对齐。已经存在的
release 会直接沿用，同一条标签推两次不会让运行失败：每个文件会**替换**同名附件；
本实例对同名附件各存一条，并不覆盖，运行发现 release 上已经有附件时会明说。
[.gitea/scripts/dedupe-release-assets.sh](.gitea/scripts/dedupe-release-assets.sh)
用来清理此前已经挂成两份的 release，保留每个名字下最新的那份附件。release 的正文
就是 `CHANGELOG.md` 里对应标签的那一节，运行会从文件里读出来，找不到就拒绝发布。

那一节是给「要不要升级」的人看的：写对他们变了什么、他们需要做什么。这一版改了
项目自己的东西，比如测试有多少个、动了哪几份文档，写在提交记录里就够了，不必在这
里单列。

wiki 也在同一次运行里从仓库重新生成：把使用手册、更新日志和这份部署指南复制进
去，`docs/wiki/` 放的是只有 wiki 才有的页面。

## Debian、Fedora、RHEL、openSUSE 与 Arch

三个步骤，各发行版都一样：装包、写账号、重启服务。

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
postinst 在发现还没有 `config.json` 时也会这么说。机器上的
`/usr/share/doc/sdautologin/README.Debian` 写着同样的步骤。卸载会把设置和日志留下，
purge 也一样，账号就在设置里。

想看看装了什么：`rpm -K` 检查摘要，`rpm -qi` 看头信息，`rpm -e sdautologin` 卸载
并留下 `/var/lib/sdautologin`；`pacman -Qi` 和 `pacman -Qkk` 用在 Arch 包上，后者
会拿包里的文件清单逐个核对。

## Windows

压缩包里只有程序、README 和许可证，没有安装程序。解压到打算长期放置的目录，双击
运行即可；设置会写在程序旁边，这个目录得留着。

也只有这份压缩包里的副本能给自己装更新，前提是打开 `checkForUpdates`。装不装看
文件归谁管，不看平台：能写自己所在目录、又不是包管理器放进去的副本会替换自己。
Scoop 和 winget 装的会被认出来（程序旁边有 `install.json`，或路径在
`scoop/apps`、`windowsapps` 下），程序不碰它们。其它平台上程序都装在包管理器管
的包里，检测只报出新版本，并说明升级交给包管理器。Linux 服务在
`ProtectSystem=strict` 下运行，写不了自己启动时的那个文件。

`dist/packages/manifests/` 里放的是两个包管理器要用的清单，它们都能装这种压缩包：
`scoop/sdautologin.json` 与 winget 清单的三个文件。两者都把发布产物写成下载地址
`<项目地址>/releases/download/v<版本>/`，由本项目所在的 Gitea 提供发布附件；如果
发布挪到别处，改 `tools/build/pack_win.go` 里的 `releaseURL`，两个清单都会跟着变。
winget 命令行本身就能校验它们
（`winget validate --manifest dist/packages/manifests/winget`）；Scoop 清单
把设置和日志声明为持久化，升级不会丢账号。

## systemd

[packaging/systemd](packaging/systemd) 里有两份现成的 unit，一份是系统服务，一份
给 `systemctl --user` 用；用户级那份开头写着自己的安装步骤。系统级那份和安装包里
的文件相同，下面的步骤就是它做的事：

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
systemctl reload sdautologin
journalctl -u sdautologin -f
```

单元里写着 `ExecReload=/bin/kill -HUP $MAINPID`。`run` 收到 `SIGHUP` 就重读
`config.json`、重启检测循环，程序不退出，`systemctl reload sdautologin`
就能让手改过的配置生效。`SIGINT` 和 `SIGTERM` 仍然退出程序。

这份 unit 把设置和日志放在 `/var/lib/sdautologin`，并把进程限制在它需要的网络上。
要用浏览器配置那台机器，就在 `ExecStart` 里加上
`--settings=127.0.0.1:8088`，再用 SSH 转发端口：
`ssh -L 8088:127.0.0.1:8088 the-server`。然后打开设置页启动时打印到控制台的那个地址
（`journalctl -u sdautologin` 能看到，服务的控制台输出就落在这里）。只写端口不行：
地址里带着一个一次性 token，页面就靠它打开，请求不带它进不去；地址用过一次就作废，
挑最新的那条用。这个地址**故意不写进 `logs/app.log`**：日志比 token 活得久、还会被到
处复制，把地址留在那里，等于留下一条比这次打开更长寿的入口。

单元也可以把密码交过来，让它不进 `config.json`：

```ini
LoadCredential=password:/etc/sdautologin/password
```

它指向的文件里，登录列表不止一条时每行写一个 `account:password`；只有一条时，
整份文件就是那一条的密码。服务把密码放在 `/etc/sdautologin/password`：
只有这一处存着服务的密码，这个文件也不会被人拷来拷去。配成 systemd 凭据后，密码
不会落进程序读取的持久文件：源文件留着，每次启动服务时由 systemd 复制进
私有的 tmpfs。

## 容器

[Dockerfile](Dockerfile) 编译同一个静态二进制，放进一个小镜像里以非特权用户
运行，`/data` 放设置和日志：

```sh
docker build -t sdautologin .
docker run -d --name sdautologin --restart unless-stopped \
    -e TZ=Asia/Shanghai -v sdautologin:/data sdautologin
docker exec sdautologin SDAutoLogin --home=/data status --json
```

账号也用同样的方式写下来，跑一次一次性命令就行：

```sh
docker run --rm -v sdautologin:/data sdautologin \
    --username=s1234567 --password=SECRET --isp=@zgyd --save config --show
```

`--home=/data` 是入口点的一部分：`status` 之类的一次性命令和服务读的是同一份
设置。`status` 报会话不在线时，容器就处于不健康状态。

## OpenWrt

两个 MIPS 构建都会打包，一个字节序一个：

| 安装包 | 适用 |
| --- | --- |
| `sdautologin_<版本>-1_mips_24kc.ipk` | 大端板子，例如 Atheros 系（ath79） |
| `sdautologin_<版本>-1_mipsel_24kc.ipk` | 小端板子，例如 MediaTek 系（ramips、mt76x8） |

路由器没有 systemd，这个包把一份 procd 启动脚本装到 `/etc/init.d/sdautologin`，
以及一份 UCI 配置到 `/etc/config/sdautologin`。程序压缩后装成
`/usr/lib/sdautologin/SDAutoLogin.gz`，`/usr/bin/SDAutoLogin` 是一个短脚本，第一次
启动时把它解到 `/tmp` 再从那里运行：闪存只占约 2.9 MiB（直接存原文件是 8.3 MiB），
代价是运行时占用同样多的内存。`/tmp` 里的副本每次开机、每次升级都会重新解一次，
解出来的文件名里带版本号。

```sh
go run ./tools/build openwrt
scp dist/packages/sdautologin_*_mipsel_24kc.ipk root@router:/tmp/
ssh root@router opkg install /tmp/sdautologin_*_mipsel_24kc.ipk
```

安装会启用服务，此后它随路由器一起启动。给它账号：

```sh
uci set sdautologin.main.username=s1234567
uci set sdautologin.main.password=SECRET
uci set sdautologin.main.isp=@zgyd
uci commit sdautologin
/etc/init.d/sdautologin restart
```

`password` 会把密码写进进程列表，路由器上每个用户都看得到。`password_file`
让程序改从这个文件里读：

```sh
printf '%s\n' SECRET > /etc/sdautologin/password
chmod 600 /etc/sdautologin/password
uci set sdautologin.main.password_file=/etc/sdautologin/password
uci commit sdautologin
/etc/init.d/sdautologin restart
```

包不会创建这个文件。文件里每行一个 `account:password`，只有一条时整份文件就是
那一条的密码，权限设成 `0600`。启动脚本把它传成 `--password-file`，没有配文件
时才传 `--password`，只配了后者的机器照旧能用。`/etc/config/sdautologin` 也按
`0600` 安装。

留空的选项不会往下传，这时由 `config.json` 决定，那份文件是设置页写的。路由器没有
显示器，可以在它服务的网络里打开设置页：

```sh
uci set sdautologin.main.settings=0.0.0.0:8088
uci commit sdautologin
/etc/init.d/sdautologin restart
```

然后打开 `logread -e SDAutoLogin` 打印的地址，把里面的 `127.0.0.1` 换成路由器应答
的地址：页面在所有接口上都提供服务，但日志里写的是回环那个。只给端口不行，可用的
地址是日志里最新出现的那条。地址不是回环时，token 就是唯一的保护，请让它留在可信
网络里，或者用隧道访问。

`/etc/config/sdautologin` 是声明过的配置文件，升级时里面的账号会保留；`config.json`
放在 `/etc/sdautologin`，它位于 overlay 上，重启和升级都留得住。

日志文件在这里默认关闭：程序把同样的行写进控制台，procd 把它们交给 `logd`，
`logread -e SDAutoLogin` 再从内存里读出来。想让日志落到文件里就打开它，代价是日志
长到上限后最多占八兆闪存：

```sh
uci set sdautologin.main.log_file=1
uci commit sdautologin
/etc/init.d/sdautologin restart
```

每次检测的明细同样默认开着，也可以用同样的方式关掉。`quiet` 会省掉每十五秒一条的
检测明细，只留下报告状态变化的那几行：路由器跑顺之后就该这样，否则这些明细一小时内
就会填满 logd 的环形缓冲，把其它服务的日志挤出去。排查登录失败时把它设成 `0`。

```sh
uci set sdautologin.main.quiet=0
uci commit sdautologin
/etc/init.d/sdautologin restart
```

### 编进固件镜像

路由器也可以出厂就带着它，不必事后安装。`packaging/openwrt/Makefile` 就是为此
准备的 OpenWrt 包，它需要的 Go 构建系统来自 packages 源：

```sh
cd $OPENWRT
./scripts/feeds update packages
./scripts/feeds install golang
mkdir -p package/sdautologin
cp -r /path/to/SDAutoLogin/packaging/openwrt/. package/sdautologin/
make menuconfig          # Network -> sdautologin
```

它把程序**不压缩**装进去：squashfs 会把里面的东西压一遍，而且压得比 gzip 好
（这个程序约 2.1 MiB 对 2.9 MiB）。启动脚本和 UCI 配置，跟上面那个包安装的是同样
两个文件。
