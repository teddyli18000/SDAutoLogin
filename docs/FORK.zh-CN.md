# 本仓库的来源与发布

上游项目：[zqlan/SDAutoLogin](https://git.zqlhub.com/zqlan/SDAutoLogin)，保留原作者和 MIT 许可证。

- 稳定版基线：上游 `v1.2.0`，提交 `93013529f621e510428cf7324a9a66b02372e02f`，源码树 `4c0cd626f2ca100e69c0c1c1eb1e078156127049`。
- 后续 alpha 基线：用户的 [web-ingest 镜像](https://github.com/teddyli18000/web-ingest/tree/main/git-mirror/data/git.zqlhub.com/zqlan/SDAutoLogin)，上游提交 `919675abc03d5697879b1143086cf834e465a22b`，源码树 `36b5240b418c559bd5025731cb46e2bdb851f26c`。镜像只保存工作树，未导入其 Git 历史。
- `main` 保存上游代码：先记录稳定版，再叠加上述 alpha 快照。仅增加本仓库的 GitHub CI 和此来源说明，不合入 Linux 使用环境的定制代码。
- `linux-portable` 保存基于该 alpha 快照的 Linux 桌面目录部署、自启动和配置面板入口。
- `v1.2.0` 由本仓库 GitHub Actions 独立构建上游原版，作为 Latest；它不是上游原作者上传的二进制。
- `v1.3.0-alpha.2` 是本仓库 Linux 定制分支的首次预发布，不替换稳定版 Latest。构建提交号区分本仓库与上游构建。

发布附件来自 CI，附带 SHA256 校验清单。保留上游程序行为与上游地址；本仓库附件不冒充上游签名更新包。Linux 部署配置默认关闭自动更新检查，升级时人工下载并核对附件。

不提交车端账号、密码、实际 config.json、日志或构建产物。发布和 CI 通过不等于已完成实车部署或校园网登录验证。
