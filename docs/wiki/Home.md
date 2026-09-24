# Campus Auto Login

A tray program that keeps a campus network (Dr.COM eportal) session alive. It asks
the portal every few seconds whether you are still online, and signs you back in
when you are not. It never logs you out.

- **[The manual](Manual)** ([简体中文](Manual.zh-CN)) — installing it, the settings
  page, the command line, the configuration file, and what to look at when
  something goes wrong.
- **[The changelog](Changelog)** ([简体中文](Changelog.zh-CN)) — one section per
  version, newest first.
- **[Releases](https://git.zqlhub.com/zqlan/SDAutoLogin/releases)** — a package for
  Debian, Fedora, Arch and OpenWrt, an archive for Windows, and `SHA256SUMS` to
  check a download against.
- **[Issues](https://git.zqlhub.com/zqlan/SDAutoLogin/issues)** — a line out of
  `logs/app.log`, such as `session is down, reconnect attempt #N`, answers most
  questions before they are asked.

This wiki is written from the repository: every release regenerates it from
`README.md`, `CHANGELOG.md` and `docs/wiki/`, so an edit made here is lost. Change
the repository instead.
