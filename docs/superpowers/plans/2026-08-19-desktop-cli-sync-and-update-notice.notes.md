# 取证记录

## P3：Gatekeeper 拦不拦释出到 `~/.local/bin` 的二进制

**结论：成立。** 释出的二进制能正常执行，Gatekeeper 不拦。
本 plan 的 Task 5–8 可以按原设计进行。

日期 2026-08-19 · 被验对象 `v0.3.0-rc10` 的 `handoff-desktop.app` · macOS 26.5 (Darwin 25.5.0) / arm64

### 判据本身有两条是错的，先记这个

原 plan 的 Step 6 列了四项判据，其中**第 3、4 项对裸可执行文件不适用**：

- **`xcrun stapler validate` 报 `does not have a ticket stapled to it`——这不是缺陷。**
  苹果只支持把公证票据装订到 `.app` / `.dmg` / `.pkg`，**裸 Mach-O 可执行文件无法装订**。
  内嵌的 CLI 是裸二进制，所以它永远不会有票据。它的公证覆盖来自签名本身 +
  在线校验，不来自装订。
- **`spctl -a -t exec` 报 `rejected` ——读它给的理由。**
  原文是 `rejected (the code is valid but does not seem to be an app)`，同一行明说了
  `the code is valid`。`spctl -t exec` 对非 bundle 的命令行工具就是这么报的，
  拒的是"这不是个 app"而不是"签名有问题"。

**教训**：判据要先确认它适用于被验对象。这两条若照原样当门用，会把一条成立的
路径判成不成立，然后整份设计被推倒重做——代价远大于验错方向。

### 真正的判据与原文

**① 隔离属性有没有被传染到释出的二进制上** —— 没有：

    $ xattr -l /tmp/p3-AighdN/.local/bin/handoff
    com.apple.provenance:

只有 `provenance`，**没有 `com.apple.quarantine`**。这是本条成立的根本原因：
Gatekeeper 对可执行文件的评估由隔离属性触发，没有它就不评估。

对照——被释出它的那个 `.app` **是**带隔离属性的（见下面「怎么复现」第 2 步）：

    com.apple.provenance:
    com.apple.quarantine: 0281;00000000;;81984FE3-0B55-4E64-A2DD-58FD78AB807F

即：隔离属性传染到了 `.app`，但**没有**继续传染到它写出的文件上。

**② 签名** —— 在，且是硬化运行时：

    CodeDirectory v=20500 size=39347 flags=0x10000(runtime) hashes=1224+2 location=embedded
    Signature size=9041
    Authority=Developer ID Application: zhu xuemei (FZ8VD4RF7B)
    Authority=Developer ID Certification Authority
    Authority=Apple Root CA
    TeamIdentifier=FZ8VD4RF7B

**③ 真的执行一次** —— 这是唯一无法被前两项替代的判据：

    $ env -i HOME=/tmp/p3-AighdN PATH=/usr/bin:/bin:/usr/sbin:/sbin \
        /tmp/p3-AighdN/.local/bin/handoff version
    v0.3.0-rc10
    revision  4aa47ef6c57eaac957257fdbac6c9caba987c8b3  2026-08-19T06:02:28Z
    go        go1.26.1
    platform  darwin/arm64
    退出码=0

没有 SIGKILL、没有「无法打开」对话框。

**④ 顺带复核一条既有契约** —— 跑 `handoff version` 没有在隔离 HOME 里留下
`config.yaml`（`ls: /tmp/p3-AighdN/.handoff/: No such file or directory`）。
这是 `cmd/root.go:301` 那个守卫：若它失效，任何东西跑一次 version——脚本、
包管理器、监控、CI、桌面壳探测——都会在用户的 `~/.handoff` 留下一份从未与任何
agentd 配过对的配置，此后 `shell.Resolve` 永远判「已配置」，图形向导再也不出现。

### 怎么复现（两个坑，不避开就是假绿）

**坑一：`gh release download` 不设 `com.apple.quarantine`。**
它只留 `com.apple.provenance` 和一个 diskimages 校验和。而 P3 问的恰恰是隔离
属性会不会传染。拿 `gh` 下的 DMG 直接测，必然"通过"，且完全测不到真实用户的
处境（用户是浏览器下载的）。必须手工补上：

    xattr -w com.apple.quarantine "0081;$(printf %x $(date +%s));Safari;$(uuidgen)" xxx.dmg

补完挂载、`ditto` 拷出 `.app`，可见 `0281;...` 且 UUID 与 DMG 的一致——传染链成立。

**坑二：只隔离 HOME 不够，PATH 也要隔离。**
`ResolveBinPath` 会搜 PATH。第一次跑只设了 `HOME`，子进程继承了操作者的 PATH，
于是它找到了**真实的** `/Users/xushixin/.local/bin/handoff`，判成 `use-existing`
直接不释出——整个验证空转，而日志看着一切正常：

    释出决策 decision=use-existing existing=/Users/xushixin/.local/bin/handoff
      existing_version=web-console-0c75a56c9+b161fix embedded_version=v0.3.0-rc10

正确的跑法是 `env -i HOME=... PATH=/usr/bin:/bin:/usr/sbin:/sbin ...`，此时才是：

    释出决策 decision=install existing="" existing_version="" embedded_version=v0.3.0-rc10
    内嵌二进制已释出 dst=/tmp/p3-AighdN/.local/bin/handoff perm=0755

### 一个与设计直接相关的旁证

上面那次空转照出：操作者本机装的 handoff 自报版本是
`web-console-0c75a56c9+b161fix`，**不是 `vX.Y.Z` 形态**。`CompareVersion` 解析不出，
`DecideRelease` 走保守分支 `use-existing`。

即：**对开发构建的二进制，同步永远不会触发。** 这是设计要的行为（判不出就不覆盖
用户手上的），不是缺陷。但走查时必须先把被验机器换成 release 构建，否则会看到
`plan=skip` 而误以为功能没生效。已写进 Task 12 的走查清单要求。
