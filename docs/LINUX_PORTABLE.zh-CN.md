# Ubuntu 18.04 桌面便携版

此压缩包适用于 Ubuntu 18.04 的 amd64 或 arm64 桌面，运行时使用 Python 3.6 和 systemd 237。安装器以当前桌面用户运行，不需要 sudo；它会把 `SDAutoLogin/` 放到实际桌面目录下，创建当前用户的 systemd 服务，并立即启动。

基于镜像源码 `919675abc03d5697879b1143086cf834e465a22b`。程序使用静态 `notray` 构建，设置页面仍可使用；不依赖系统托盘。amd64 使用 `GOAMD64=v1`。本分支只增加部署文件和 CI，不改动上游登录逻辑。

解压后运行：

```sh
python3 install.py
```

默认安装根目录为 `xdg-user-dir DESKTOP` 返回的位置下的 `SDAutoLogin/`。若该命令不可用，安装器使用 `~/桌面`（目录存在时），否则使用 `~/Desktop`。也可以指定安装根目录：

```sh
python3 install.py --home "$HOME/桌面/SDAutoLogin"
```

安装根目录及其中的 `config.json`、`logs/` 和应用状态目录都由当前用户独占。XDG 配置、数据、状态、缓存、运行时与临时目录分别位于 `logs/config/`、`logs/data/`、`logs/state/`、`logs/cache/`、`logs/runtime/` 和 `logs/tmp/`。安装器只在 `config.json` 不存在时生成空账号模板，不写入用户名或密码。程序可在没有账号时启动，打开安装根目录中的“配置面板.desktop”后再填写设置。记住密码时，程序会把配置保存在安装根目录的 `config.json` 中。

服务单元位于安装根目录的 `.startup/`。系统只在当前用户的 `~/.config/systemd/user/` 下建立服务链接和 `default.target.wants/` 启用链接。服务以 `UMask=0077` 运行，重启间隔为 15 秒，标准输出和标准错误都丢弃；程序自己的应用日志保存在安装根目录的 `logs/app.log`。

首次启动时，程序生成的设置页一次性地址只会写入权限为 `0600` 的 `logs/runtime/settings-entry.url`。双击安装目录内的“配置面板.desktop”会通过 `xdg-open` 打开它；启动器成功交给桌面环境后会删除这个一次性地址，之后同一浏览器用会话 cookie 访问 `http://127.0.0.1:8088/`。如果浏览器清除了该会话 cookie，可运行 `systemctl --user restart sdautologin.service`，再打开配置面板以取得新的入口。

如果桌面仍提示启动器未经信任，右键该文件选择“允许启动”。配置面板只监听本机 `127.0.0.1:8088`，请在 Linux 桌面的浏览器打开。

重新运行安装器时，已有的 `config.json` 会保留；辅助脚本、桌面启动器、许可证、说明文件和服务单元会更新。若安装目录已有与新压缩包不同的二进制，安装器会停止并报告，不会静默继续运行旧版本。确认升级后，用新压缩包运行：

```sh
python3 install.py --replace-binary
```

该选项先停止当前服务，再原子替换二进制并重新启动服务；`config.json` 仍会保留。若机器启动时尚未登录桌面，而仍需让用户服务启动，可选地执行：

```sh
sudo loginctl enable-linger "$USER"
```

查看运行状态与重启服务：

```sh
systemctl --user status sdautologin.service
systemctl --user restart sdautologin.service
```

清理时先运行 `systemctl --user disable --now sdautologin.service`，删除 `~/.config/systemd/user/sdautologin.service` 链接并运行 `systemctl --user daemon-reload`，再删除安装器报告的整个安装目录。如果设置过 `XDG_CONFIG_HOME`，服务链接位于该目录的 `systemd/user/` 下。仅在其他用户服务也不需要 linger 时，使用 `sudo loginctl disable-linger "$USER"` 恢复。

CI 验证程序在 Ubuntu 18.04 启动、Python 3.6 辅助脚本及面板生命周期、安装目录布局和重复安装保留配置。安装器检查使用隔离目录与模拟 systemctl，不代表已验证真实开机启动、图形浏览器交互或实车校园网登录。
