# B417 breakdown 节点台账（2026-09-28）

本文件记本节点实验过程与原始读数；结论文本在 `docs/superpowers/specs/b417-breakdown.md`。
卡 B417（Android/iOS 原生移动壳），有效基线 `origin/cards/B417-spec`。

## 0. 开工状态位

- 当前分支 `cards/B417-charter-2`；`git rev-parse HEAD` = `5894f5ac`（= contract 提交）。
- `origin/cards/B417-spec` 实读 `a9ba59b5`；`git merge-base --is-ancestor a9ba59b5 HEAD` → YES（spec 提交为 HEAD 祖先）。
- 工作树 clean（`git status --short` 空）。
- spec 头部「状态：已批准（2026-09-28）」；contract 头部「冻结状态：本提交随本文件…冻结」+「有效基线 cards/B417-spec @ a9ba59b5」——均为本工作树实读。

## 1. 环境与工具链读数

```
$ uname -a
Linux ... x86_64 GNU/Linux        # 本工作树为 linux，无 Xcode/Android SDK
$ which codegraph
/usr/local/bin/codegraph
$ go version
go version go1.26.1 linux/amd64
$ find . \( -name '*.kt' -o -name '*.swift' \) | grep -v node_modules | wc -l
0                                  # 全仓零 Kotlin/Swift（与 spec 问题陈述一致）
$ ls mobile/android mobile/ios
ls: cannot access 'mobile/android': No such file or directory
ls: cannot access 'mobile/ios': No such file or directory
```

## 2. 图查询（codegraph，本机二进制）

```
$ codegraph --repo . summary
本仓库有代码图：5315 节点 / 6639 边 / 26 领域（codegraph/）。

$ codegraph --repo . domains   # 顶层（parent 空）：d_cli d_collab d_execution d_gateway d_keystone
                               # d_ledger d_maintenance d_orchestration d_policy d_protocol d_scheduling
                               # d_sessions d_transport d_web d_workspace
$ codegraph --repo . check         # exit=0，fails=[]（warns 为基线既存 best-dangling/budget-raised/legacy 等）
$ codegraph --repo . resolve --doc docs/superpowers/specs/b417-contract.md   # exit=0，锚均 moved
$ codegraph --repo . resolve "internal/mobilecore/core.go#Core.Activate"     # anchor=vanished, line=0
   → 该符号未入图（既存图覆盖债，见 contract §1.4），本稿改用普通路径+行号，不使用 #Symbol 锚。
$ codegraph --repo . resolve "internal/agentd/authroutes.go#sessionCookie"   # anchor=ok, line=343
$ codegraph --repo . resolve "internal/proto/pairing.go#EncodePairBundle"    # anchor=moved, line=111
$ codegraph --repo . resolve "cmd/console.go#runConsolePair"                 # anchor=moved, line=121
```

## 3. 见证现状事实的读数（本节点亲跑）

```
$ cd mobile && go test ./bind/ -count=1
ok  github.com/Xsxdot/handoff/mobile/bind  8.904s        # 绑定面冻结测试（含 shell_api_golden / export_surface）绿
$ grep -n "consoleQR\|consoleBundle\|runConsolePair\|renderBundleQR" cmd/console.go
46: consoleQR bool / 47: consoleBundle bool / 75: runConsolePair(cmd) / 121: func runConsolePair ...
   → console --qr/--bundle 已实现（B369 交付），壳配对载体生产者就位。
$ grep -n "Token\|Dial\|Credential\|wantBindSurface" mobile/bind/export_surface_test.go
   → 负向断言在位（绑定面禁止导出 Token/Dial 的绕过门禁入口）。
$ grep -n "修订记录" docs/superpowers/specs/b369-contract.md
372:## 修订记录（breakdown 出稿轮，2026-09-14）   # b369 有先例；b417-contract 尚无→P7 提议新增
$ grep -n "iOS XCFramework 尚未" mobile/README.md
   → mobile/README.md 仍写「iOS XCFramework 尚未在装了完整 Xcode 的机器上跑过（未验项）」，
     与 spec 备注「2026-09-28 本机 Xcode 27 gate 双端 PASS，旧读数就此闭合」矛盾 → 既存文档漂移，P7。
```

## 4. 本条执行的判断与放弃的尝试

- **不重跑工具链 gate**：本工作树 linux 无 SDK/Xcode，spec/contract 已记 darwin gate 双端 PASS，故本稿以既有读数为据，壳构建一律标「未验证，需真机」（§6）。未把未跑的 iOS/Android 构建写成结论。
- **执行机落地方案（P1）**：正面回答 spec 备注/contract §8#1 要求——charter 派发列绑小队（pro=cline@linux-01，
  runner=mimo@linux-01）且 `effectiveCovers` 强制 target/executor 空，无法把壳子卡定点到 macOS。给出三案：
  甲=协调者 macOS 本机 Subagent-Driven（推荐，先例 B288 `b288.md:153`）；乙=小队改绑 macOS 开发机；
  丙=分平台/真机侧另办（弃）。据此设计 DAG：A（Android）/B（iOS）依赖既有 `mobile/build.sh` 与 `console --qr/--bundle`，
  可并行；I（父卡 integrate）依赖 A、B，跑构建闸 + iOS 模拟器竖切。DAG 不因甲/乙而变（只换执行载体）。
- **P5（minSdk 21 × EncryptedSharedPreferences 下限）**：本机无 Android SDK，**未能核对** `androidx.security:security-crypto`
  的 minSdk；不作断言，标「未验证，需 macOS 核对」；若确认冲突则子卡暂停回 contract。
- **P6（明文回环）**：Android API 28+ 默认禁明文、iOS ATS 是跨平台假设族的命中项；本机无 SDK，具体 API 归子卡核对，
  本稿只定「最小放行仅 loopback」的姿态。
- **不建卡、不派发、不调 handoff CLI、不起 executor**（纪律块）。

## 5. 出稿自检读数

```
$ codegraph --repo . resolve --doc docs/superpowers/specs/b417-breakdown.md
{"anchors":[
 {"ref":"mobile/bind/bind.go#Pair", ... "anchor":"moved"},
 {"ref":"internal/agentd/auth.go#sessionCookieName", ... "anchor":"moved"},
 {"ref":"internal/agentd/authroutes.go#sessionCookie", ... "anchor":"ok", "nodeId":"n_agentd_sessionCookie"},
 {"ref":"internal/proto/pairing.go#EncodePairBundle", ... "anchor":"moved"},
 {"ref":"cmd/console.go#runConsolePair", ... "anchor":"moved"}]}
resolve_exit=0        # 无坏锚
```

- 产出四样齐全：子系统清单带类型（图外新建两项标逻辑+边界双型）；契约增量逐条结论（§2.3 无强退回、P5 条件性退回、§2.4 六条澄清）；A/B/I 三卡四段式、判据行为化；缺陷族逐族含「无，因为……」。✓
- 待拍板「P1–P7」集中于稿首 §0；「未验证，需真机」汇总 §6 七条；每卡有界文件集核过（A=`mobile/android/**`、B=`mobile/ios/**`、I=无新增源）。✓

## 6. 提交事实（历史读数）

```
$ git add docs/superpowers/specs/b417-breakdown.md \
          docs/superpowers/ledgers/2026-09-28-b417-breakdown-ledger.md
$ git commit -m "docs(B417): breakdown 拆 Android/iOS 壳子卡与集成 DAG（执行机回落提案，待拍板）"
<commit hash 见收尾行>
```

（本段落随同一提交 amend 收口；amend 会换 hash——git 的事实，不 chasing。收口判据是工作树干净。）
