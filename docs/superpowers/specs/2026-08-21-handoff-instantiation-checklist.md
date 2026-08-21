# handoff 项目实例化清单（分域开发协议 §6）

> 2026-08-21 立。协议正文见 `2026-08-21-domain-partitioned-dev-protocol-design.md`，
> 本档是 handoff 这个项目的差异化配置——协议正文与工作台模板通用，**项目差异全部
> 收敛在这里**。机械底稿见 `2026-08-21-handoff-domain-inventory-draft.md`（包规模、
> 依赖边、暴露面、红线命中；由 codex 扫描产出，数字已抽查）。
>
> 维护纪律：域边界变化、契约层新增声明点、缺陷族新增，都在本档更新；
> 域文档**懒补**——哪张卡第一次深入某个域，补该域的域文档是那张卡的产出。

---

## 1. 契约层声明

「契约层」= 拆解节点装配上下文时要读的东西，也是编译器/测试强制的落点。
handoff 不是 DDD `api/` 分层项目，它的契约层分三段：

| 段 | 路径 | 强制层级 | 状态 |
|---|---|---|---|
| **进程内 Go 契约** | `internal/proto`（89 导出符号、入度 14、零仓内依赖） | 编译器 | ✅ 健康 |
| **Go↔TS wire 契约** | `internal/proto` 的 fixture 生成器 + `web/src/api/testdata/*.json` + `web/src/api/contract.test.ts` | 测试（逐字节钉住 + 前端镜像断言） | ⚠️ **覆盖有缺口，见 §1.1** |
| **账本数据模型** | `internal/ledger/types.go`（Card / WorkflowDef / NodeDef / Gate） | 编译器（仅 Go 侧） | ⚠️ 未接入 wire 契约 |
| **executor 适配契约** | `internal/executor`（根包 434 行，各 provider 子包实现） | 编译器 | ✅ 健康 |

### 1.1 已知缺口：账本域不在 wire 契约夹具里（结构性，非偶然）

**事实**（2026-08-21 实测）：

- `internal/proto` 的 `TestContractFixtures` 为 35 个对外类型生成 fixture 并逐字节比对，
  `web/src/api/contract.test.ts` 用强类型变量承接同一批 JSON——两侧任一改动线格式即当场变红。
- `web/src/api/testdata/` 里**没有任何** Card / CardView / WorkflowDef / NodeDef / FlowDetail 夹具。
- `internal/agentd` 有 **15 处**手搭 `map[string]any` 回包（`ledgerapi.go` 占 13 处），
  TS 侧 `web/src/api/ledger.ts` 的 8 个接口全部手写，无生成器。

**后果**：账本域是全仓**唯一**没有 wire 契约强制的区域，而 2026-08-21 两次踩到的 wire 缺陷
**全部**落在这块：

1. `CardView.ChildrenTotal/ChildrenDone` 派生字段——Store 测试绿、前端组件测试绿、
   `handleCardsList` 手搭 map 漏掉两个键，端到端不通而全链路无一转红。
2. `contract` 附件 kind 被 `attachmentKinds` 白名单挡死——CLI 能挂、Web 400，
   两通道分裂，两侧测试各自绿。

**判断**：这不是「要发明一套机制」，是**已有机制没覆盖到这个域**。修法方向是把账本 wire
类型接进既有夹具机制，成本远低于新造。任何触及账本 wire 的卡都应把这件事纳入契约增量。
（尚未立卡，见 §6。）

---

## 2. 领域清单与域类型标注

域类型决定验收形态：**逻辑域**接缝对面是自有代码，mock 契约层接口可信、机内测试可闭环；
**边界域**接缝对面是外部现实，机内只验契约形状（优先录制回放），行为验收写成
「真机清单，归协调者执行」。

| 域 | 主要包 | 源码行数 | 类型 | 边界域的外部现实是什么 |
|---|---|---:|---|---|
| **账本域** | `internal/ledger`, `ledgerstep`, `ledgermirror`, `store` | 6,877 | 逻辑域 | —（SQLite/PG 经 store 抽象，测试用真实 SQLite） |
| **控制面 API 域** | `internal/agentd`（HTTP/WS 部分） | 19,870（整包） | 逻辑域 | —（但**对 TS 的 wire 是接缝**，见 §1.1） |
| **远端连接域** | `internal/client`, `targetclient`, `relay`, `proxycfg` | 4,108 | **边界域** | 网络、relay 服务、对端 agentd 版本差异 |
| **Executor 适配域** | `internal/executor` 及各 provider 子包 | 14,945 | **边界域** | 真实 executor 进程、其 CLI/协议、会话生命周期 |
| **宿主进程与 PTY 域** | `internal/prochost`, `ptyhost` 及子包 | 5,969 | **边界域** | OS 进程、信号、PTY、文件系统、平台差异 |
| **换版与发布域** | `internal/release`, `upgrade`, `selfupdate` | 1,638 | **边界域** | GitHub、DMG/安装包、公证、真实换版 |
| **本机集成域** | `internal/service`, `toolchain`, `pathenv`, `envfile`, `permgate`, `initflow`, `localsync`, `skill` | 4,318 | **边界域** | launchd/systemd、PATH、真实文件系统、权限 |
| **CLI 域** | `cmd` | 7,804 | 逻辑域 | —（其边界性来自它调用的边界域） |
| **Web 控制台域** | `web/src` | 20,483 | 逻辑域 | —（webview 差异属独立风险，见缺陷族） |
| **契约域** | `internal/proto`, `buildinfo`, `projectid`, `discipline` | 2,533 | 逻辑域 | — |

