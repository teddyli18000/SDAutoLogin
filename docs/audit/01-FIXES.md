# SDAutoLogin 审查问题修复记录

- 分支：`fix/audit-hardening-2026`（自 `c5d8d5d` 起 9 个提交）
- 对应审查：`00-SUMMARY.md` 与 A–F 六份分报告
- 用户确认的范围：全部 S1–S4；按根因分提交；门户保持协议兼容（只加防护）

## 验收门禁（最终状态，全部实测）

```
go build ./...                            → 通过
go vet ./...                              → 通过
CGO_ENABLED=0 GOOS=windows go vet ./...   → 通过
gofmt -l cmd internal tools               → 零输出
TMPDIR=/var/tmp/sda-gate go test -race -count=1 ./...  → 13/13 包 ok
```

## 提交清单（按根因）

| # | 提交 | 内容 |
|---|---|---|
| 1 | `782e55e` | 日志层 `safeLine` 净化（伪造换行/ANSI 失效）；设置页令牌不再入日志与 `msg.stale` 文案；`Server.Addr()` |
| 2 | `ee5c38a` | 门户客户端：显式 `Proxy = nil`、首页同主机重定向上限 3 跳、登录应答状态码、非法 IP 拒发、超限报错、值级脱敏 |
| 3 | `09cb5bf` | 配置写入：拒符号链接、校验父目录属主、临时文件+rename 原子替换、ACL 失败不再丢弃、密码文件类型/尺寸/权限/BOM/歧义、装载路径校验 |
| 4 | `d0a33e6` | 设置服务：`Host` 头校验、`/static/*` 缓存头、`Content-Type` 校验 + `DisallowUnknownFields`、请求体不再能自带 `passwordSource` |
| 5 | `d3a6743` | 服务编排：账号间 300–500 ms 间隔、瞬时失败指数退避（上限 5 分钟、只加 jitter）、`Stop` 明确上界 |
| 6 | `8496c96` | 平台：单实例锁移出共享 `/tmp`、拒绝符号链接、Socket 对端身份校验、OpenWrt 以 root 执行前校验属主与权限、systemd 加固与文档 |
| 7 | `92154b6` | 命令行：未知选项不再回显原始参数、`--save=false` 语义、退出码区分、`--` 终止符、回显关闭失败告知、去除未使用参数 |
| 8 | `00f23d3` | 更新链：安装文件由**已签清单**决定、标签与清单必须一致、显式拒绝降级、`BuildVersion` 缺失即拒绝安装、forge 主机与 https 约束、跨主机重定向拒绝 |
| 9 | `65aae81` | 发布链与文档：签名后再校验、未签名清单判失败、CI 权限收敛与 token 不再进命令行/URL、`.dockerignore`/`.gitignore` 密钥模式、镜像 COPY 白名单、`verify` 顺序文档同步 |

## 覆盖率变化（实测）

| 包 | 审查基线 | 修复后 |
|---|---|---|
| `cmd/SDAutoLogin` | 38.3% | **72.7%** |
| `internal/platform` | 50.7% | **72.0%** |
| `internal/portal` | 80.2% | **87.3%** |
| `internal/service` | 93.8% | **95.1%** |
| `internal/logging` | 79.2% | **84.1%** |
| `internal/update` | 82.9% | **84.1%** |
| `internal/app` | 72.7% | **75.2%** |
| `internal/config` | 90.9% | 86.1% |
| `internal/tray` | 21.7% | 21.7%（未改善） |

`cli.go` 的 `parseOptions` 由 **0% → 100%**——F-01/F-02 就住在那里，此前没有任何测试执行过它。

## 已修复的高危项（S1）

