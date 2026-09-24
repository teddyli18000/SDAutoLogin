# 校园网自动登录

一个小托盘程序，让你的校园网（Dr.COM eportal）会话一直在线。它每隔几秒问一次门户"我还在
线吗"，掉线了就自动替你补上登录。它从不主动注销。

- **[使用手册](Manual.zh-CN)**（[English](Manual)）——怎么装、设置页怎么用、命令行、
  配置文件，以及出问题时该看哪里。
- **[更新日志](Changelog.zh-CN)**（[English](Changelog)）——一个版本一节，最新的在前。
- **[发布页](https://git.zqlhub.com/zqlan/SDAutoLogin/releases)**——Debian、Fedora、
  Arch、OpenWrt 的安装包，Windows 的压缩包，以及用来核对下载的 `SHA256SUMS`。
- **[问题反馈](https://git.zqlhub.com/zqlan/SDAutoLogin/issues)**——从 `logs/app.log`
  里摘一行（例如 `session is down, reconnect attempt #N`），多数问题在问之前就有答案。

本 wiki 是从仓库生成的：每次发布都会用 `README.md`、`CHANGELOG.md` 和 `docs/wiki/`
重新生成，所以在这里改的内容会被覆盖。请改仓库。
