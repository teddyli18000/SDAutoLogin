# Windows 测试首次运行结果（Phase 1）

## 为什么这是新的证据

此前 CI 只有 `CGO_ENABLED=0 GOOS=windows go vet ./...`，**Windows 测试从未运行**。
本轮用 WSL interop（`/proc/sys/fs/binfmt_misc/WSLInterop` = enabled）交叉编译测试二进制
并在 Windows 上真实执行：

```
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go test -c -o <name>.test.exe <pkg>
cd <Windows 目录> && ./<name>.test.exe -test.count=1
```

结果：**8 个包零失败，3 个包共 17 个失败**。

| 包 | Windows 失败数 |
|---|---|
| `tools/build` | 9 |
| `cmd/SDAutoLogin` | 6 |
| `internal/config` | 2 |
| `internal/app` / `i18n` / `logging` / `portal` / `service` / `update` / `webui` / `brand` | 0 |

## 分类（逐条判定，非猜测）

### 类别 1 · 测试在新平台上不成立（**不是代码缺陷**）

- **`cmd` 3 条**：`exec: "cmdSDAutoLogin.test.exe": cannot run executable found relative
  to current directory`。测试辅助 `runMain` 用 `os.Args[0]` 重新执行自身，而从 `./x.exe`
  启动时 argv[0] 会是裸文件名，Go 在 Windows 上拒绝执行相对名字（`ErrDot` 类防护）。
  涉及 `TestAMisspelledCredentialOptionIsNotWrittenDownAnywhere`、
  `TestACommandAfterAShortcutWordIsNotDropped`、`TestALoginStopsWhenAnotherCopyOwnsIt`。
  处置为改用 `os.Executable()` 让其**真实通过**（而非跳过）。补充：第三条另有一半成因
  首次记录时遗漏——`TestALoginStopsWhenAnotherCopyOwnsIt` 末尾还有一条「锁文件仍在」的
  断言，而 Windows 的单实例是会话内命名互斥体、没有锁文件；该断言已按平台守卫。
- **`cmd` 1 条**：`TestTheDirectoryChecksAnswerWhatTheyFind` 断言
  `systemBinDir("/usr/bin")` 为真——POSIX 路径，Windows 上不存在。/usr/bin:/usr/local/sbin。