1. **令牌落盘**：全仓 6 个把含 `?token=` 地址写进日志的位置全部清除（`settings.go` 的 `rollEntry` 与 remote-management、`main.go` 无条件首启、`tray_unix.go`、`cli.go` 两处）。新增 `Server.Addr()`（去掉 `/?token=`）与两个回归测试，其中一个断言 stderr 与日志文件都不含令牌。
2. **日志注入**：`logging` 层对整行做统一净化，伪造换行不再能造出第二条日志行，ANSI 失去 ESC 引导字节；普通中英文、路径、URL 一字未改（有逐字对照测试）。
3. **凭据重定向**：`ValidHTTPURL` 拒绝 `#`/控制字符；`LoadConfig` 开始校验并把不合法门户地址与 `accountPrefix` **复位为出厂默认**（即使调用方忽略错误，凭据也不会发往未校验的地址）。
4. **跨模块凭据外泄链**（B-S2-2，本次由 S2 上调 S1）：随第 3 项关闭——主机不再能无约束指向任意地址。
5. **环境代理**：门户客户端显式 `Proxy = nil`；以非回环地址 + 双向对照实测（标准 transport 确被代理拦下、门户客户端直连）。
6. **`/tmp` 单实例锁**：锁文件与套接字移出共享可写目录，拒绝符号链接，socket 校验对端身份。
7. **OpenWrt root 提权链**（本次唯一真实提权链）：包装脚本改为全路径调用工具、校验 `prog_dir`/`prog`/`partial` 的属主与写位、拒绝符号链接、先解压到 `$$` 暂名再 `mv` 就位。其模式位判定算术已单独实测（`drwxrwxr-x`/`drwxr-xrwx` 被正确判为可写而拒绝）。
8. **拼错的口令选项落盘**：未知选项不再回显原始参数值。
9. **一个周期内连发全部账号**：账号间加 300–500 ms 间隔与 jitter（诚实说明：这只消除「一个 RTT 一条」的连发，挡不住恶意门户一次性收走口令，根治在主机信任与 HTTPS）。
10. **降级与版本绑定**：安装哪个文件由**已签清单**的文件名决定，标签必须与清单一致，且显式拒绝非更新版本；`BuildVersion` 取不到即拒绝安装（fail-closed）。旧代码在「更旧的真签名发布」用例下会成功安装，这是被修掉的洞。

## 刻意未修 / 不可实现（连同理由）

1. **C-1 明文 HTTP + 密码在 URL query**：校园门户即 `http://10.9.1.3:801/eportal/` 且用 GET。按用户要求保持协议兼容，改为**明确告警**（`credentials are sent in clear text`）。真正收敛只有两条路：门户支持 https，或门户支持 POST 后由配置开关切换。
2. **`Retry-After` 未尊重**：`portal.LoginResult` 不携带该字段、`get()` 丢弃响应头，service 侧没有通道可读。接入点已就位（`noteTransientFailure`/`nextBackoff`），需 portal 契约加字段。
3. **无法主动取消在途门户请求**：`portal.Client` 无 `context.Context`，`Stop` 只能「设有界后放弃」。建议 portal 侧 `QueryStatus/Login` 接收 ctx。
4. **更新显示的版本号仍可伪造**：版本号的**显示**来自未签名的 tag；装错东西已被封死，但误导性显示仍在。彻底消除需把版本纳入被签字节，而清单由 `tools/build/pack.go` 写。
5. **镜像/runner 摘要未固定**（E-F6）：本机禁用 docker、无 registry 访问，凭空写摘要会直接打坏 CI。完成方式已写入报告。
6. **Dockerfile 显式 COPY 未实测**：已实现，但禁止 `docker build`，需人工复核一次。
7. **`internal/tray` 覆盖率停在 21.7%**：托盘实现以 Win32 为主，Linux 上无法执行；Windows 侧仍只有 `GOOS=windows go vet` 编译验证。
8. **Windows 专有路径零运行验证**：ACL（`SetNamedSecurityInfo`）、单实例命名对象、控制台回显恢复，均只有读码置信度。
9. **`s.cfg` 无锁读**：已由 `-race` 全绿与持锁捕获 `cfg/client` 覆盖，但未做形式化论证。

## 环境与过程说明

- **本次审查早期误判「本机无 Go 工具链」**（实际仅 Windows 侧没有；WSL 侧有 `go1.26.0`）。发现后已在规范检出目录补做全部门禁，但 **B/C/E/F 的部分运行时结论仍停留在静态阅读**。
- `/tmp` 曾是 23 G tmpfs 且 99% 占满（占用主体来自**其他会话**，未删）。所有测试统一改用 `TMPDIR=/var/tmp/sda-gate`，否则可能出现 ENOSPC 假红。
- 提交前清除了两处审查过程泄漏到仓库根的临时产物（游离二进制 `SDAutoLogin`/`SDAutoLogin.exe`，以及一个以脚本正文为文件名的探针残留）。