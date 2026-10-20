# 真机验证：Windows 产品二进制（第 3 轮）

## 结论摘要

用项目自己 `dist/` 里已有的 Windows 二进制在真机（`DESKTOP-367AV2G`）上执行，**三项观察**：

1. `status` 走完整启动路径并按正确语义返回：真实 HTTP 尝试 → `portal unreachable … Get "http://10.9.1.3/": EOF`，退出码 1。
2. `status --json` 输出合法 JSON，字段完整。
3. **`config.json` 的访问列表在真机上确实只剩运行账户一项** —— 这是此前只有读码置信度的项，现已实测确认。

## 实测记录

```
> SDAutoLogin-windows-amd64.exe status
portal       : http://10.9.1.3/
reachable    : false
state_known  : false
online       : false
client_ip    : -
account      : -
online_since : -
detail       : portal unreachable at http://10.9.1.3/: Get "http://10.9.1.3/": EOF
exit=1

> SDAutoLogin-windows-amd64.exe status --json
{"portal":"http://10.9.1.3/","state":"unreachable","reachable":false,"known":false,
 "online":false,"clientIp":"","account":"","onlineSince":"","detail":"portal unreachable ..."}

> (Get-Acl config.json).Access
DESKTOP-367AV2G\lanzh : FullControl        # 恰好 1 个 ACE
```

日志行同样正确（`app dir`、`http get http://10.9.1.3/ failed after 5002 ms`），
说明启动路径、日志初始化、门户客户端与状态渲染在真机上端到端工作。

## 新发现的部署约束（值得处理）

**本机已启用 Smart App Control，它会阻断新构建的未签名可执行文件。**

```
> .\SDAutoLogin.exe --help
程序"SDAutoLogin.exe"无法运行: An Application Control policy has blocked this file

HKLM:\SYSTEM\CurrentControlSet\Control\CI\Policy
  VerifiedAndReputablePolicyState = 1     # 1 = Smart App Control 开启
C:\Windows\System32\CodeIntegrity\CiPolicies\Active\  # 另有 WDAC 策略
Get-AuthenticodeSignature → NotSigned
```

判定依据（已做对照实验）：

| 二进制 | 结果 |
|---|---|
| 本次新构建、放在 `%TEMP%` 的产品二进制 | **被阻断** |
| 同一个二进制改名为 `prod-copy.exe` 后 | **仍被阻断**（⇒ 与文件名无关） |
| Go 测试二进制（同一次构建、同样未签名） | **PASS, exit 0** |
| **项目 `dist/` 下已有的产品二进制**（同样 `NotSigned`） | **正常运行，exit 0** |

⇒ 阻断**不是**由名字或路径决定，而是 Smart App Control 按内容/信誉判定：它已放行
`dist/` 里那个（此前多次运行、已有本地信誉），而拒绝任何它没见过的行为者。
这解释了 `dist/` 二进制可跑、而新构建的副本不可跑。

**对本项目的意义**：SAC 在「已签名」与「信誉良好」之间放行，Go 测试二进制因内容形态被
放行，而**未签名的新发布二进制会被拒绝**。若用户机器启用了 Smart App Control（Windows 11
默认在新装机上启用），下载未签名发布可能根本无法运行。建议：
- 在文档的「安装/故障排查」里写明这一现象与解法（右键属性 → 解除锁定并不够；需签名，
  或用户手动关闭 SAC）；
- 发布流程考虑代码签名（当前只有更新链的 ed25519 签名，那是校验**更新**的，不是
  Windows 的 Authenticode 签名，两者解决的不是同一个问题）。

## 未能完成的部分与原因

- **托盘/UI 路径未验证**：`run` 会拉起托盘图标并阻塞，属交互式动作，不适合在用户桌面上
  无人干预地执行。
- **隔离的 `--home` 冒烟没做成**：我用 `$home` 作变量名，而 PowerShell 的 `$HOME` 是
  **只读**变量，赋值静默失败，命令退回了真实用户目录 `C:\Users\lanzh`。影响已清理
  （只留下默认 `config.json` 与 434 字节日志，都是我这次产生的，已删除）。
  复现时请改用别的变量名（如 `$datadir`）。
- **新构建的二进制在本机无法执行**，受上述 SAC 策略限制；本轮改用 `dist/` 里已有的
  二进制完成验证。若要在本机验证「刚构建的那一份」，需要关掉 Smart App Control
  或对该二进制签名。