**与机械底稿的分组差异（拍板记录）**：底稿把 `agentd` 与 `client/relay/upgrade/release` 合成
一个「控制面」大领域。**不采纳**——它们的域类型不同（前者逻辑域、后者边界域），合在一起
会让边界域的真机纪律稀释掉逻辑域的机内闭环要求。底稿列出的七处「拿不准的边界」中，
`prochost` 归宿主进程域、`ptyhost` 归宿主进程域（agentd 只是消费者）、`ledgerstep` 归账本域
（它是账本的推进器不是 agentd 的调配层）、`config` 归本机集成域、`permgate` 归本机集成域。

---

## 3. 缺陷族清单

以协议附录 A 的通用五族为基准（源自 handoff backlog 182 条聚类）：
生命周期/状态机中断、静默失败/误导报错、跨平台假设、假红测试、门禁绕过。

本项目已实战补入的两条设问（已写进协议 §7.1，此处只记指针）：

- **序列化边界设问**——新增字段的每一处手写序列化/投影都要列进文件清单并加断言。
- **新增枚举值必问既有白名单**——新引入的枚举取值凡流经既有校验器/白名单/switch 的，
  每一处都要确认已登记。

**本项目特有的第六族候选：webview / 平台表现差异**。已有多次实证（WKWebView 扣 Strict cookie
而 Chromium 不扣；Wails 真实手势下剪贴板写入被拒而合成点击是假绿）。触及 Web 控制台域
且涉及浏览器 API 的卡，必须逐平台验，不能拿一个 webview 的绿推广到另一个。

---

## 4. 架构测试

**现状：无。** 全仓没有锁死域边界 import 规则的测试。

现在的边界靠 Go 的 `internal/` 可见性 + 人工 review，够用但不成网：`internal/agentd` 是
一个 61 文件的平铺包，包内没有任何编译器层面的子边界。

**待办**（不在本次试点范围，另立卡）：一条遍历 `go list` 依赖图、断言域间 import 只走
允许边的测试。优先级取决于是否真出现跨域乱引——目前没有实证，不预支。

---

## 5. 参数

| 参数 | 本项目取值 | 说明 |
|---|---|---|
| 域尺寸红线 | 20,000 行（源码，不含测试） | 协议默认 2~3 万，取下沿 |
| 升格判据 | 一个文件名前缀家族 ≥5 个源文件 | 协议默认 |
| 子工作流嵌套上限 | 3 | 代码内 `maxWorkflowNesting`，与协议默认一致 |

### 5.1 两条判据的已知盲区（2026-08-21 实测发现，值得回写协议）

`internal/agentd`：**19,870 行、61 个源文件、零子包**——

- 域尺寸红线：19,870 < 20,000，差 130 行不命中；
- 升格判据：61 个文件里最大的前缀家族只有 3 个（`workspace*`），不命中。

**两条判据同时够不着它，而它是全仓最该拆的包。** 根因是判据形状：升格判据抓的是
「包内长出了家族」，对**扁平命名的大包**失效（agentd 的文件多是 `auth.go` / `forward.go` /
`ledgerapi.go` 这种单文件单概念）；尺寸红线是个绝对阈值，差一点就漏。

**补一条判据（本项目先行，验证后再回写协议）**：
**单包源文件数 ≥ 40 且无子包**，即使前两条都不命中，也必须在拆解时显式回答
「这个域还能圈出有界文件集吗」。文件数是比行数更稳的「一个人还能不能拿住它」的代理。

命中现状：`internal/agentd`（61 文件）、`cmd`（46 文件，但它是 CLI 入口聚合，
每个文件对应一个子命令，属于**正当的扁平**——判据命中不等于必须拆，它只要求显式回答）。

---

## 6. 由本清单派生的待办（未立卡，等拍板）

1. **账本 wire 类型接入契约夹具机制**（§1.1）——修的是两次 wire 缺陷的共同根因。
2. **`internal/agentd` 竖切**（§5.1）——61 文件平铺包，两条判据都够不着。
3. **架构测试**（§4）——目前无实证需求，不预支。
