# 安全策略

## 报告漏洞

**请不要用公开 issue 报告安全问题。**

用 GitHub 的私密通道：仓库 [Security → Report a vulnerability](https://github.com/Xsxdot/handoff/security/advisories/new)。
它只有仓库维护者可见，可以在修复发布前讨论细节。

报告里请尽量包含：受影响的版本（`handoff version` 的输出）、复现步骤、
以及攻击者能拿到什么。有 PoC 更好，但没有也照收。

这是一个业余时间维护的项目，**不承诺响应时限**。收到后我会尽快确认，
修复发布时会在 CHANGELOG 里注明并致谢（不想具名请说明）。

## 支持的版本

只有最新发布版接受安全修复。旧版本请先 `handoff upgrade`。

## 威胁模型（先读这段再判断是不是漏洞）

handoff 的设计前提是**你信任参与的每一台机器和每一个人**。以下几条是
**已知且有意为之**的设计取舍，不是漏洞：

- **agentd 是明文 HTTP/WS + Bearer token，没有 TLS。** 拿到 token 就等于能在
  执行机上派发任意代码执行。它被设计成跑在可信内网或虚拟组网（如 Tailscale）
  之后。README 的「连接远程执行机」一节写明了这条红线：**带公网 IP 的云主机
  现阶段不要当执行机**，或用防火墙把端口收窄。把 agentd 直接暴露到公网导致的
  后果不算漏洞，算误用。
- **token 明文存在 `~/.handoff/config.yaml`。** 保护它的是文件权限，
  和大多数 CLI 工具的凭据文件同级。
- **executor 在执行机上跑 AI 生成的代码。** 这是这个工具的**用途**本身。
  权限闸（`permission_request` 工单）是给协调者一个判断的机会，不是沙箱。
  没有沙箱、没有系统调用过滤——不要在放着敏感数据的机器上派发不受信的计划。
- **`handoff run` 会在任务仓库里执行你给的命令**，走 `sh -c`。这是设计功能。

反过来，**这些属于漏洞，欢迎报告**：

- 不带有效 token 就能调 agentd 的任何写接口，或读到任务内容
- 一台机器的 token 能操作到另一台机器/另一个项目的资源
- 任务 id、项目名、分支名等输入能穿越到预期目录之外（路径穿越）
- 权限闸能被 executor 侧的输入绕过（比如伪造成已批准的工单）
- 升级链路上的完整性问题：校验和绕过、降级攻击、把非发布产物装进来
- token、密钥被写进日志、事件流或错误报文
- `install.sh` / `install.ps1` 里能被中间人利用的问题

## 分发与完整性

- 每个 release 都带 `checksums.txt`（sha256）。
- macOS 资产做 Developer ID 签名与公证。裸命令行工具无法内嵌公证票据
  （Apple 的 stapler 只支持 `.app` / `.dmg` / `.pkg`），所以首次运行需要联网
  让系统去 Apple 校验——这是固有限制，不是配置问题。
- **Windows 资产没有 Authenticode 签名**（需另购 OV/EV 证书），SmartScreen 会
  提示未知发布者。请用 `checksums.txt` 核对下载物的 sha256 再运行。
- 一行安装脚本由 `handoff.gosuper.dev` 重定向到本仓库 `main` 分支的
  `install.sh` / `install.ps1`，两者都会校验下载物的 sha256 后才安装。