- **`tools/build` 9 条**：`finding the repository: no go.mod found above C:\`。这些测试
  要从工作树向上找 `go.mod` 以读仓库版本；测试二进制在 `C:\...\Temp` 下运行时找不到。
  纯测试环境假设。

### 类别 2 · **真实代码缺陷**（Windows 专属逻辑缺失）

- **`cmd` 1 条** → 根因：`makePrivate()`（`cmd/SDAutoLogin/main.go:350-364`）在 Windows 上
  **是错的**：
  ```go
  if err := os.Chmod(dir, 0o700); err != nil { ... }   // Windows 上 mode 位无意义
  if mode := info.Mode().Perm(); mode&0o077 != 0 {
      return fmt.Errorf("%s is mode %#o, which other users can reach and which this
  file system will not change; pass --home=DIR ...", dir, mode)
  }
  ```
  实测（`TestThePerUserConfigDirectoryIsMadePrivate`，cli_test.go:2212）：
  ```
  perUserDir: C:\Users\lanzh\AppData\Roaming\sdautologin is mode 0777,
  which other users can reach and which this file system will not change;
  pass --home=DIR to name a directory of your own
  ```
  **影响真实用户**：`perUserDir`（`main.go:276`）是「程序装在自己所在目录不能写」时的
  回退路径（例如装在 `C:\Program Files`）。两条错误后果之一必然发生：
  ① `os.Chmod` 在 Windows 上不失败但也不改变 ACL，mode 仍读作 0777 ⇒ **目录未被收紧**，
  而错误信息把「文件系统不会改」说得像外部限制，掩盖了代码没有做 ACL 这一步；
  ② 若某环境下 Chmod 失败，用户被告知去用 `--home=`，而实际上程序本可以自己设 ACL。
  Windows 上正确的做法是设置访问列表（`internal/config/protect_windows.go:16` 的
  `SetNamedSecurityInfo` 逻辑），而该实现**未导出**，`cmd` 无法复用。
- **`internal/config` 1 条** → `TestAPasswordFromAFileIsNotWrittenToTheConfig`
  （`passwordfile_test.go:162`）与 `cmd` 的
  `TestSavingAPasswordFileRemembersThePathAndNotTheSecret`（`cli_test.go:473`）。
  **已定级：测试缺陷，生产代码正确。** 初次记录为「待定级」，并误述为「dump 出的
  config.json 里 passwordFile 缺失」——**这一前提是错的**。实测 dump 出的文件里
  `passwordFile` 确实写入了：
  ```json
  "passwordFile": "C:\\Users\\lanzh\\AppData\\Local\\Temp\\...\\passwords",
  ```
  `marshal()`（`config.go:786-810`）只清 `Password` 与 `PasswordSource`，从不碰
  `PasswordFile`，值非空故 `omitempty` 不裁剪。真正原因是断言写法：
  `strings.Contains(string(data), file)` 拿**磁盘形态**路径（单反斜杠）去比 **JSON 文本**
  （反斜杠被转义成两个），在 Windows 上永远匹配不上；Linux 因 `/tmp/...` 无反斜杠而通过。
  处置：两处改为 `json.Unmarshal` 后比对字段值。

### 类别 3 · POSIX mode 断言（**测试缺陷，生产代码正确**）

- **`internal/config` 1 条**：`TestSavingMakesTheDirectoryItsSettingsGoIn`
  ```
  passwordfile_test.go:339: directory mode = 0777, want 0700
  passwordfile_test.go:352: directory mode after a second save = 0777
  ```
  生产代码**正确**：`configDirMode = 0o700` 确实传给了 `os.MkdirAll`
  （`config.go:611-633`），而 Windows 的真实保护来自文件 ACL
  （`restrictToOwner`，见 `config.go:540` 附近的注释）。NTFS 无法回报 POSIX 位，
  `os.Stat` 返回 0777，故该断言在 Windows 上**恒假**。至于共用助手
  `fileMode()`（`config_test.go:858-866`），**复核后更正**：原先判断「同一缺陷影响
  `config_test.go:802/814/847/1214` 等处」是**过时且不成立的**——那些调用点都已在既有的
  `runtime.GOOS == "windows"` 守卫内，或位于 `//go:build !windows` 的文件里；本轮唯一
  漏网的 Windows 侧 mode 断言就是 `passwordfile_test.go:339/352` 这两行（另有
  `tools/build/signing_test.go` 一处 `perm != 0o600`，修好前被 `repoRoot()` 的
  `t.Fatal` 掩盖）。

## 结论与处置建议

1. **`CI 从不运行 Windows 测试` 这条缺口的价值已被证实**：它掩盖了 1-2 处真实
   Windows 专属缺陷（首要为 `makePrivate`），以及若干只能在该平台暴露的测试缺陷。
2. 建议把「Windows 测试真实执行」纳入 CI，并按平台对上述测试分类处理：
   平台不成立的用例加 Windows 跳过（并说明原因），真实缺陷修生产代码。
3. `makePrivate` 的修法：让 Windows 构建使用访问列表（复用 `config/protect_windows.go`
   的机制，需导出一个目录版本），POSIX 构建保留 mode 检查；按 `cmd` 既有的
   `console_windows.go` / `console_other.go` 构建标签成对模式落地。
---

# Docker 构建复核（Phase 2）

## 结论：E2 的 Dockerfile 改动**已用证据确认，但 `docker build` 无法在本机完成**

## 无法完成的原因（环境，非代码）
`registry-1.docker.io` 不可达：
```
failed to resolve reference "docker.io/library/golang:1.26":
dial tcp 108.160.170.39:443: i/o timeout
```
镜像源探测结果：
| registry | 结果 |
|---|---|
| `registry-1.docker.io` | **Network is unreachable** |
| `docker.m.daocloud.io` | 可达；`alpine:3.21` 成功拉取并打回原名，但 `golang:1.26` 返回 **502 Bad Gateway**（重试仍 502） |
| `registry.cn-hangzhou.aliyuncs.com` | 可达，但无公开 `library/golang`（`pull access denied`） |
| `dockerproxy.com` | 不可达 |
| `mirror.ccs.tencentyun.com` / `hub-mirror.c.163.com` | DNS 解析失败 |

⇒ 缺 `golang:1.26` 基础镜像，`docker build` 无法开始。**要完成这一步需要可用的镜像源或代理。**

## 替代验证（已执行，直接命中两条待验性质）
既然无法构建镜像，就验证这条改动的**两条实质性质**：

1. **显式 COPY 列表是否完整**（E2 把 `COPY . .` 改成白名单，若漏项构建会失败）
2. **密钥材料是否确实进不了构建上下文**（安全性质）

方法：手工构造一个与 Dockerfile COPY 列表**完全等同**的目录，在其中种下诱饵
（`signing.key`、`internal/leaked.pem`），按 `.dockerignore` 的规则剔除，然后在该目录里
跑 Dockerfile 第 36 行真正执行的命令。

```
$ cp go.mod go.sum <ctx>/ && cp -r vendor cmd internal tools winres <ctx>/ + 5 个文档
$ echo DECOY > <ctx>/signing.key ; echo DECOY > <ctx>/internal/leaked.pem
$ (按 .dockerignore 剔除 signing.key 与 *.pem)
$ cd <ctx> && CGO_ENABLED=0 GOFLAGS=-mod=vendor go run ./tools/build build
  go build -trimpath -ldflags "-s -w -X sdautologin/internal/app.Version=1.3.0-alpha.2 \
    -X sdautologin/internal/update.BuildVersion=1.3.0-alpha.2" \
    -o <ctx>/dist/SDAutoLogin ./cmd/SDAutoLogin
  -> dist/SDAutoLogin (9.6 MiB)
build exit: 0
```

结果：
- **COPY 列表完整** —— 仅凭 `go.mod go.sum vendor/ cmd/ internal/ tools/ winres/` + 5 个文档
  即可完成构建并产出二进制，无需上下文里的任何其它文件。
- **诱饵被排除** —— `signing.key` 与 `*.pem` 不在上下文中，与 `.dockerignore` 的规则一致。
- 顺带确认 `-X sdautologin/internal/update.BuildVersion=...` 确实被工具链写入
  （与 E2「版本绑定」所需的 linker 变量一致）。

**残留缺口**：以上不覆盖两层基础镜像的拉取与 `COPY --from=build` 的分层行为。

## 后续：真实 `docker build` 已完成，并发现等价验证漏掉的一处真实缺陷

网络恢复后基础镜像可拉取，于是本轮补做了真实构建（`docker build` 全 19 步成功，
最终镜像可运行 `status`，输出与 Windows/Linux 一致）。**真实构建暴露了等价验证漏掉的一个
问题**：

```
# 种下诱饵后只构建 build 阶段并检查该阶段的层
$ docker build --target build -t sdautologin-audit:buildstage .
$ docker run --rm --entrypoint sh sdautologin-audit:buildstage -c 'ls /src/signing.key /src/internal/leaked.pem'
  absent : /src/signing.key          ← 根目录的通配符生效
  LEAK   : /src/internal/leaked.pem  ← 子目录里的 .pem 进了镜像层
```

**根因：`.dockerignore` 里不带斜杠的模式只匹配上下文顶层的名字。** `*.pem` 匹配
`./x.pem`，**不**匹配 `internal/x.pem`。E2 的 COPY 白名单挡住了根目录的 `signing.key`
（模式是 `signing.key`，恰好匹配），但任何落在 `internal/`、`cmd/`、`tools/` 等被 COPY
目录里的密钥材料都会进层——而「能拉取镜像的人都能读层」正是该文件自己的注释所担心的。

修法：给所有扩展名模式加 `**/` 前缀（保留裸 `signing.key` 作为顶层写法）：

```
signing.key
**/*.key
**/*.pem
**/*.p12
**/*.pfx
```

修复后实测：同样种下两个诱饵，重建 build 阶段，**两者均 `absent`**；完整镜像仍能构建并
运行。结论已写进 `.dockerignore` 的注释（附「这是实测得出、不是假设」）。

**这条是本轮最有价值的发现之一**，而且只有真实构建才抓得到：我上一轮的「COPY 等价上下文」
验证是手工按字面理解模式去剔除文件的，没有用 Docker 的真实匹配语义，因此漏判。
教训：**等价的模拟验证不能替代真实工具的行为**，尤其是通配符、忽略文件这类由工具自己
解释的语义。

## 顺带修正的文档不一致
`Dockerfile:15` 原写「打开 `docker logs` 打印的那个地址」。但本轮修复已把设置页地址从
**日志文件**移到**控制台（stderr）**，`docker logs` 仍然能看到（它同时含 stderr），
所以该指引要写准并给出可复制的命令；已改为：
```
# Open the page at the address the program prints on its console, which
# "docker logs sdautologin" shows, since that is where a container's output goes:
#   docker logs sdautologin 2>&1 | grep 'settings page'
# The address carries a one-time token and is replaced after every opening, so
# take the newest line; the bare port will not open it. The token is deliberately
# not written to logs/app.log, which outlives it.
```
---

# 处置结果（Phase 1b，已由调度者独立复现）

## Windows 真实执行：**0 失败**

我用自己的命令重新交叉编译并执行了全部 13 个测试包（`-test.v`，逐子测试计数）：

| 包 | pass | skip | fail |
|---|---|---|---|
| `internalconfig` | 86 | 8 | **0** |
| `cmdSDAutoLogin` | 78 | 1 | **0** |
| `internalportal` | 61 | 0 | **0** |
| `internalservice` | 59 | 0 | **0** |
| `internalapp` | 56 | 0 | **0** |
| `internalupdate` | 48 | 3 | **0** |
| `toolsbuild` | 31 | 8 | **0** |
| `internaltray` | 20 | 0 | **0** |
| `internallogging` | 10 | 3 | **0** |
| `internalbrand` | 9 | 0 | **0** |
| `internalplatform` | 8 | 0 | **0** |
| `internali18n` | 6 | 0 | **0** |
| `internalwebui` | 4 | 0 | **0** |
| **合计** | | | **0** |

改动前同一 census：`toolsbuild` skip=0/**FAIL=9**、`cmd` SKIP=1/**FAIL=6**、
`internalconfig` SKIP=8/**FAIL=2**。

## 真实代码缺陷的修法（已核实）

`makePrivate` 按平台拆分（新文件，参照 `console_windows.go`/`console_other.go` 的成对模式）：

- **Windows**（`cmd/SDAutoLogin/makePrivate_windows.go`）：调
  `config.RestrictToOwner(dir)` 设访问列表；**不再用 `os.Chmod`、不再读 `Mode().Perm()`**；
  失败信息改为诚实的「could not be given an access list that keeps it to the account this
  program runs as: …」，不再把「代码没设 ACL」说成「文件系统不会改」。
- **POSIX**（`cmd/SDAutoLogin/makePrivate_other.go`）：原样保留 `chmod 0700` + 回读
  `mode&0o077` 语义与原文错误信息（那里的措辞本来就准确）。
- `config.RestrictToOwner` 由原 `restrictToOwner` **导出**（仅首字母大写，3 个内部调用点
  同步更新），而不是新增一个目录专用函数：`SetNamedSecurityInfo(SE_FILE_OBJECT)` 对文件
  与目录是同一个 API，ACL 写入只应有一份实现。

**新断言不是空断言**（调度者核实实现 + 子代理探针实测）：
`assertDirectoryIsPrivate`（`private_windows_test.go`）用
`windows.GetNamedSecurityInfo(..., DACL_SECURITY_INFORMATION)` **读回**访问列表，要求
恰好 1 个 ACE 且其 SID 等于本进程令牌用户（`EqualSid`）；普通目录实测 `acecount=3`
（含 SYSTEM），`RestrictToOwner` 后 `acecount=1`。比原来的 mode 断言**更强**。

## 环境发现（对将来写测试有价值）

- `os.UserConfigDir()` 在 Windows 上**忽略 `XDG_CONFIG_HOME`**，读 `%AppData%`。凡
  `t.Setenv("XDG_CONFIG_HOME", …)` 的测试在 Windows 上会落到用户**真实**目录；本轮据此
  改掉了 `TestThePerUserConfigDirectoryIsMadePrivate`（除 `XDG_CONFIG_HOME` 外同时设
  `%AppData%`），并删除了审计轮遗留在真实 Roaming 目录的空 `sdautologin`。
- Windows 上 Go 拒绝执行**相对名字**的可执行文件（ErrDot 类防护），故「重新执行自身」的
  测试辅助必须用 `os.Executable()`，不能用 `os.Args[0]`。
- Windows 测试二进制可以直接由 PowerShell 执行，不必经 WSL；但 `.\x.test.exe -test.count=1`
  在 PowerShell 里会把 `.` 拆成参数，flag 需要加引号。