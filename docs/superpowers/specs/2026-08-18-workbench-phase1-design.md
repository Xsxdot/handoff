# 工作台一期：任务卡账本 + 看板 + 主会话驱动（spec v4）

> 状态：待敲定项已全部敲实（选型 PG、DDL、镜像 lease、CLI 命名、裁决 schema、
> 页面关系、逐条判法），原型形态经两轮走查确认（基准落 base/README.md，
> 确认中）。待用户终审后交 writing-plans。
> 上游蓝图：[2026-08-18-workbench-blueprint-design.md](2026-08-18-workbench-blueprint-design.md)
> （账本宿主、双聚合、正交标记、对账规则等设计裁决见蓝图 §3，本文不重复论证）。

## 1. 目标与非目标

**目标**：把 backlog 从 markdown 总账搬进中心账本库，成为带合并/拆分、
阻塞边、自定义状态、工作流/派发模板双聚合的一等实体；事件镜像把跨机 task 事件
汇成账本单流；看板作为不说谎的注意力平面；CLI + 节点执行器支撑主会话按工作流
驱动推进；账本单流多路 wait 支撑「全程一个 wait」。

**非目标**（蓝图二期以后）：事件自动触发、群聊、富评论、上下文文档实体化、
蓝图 goal、自动合 main、executor 写账、agentd 自主唤醒协调者。

**模块边界约束（蓝图 §3.8，一期就位）**：账本域落为独立 Go 模块（api/Facade
收口，执行域代码不 import 其 internal，反之亦然）；镜像以 agentd 子系统注册
接入；web 两页经 dock/路由注册点挂载；账本持久化收在 store 接口后。这四个
扩展点是二三四期的生长面——一期实现不得绕过它们走捷径。

## 2. 数据模型（中心账本库 PostgreSQL，单机回退 SQLite）

> **选型定案：PostgreSQL**。理由：LISTEN/NOTIFY 天然支撑账本单流的事件推送
> （多路 wait 免轮询）、partial unique index 直接表达「一卡至多一条
> merged_into」与镜像幂等键。store 接口层保留方言吸收位，MySQL 适配留到真有
> 需求再做（不预先实现）。单机回退 SQLite 时事件推送退化为进程内广播 +
> 兜底轮询（回退模式下账本与消费者同进程，无跨进程推送需求）。
>
> DDL 见 §2.1；以下为定案的结构性决策：

- **账本表只存在于中心账本库**（或单机回退模式的本机 SQLite）；执行机 agentd
  的 store 永不建这些表，执行机不持有账本库凭据。账本持久化收在 store 接口
  后面，SQL 方言差异（占位符、upsert、通知机制）在该层吸收。
- `cards`（任务卡，蓝图 §3.1——**无类型身份，只有阶段与附件**）：id（**沿用
  B 号连续编**，全部记忆/skill/commit 都用 B 号指代，换前缀会断上下文）、
  title、status、priority（仅展示/排序）、project、parent_id、workflow_id +
  版本、`attachments`（附件引用集：{kind: spec/plan/doc, path} 列表，路径归一
  为相对 docs/superpowers/ 的规范形）、`acceptance_criteria`（判据文本，每卡
  独立）、`base_branch`（**基线分支**，可空：空 = 继承最近显式设置的祖先卡，
  顶层也空 = 项目默认主线。派发的工作分支从基线拉出，合并节点的自动合并目标 =
  基线；蓝图 §3.1）、driver lease（会话标识 + 心跳时间）、时间戳。**不强制出
  spec**——spec 是否必须由工作流 gate 决定（见 workflows）。
- `card_relations`：`from → to` + `type` 枚举 {blocks / merged_into（并入）/
  discovered_from（发现自）/ split_from（拆分自）/ relates（关联）}。
  环检测与 blocked 衍生态**只对 blocks 生效**；`merged_into` 派生**跟随**态
  （被并卡显示状态 = 承载卡状态；一卡至多一条 merged_into，可解除=拆回）。
  写入时单事务读全图判定（含 parent 树与 blocks 边混合成环）。全部关系类型
  双向可查——「由此发现→B142」这类考古链是真实总账的高频写法。
- **合并/拆分是账本域一等操作**：合并 = 建 merged_into + 承载卡收编显示
  （「B4–B9 六条一份 spec」= 六卡并入承载卡，各自验收判据与已验记录保留）；
  拆分 = 建子卡 + split_from。批内单条真机验不过 → 拆回恢复自主流转。
  原「按 spec_ref 聚合的批次视图」设计**退役**——合并关系是更诚实的表达。
  **合并校验**：成员有效基线分支必须一致（跨基线拒绝）；不允许链式合并
  （承载卡自身不得是被并卡、被并卡不得再承载别人）——跟随派生保持单跳。
- **验收结构化**：验收结果落事件类型 `acceptance_recorded`（含
  `verified_on_real_machine` bool + 证据文本）。「已完成(已验)」与「已完成
  (待真机验)」是真实存在的正交维度，必须可表达、可过滤，不压进状态列。
- **终止态带 reason 枚举**：取消 / 废弃 / 搁置（可复活）。真实总账有 4 条
  🗄️ shelved 与 1 条 🧊，迁移需要此语义。
- `card_tasks`：`(card_id, target, task_id, 用途)` 弱引用表——账本侧指向 task
  的唯一通道；task 表只加 opaque `card_id` 标签列（不设 FK）。**plan 附件挂在
  派发事件上**（一 spec 多 plan = 多次派发各带各的 plan）。
- `card_events`：append-only 单流，独立 seq（沿用 events 表模式）。事件类型：
  状态转移（带 actor + CAS 前值）、派发（含模板版本 + 纪律块 hash 快照）、审阅
  裁决、合并记录、`acceptance_recorded`、**comment**（原 note 升级：body +
  引用卡 id 列表——写入时自动落 relations 关联边；`kind` 子类 {普通/更正}
  承接「变更痕迹」文化；附件字段预留空数组，二期填）、**镜像 task 事件**
  （保留来源 target 与原 seq）。
- `workflows`：状态机形状，**不可变版本化**（edit 产生新版本，卡钉版本，
  旧卡显式迁移）。转移可配 **gate 条件**（进「已出 spec」需 spec 附件非空、
  进「待合并」需验收判据非空——政策而非本体，bug 流可以无门）。**默认
  feature 工作流出厂自带「已出 spec」插入状态**——对齐用户真实生命周期
  💡→📋→🔨→✅ 的 📋 关口（人审 spec 是三个人工位之一），顺便示范插入机制。
- `dispatch_templates`：带版本；executor 类型、纪律块（引用 + hash）、prompt
  模板、目标机、分支策略、**per-target 模型覆盖**。
- `decisions`（裁决项，蓝图 §3.6）：body、options（可选 json）、card_id
  （可空——项目级请示如「推不推汇流线」）、status {open/answered}、created_by
  （会话标识）、answer + answered_by + 时间。开闭均落 card_events（挂项时）。
  open 裁决是看板「等人」面的一等数据源；答复消费 = 会话唤醒后查账，自动唤醒
  留三期。
- 正交标记不落列：`blocked`（全部 blocker 达「已完成」才解除；blocker 终止 →
  下游打等人）与 `等人`（带 reason 枚举）均从边表 + 事件流推导，查询时计算。

### 2.1 DDL（PG 规范形；SQLite 回退映射见文末注）

```sql
CREATE TABLE cards (
  id            TEXT PRIMARY KEY,          -- B 号（子卡点号形：B156.1）
  title         TEXT NOT NULL,
  status        TEXT NOT NULL,             -- 状态名中文原文，与 workflow 定义一致
  terminate_reason TEXT,                   -- 终止态才填：取消/废弃/搁置
  priority      TEXT NOT NULL DEFAULT '中',
  project       TEXT NOT NULL,
  parent_id     TEXT REFERENCES cards(id),
  workflow_name TEXT NOT NULL,
  workflow_version INT NOT NULL,           -- 卡钉工作流版本
  attachments   JSONB NOT NULL DEFAULT '[]',  -- [{kind: spec|plan|doc, path}]
  acceptance_criteria TEXT,
  base_branch   TEXT,                      -- 基线分支；NULL = 继承祖先/项目主线

  driver_session TEXT,                     -- 驱动会话 lease
  driver_heartbeat_at TIMESTAMPTZ,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_cards_board  ON cards(project, status);
CREATE INDEX idx_cards_parent ON cards(parent_id);

CREATE TABLE card_relations (
  from_id TEXT NOT NULL REFERENCES cards(id),
  to_id   TEXT NOT NULL REFERENCES cards(id),
  type    TEXT NOT NULL,  -- blocks|merged_into|discovered_from|split_from|relates
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (from_id, to_id, type)
);
-- 一卡至多一条并入边（拆回 = 删这条边）
CREATE UNIQUE INDEX uq_rel_merged_into ON card_relations(from_id)
  WHERE type = 'merged_into';
CREATE INDEX idx_rel_to ON card_relations(to_id, type);

CREATE TABLE card_tasks (
  card_id  TEXT NOT NULL REFERENCES cards(id),
  target   TEXT NOT NULL,
  task_id  TEXT NOT NULL,
  purpose  TEXT NOT NULL,   -- implement|review|merge
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (target, task_id)   -- 一个 task 至多挂一张卡
);
CREATE INDEX idx_card_tasks_card ON card_tasks(card_id);

CREATE TABLE card_events (
  seq       BIGSERIAL PRIMARY KEY,   -- 账本单流全局序
  card_id   TEXT REFERENCES cards(id),  -- 可空：项目级事件（如项目级裁决开闭）
  type      TEXT NOT NULL,  -- status_moved|dispatched|review_verdict|merged|
                            -- unmerged|split|acceptance_recorded|comment|
                            -- decision_opened|decision_answered|task_mirrored
  actor     TEXT NOT NULL,  -- 会话标识 / user / mirror
  payload   JSONB NOT NULL,
  source_target TEXT, source_task TEXT, source_seq BIGINT,  -- 仅镜像事件
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- 镜像幂等键：（来源 target, task, 原 seq）唯一
CREATE UNIQUE INDEX uq_events_mirror
  ON card_events(source_target, source_task, source_seq)
  WHERE source_target IS NOT NULL;
CREATE INDEX idx_events_card ON card_events(card_id, seq);

CREATE TABLE workflows (
  name    TEXT NOT NULL,
  version INT  NOT NULL,
  definition JSONB NOT NULL,  -- 状态序列（骨架锚点+插入位）、gate 条件、终态集
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (name, version)   -- 不可变版本化：只插新行，不更新旧行
);

CREATE TABLE dispatch_templates (
  name    TEXT NOT NULL,
  version INT  NOT NULL,
  definition JSONB NOT NULL,  -- executor、target、分支策略、prompt 模板、
                              -- 纪律块（路径引用+hash）、per-target 模型覆盖
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (name, version)
);

CREATE TABLE decisions (
  id      BIGSERIAL PRIMARY KEY,   -- 展示为 D-<id>
  card_id TEXT REFERENCES cards(id),  -- 可空 = 项目级请示
  body    TEXT NOT NULL,
  options JSONB,                   -- 可选项列表，可空 = 开放问答
  status  TEXT NOT NULL DEFAULT 'open',  -- open|answered
  created_by  TEXT NOT NULL,
  answer      TEXT,
  answered_by TEXT,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  answered_at TIMESTAMPTZ
);
CREATE INDEX idx_decisions_open ON decisions(status) WHERE status = 'open';

-- 镜像者仲裁与游标（§3）
CREATE TABLE mirror_lease (
  id INT PRIMARY KEY CHECK (id = 1),   -- 单行表
  holder      TEXT NOT NULL,           -- 协调机标识
  lease_until TIMESTAMPTZ NOT NULL
);
CREATE TABLE mirror_cursors (
  target   TEXT PRIMARY KEY,
  last_seq BIGINT NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()  -- 兼作镜像心跳
);
```

- **事件推送**：`card_events` 插入后 `NOTIFY card_events, '<seq>'`（触发器或
  应用层发均可，落实现时定）；消费者 LISTEN + 按 seq 补查，断连后从上次 seq
  catch-up——推送只是叫醒，**真相永远是按 seq 查表**。
- **SQLite 回退映射**：BIGSERIAL→INTEGER PRIMARY KEY AUTOINCREMENT、
  JSONB→TEXT(json)、TIMESTAMPTZ→TEXT(RFC3339)、partial index→应用层校验；
  NOTIFY→进程内广播 + 兜底轮询。回退模式建表由同一 store 层按方言生成。
- **B 号分配**：`card add` 在单事务里取当前 max B 号 +1（含历史归档与映射表，
  号永不复用）；子卡取父卡下一个点号位。

## 3. 事件镜像（协调机 agentd 新子系统）

- 镜像者（任一协调机 agentd，由**账本库 lease 仲裁单实例**）订阅各 target 的
  `/ws/events`，把挂账 task 的事件写入 `card_events`；断线用既有 cursor
  续拉语义补齐；写入按（来源 target, task, 原 seq）幂等，**镜像 cursor 落
  账本库**——任意协调机接任镜像者后接续，不丢不重。
- 镜像滞后/断链落显式状态，看板卡片标「事件流滞后」。
- 工单（permission_request/question）随镜像入流，是「等人」显性化的数据源。

定案的生命周期与 lease 语义：

- **订阅模型：挂账即订阅（per-task）**。~~常订全量过滤~~ 该早稿假设存在
  target 级全量事件流——真机核对后不成立：`/ws/events` 是按 task 的端点
  （`?task=<id>&from_seq=`），且 web-console 既有 `internal/agentd/mirror.go`
  正是 per-task 订阅形态。定案：镜像者按 tick（30s）从账本库读 `card_tasks`
  得订阅集，对每个挂账 task 维持一条订阅（`client.StreamEventsOnce` 语义，
  断线退避重连）；task 首次订阅从 watermark=0 起，历史事件自然全量补齐——
  「挂账前的事件」不需要单独补拉机制。终态 task 的订阅随之退订。
- **watermark 不设独立游标**：per (target, task) 的续拉起点 = 账本内
  `MAX(card_events.source_seq)`（幂等键同源，游标与数据不可能漂移——
  web-console Mirror 的 MirrorWatermark 同一手法）。`mirror_cursors` 表退化为
  **per-target 健康表**（updated_at 心跳 + 最近见到的 seq，仅供滞后判定与
  取证，不参与续拉）。
- **lease 语义**：单行 `mirror_lease` 表，时长 30s，持有者每 10s 续约
  （`UPDATE ... SET lease_until = now()+30s WHERE holder = self`）。抢占 =
  任意协调机发现 `lease_until < now()` 后 CAS 改写 holder；两机并发抢恰一个
  成功（行锁保证）。持有者续约失败（发现 holder 已非自己）立即停写。
- **切换/重启无补拉窗口问题**：cursor 按 target 落 `mirror_cursors`，每次批量
  写事件与推进 cursor 同事务；接任者从 cursor 续拉，幂等键兜住重复区间——
  不丢不重不需要额外窗口参数。
- **滞后判定**：`mirror_cursors.updated_at` 兼作心跳——持有者续约时对每条
  **健康**连接的 cursor 做空 touch（只更时间不动 seq），静默期不误报；连接
  断开的 target 不 touch。看板对 target 判 `now() - updated_at > 60s` →
  亮「事件流滞后」。无有效 lease 持有者超过 60s（全部协调机 agentd 都不在线）
  则全部 target 一起亮。

## 4. CLI

命令面定案（命名即下表所列，不再引入别名）：

- **状态名用中文原文**做 CLI 参数值（与 workflow 定义一致，如
  `card move B157 已出spec`），不造英文别名——两套词表必然漂移。
- **flag 约定**：`card add --project --priority --parent --workflow
  --base-branch`（workflow 缺省取项目默认流；base-branch 缺省空 = 继承/主线）；
  `card list --status --project --blocked --needs --base-branch --json`；
  `card update --title --priority --attach kind:path --detach path --accept
  <判据文本>`；`card move <id> <状态> [--expect <前值>]`（CAS 前值缺省取读到的
  当前值，`--expect` 显式钉住用于脚本场景）。
- **二次确认（交互确认，`-y` 跳过）只设三处**：`card close --reason
  取消|废弃`（终止不可逆语义重）、`card merge`（改变多卡的看板呈现）、
  `workflow migrate`（批量迁卡）。`unmerge`/`split`/`note`/`move` 是恢复性或
  高频操作，不设门。

- `handoff card add/list/show/update/close`：账本 CRUD；`list` 支持按状态/项目/
  blocked/等人 过滤（新会话领活前查账的入口）。骨架终态叫「已完成」，CLI 动作
  用 `close`，避免与 `handoff done` 撞名。
- `handoff card link/unlink`：阻塞边（写入即环检测）。
- `handoff card merge <ids...> --into <承载卡>` / `unmerge <id>`：合并与拆回；
  `split <id> <标题>`：拆子卡（自动挂 split_from）。
- `handoff card note <id> <text>`：记一笔。
- `handoff card move <id> <status>`：CAS 状态转移（带前值校验，冲突干净失败）。
- `handoff card dispatch <id> [--node <节点>]`：按模板拼装 prompt + 纪律块，走
  现有 dispatch 通道；**派发即认领**（待办→进行中 的 CAS 就是 claim，第二个
  会话干净失败并提示「已被 X 认领」）；task 回链 + 模板版本快照落事件。
- `handoff workflow ...` / `handoff template ...`：双聚合分开管理。
- `handoff wait --card <id> [--subtree]`：**账本单流多路 wait**。订阅卡子树
  事件流（含镜像 task 事件），wait 挂起期间新派发的 task 天然进流；退出条件 =
  子树全部卡达骨架终态；progress 类事件不唤醒（沿用现有过滤语义）。
- `handoff decision open/list/answer`：裁决项——主会话回合末落请示、用户答复、
  会话唤醒后读答复（`list --open` 是全局裁决收件箱）。
- `handoff card export`：最薄 markdown 只读快照导出（逃生门）。
- **executor 白名单不扩**：新增 card/workflow/template/decision 命令均不进 B115 自指令
  白名单，executor 永不写账。

## 5. 节点执行器（落码，不留 prose）

一期新增的唯一「编排」构件，主会话/看板按钮共用（三期规则引擎复用）：

- 输入：卡 + 节点定义（模板引用）；动作：派发审阅/合并 task、解析结构化裁决、
  落账、决定下一步。
- **裁决 schema 与通道（定案）**：executor 不写账（白名单不扩），裁决通道 =
  审阅 task 最终报文的文本契约。约定：报文末尾一个 fenced block，语言标记
  `handoff-verdict`，内容为 JSON：
  `{"verdict":"pass"|"fail","findings":[{"severity":"major"|"minor",
  "summary":"...","file":"可选"}],"notes":"可选"}`。解析器取报文中**最后一个**
  该标记的 block（防 executor 中途引用示例）；缺失或解析失败不猜，打「等人」
  （reason=裁决解析失败），原文全文落 timeline 供人裁。落账事件
  `review_verdict`，payload = 解析结果 + 原文引用。审阅类 dispatch_template
  的 prompt 模板**必须包含这份输出契约原文**——契约随模板版本化，改契约 =
  出新模板版本。
- **回合计数**：按 卡 × 节点粒度，从 card_events 推导（不存内存）；
  默认封顶 3 轮，超限打「等人」；人工插手（用户手动 continue/改裁决）是否重置
  计数：**重置**（人工介入视为新基线），落事件注明。
- 合并节点：客观判据先行（测试、gofmt），LLM 裁决 pass 仅为必要条件；**自动
  合并目标 = 卡的有效基线分支**——基线是集成分支时自动合回，基线就是 main 时
  该节点不自动合、直接打「待合并」等人（两级合并策略的退化形）；冲突打
  「等人」，冲突文件清单 + 双方 commit 范围落 timeline；合并顺序按 done 时序。
- 审阅 task 的生命周期由执行器收口（裁决落账后自动 `done` 归档），不留孤儿。

## 6. Web 看板（任一协调机 agentd 托管，读同一账本库）

**与 web-console 现有页面的关系（定案）**：**新增两页，不重构现有弹层**——
工作项账本页（路由 `/cards`）与流程管理页（路由 `/flows`），入口挂底部 dock
新图标 ▤（带「需要你」计数徽标，原型已示范）。现有看板 `<dialog>` 弹层保留
不动：它是**执行域**视图（task 生命周期，四列），账本页是**账本域**视图
（卡生命周期，工作流状态列），两者经卡片抽屉的「关联执行」区互跳。
**一键动作确认交互**：状态转移/派发采用按钮内联二次确认（点一下变确认态、
再点执行），不弹 modal——与「就地变化」原则一致。

**信息优先级原则（设计约束，用户反馈的直接教训）**：界面主角是知识流
（spec 批次、验收状态、引用关系、评论），**lease/镜像/CAS 类保真信号默认沉默、
异常才显形**（驱动正常不显示，只亮「无驱动会话」；镜像收敛为健康小点，断链才
展开告警）。**同源约束：卡的一切信息只在详情抽屉一处看，不另开面板/弹层**
（裁决就地答复、合并成员在抽屉并入区——两次形态走查确认的同一条原则）。

- 看板/**列表双视图**：看板列 = 工作流状态（骨架 + 插入）；列表复刻 markdown
  总账列（ID/标题/状态/验收/优先级/附件/备注）+「含归档」过滤——领活与考古
  入口，被并卡在列表显示「跟随 <承载卡>」。**「需要你」合一筛选**（蓝图
  §3.6）：点击就地过滤出等人/裁决/冲突卡，项目级裁决在筛选态以细条出现在列区
  上方；不设常驻横条，不弹层。blocked 徽标；多项目过滤；「未挂账」收为一行
  摘要点开展开（异常态不常驻占位）。
- 卡片主信息：优先级、附件徽标（▤ spec 等，指向 git 文件）、**承载卡「⊕ 并入
  N」徽标（点开详情抽屉并定位到并入区：各被并卡 + 各自验收状态 + 拆回
  动作）**、已完成态的
  「已验/待真机验」徽标；异常徽标（⚖ 裁决/⛔ 等人/状态冲突/blocked/工单）。
  基线非主线的卡显示分支 chip（如 `⎇ desktop-shell`，长期功能线一眼可辨，
  可按基线过滤）。被并卡不在看板单独成卡（列表与承载卡抽屉并入区可见）。
  **实时 join 关联 task 状态**，
  账面与实况矛盾亮「状态冲突」。
- 详情抽屉：状态流水线、**验收区（判据 + 已验开关 + 证据摘要）**、**并入区
  （承载卡专属：被并卡清单 + 各自验收 + 拆回，替代独立合并面板）**、**关系区
  （阻塞/发现自/拆分自/关联，双向；「承载着」不进关系区以免重复）**、
  子任务树 rollup（父状态独立驱动）、
  关联 task 跳转、**分层 timeline**（评论=气泡主视觉，系统事件=浅色 meta 行，
  镜像 task 事件折叠成组，全部/评论/裁决/系统过滤）、评论框（`#B142` 引用自动
  成关系边，双向可见）。
- 一键动作（人工插手通道）：转移状态、按节点派发——调用与主会话同一节点执行器。
- **流程管理页**（独立页，不塞 settings）：工作流 / 派发模板两个 tab，各自
  版本列表 +「N 张卡钉在 vX」+ 显式迁移动作；模板详情含 per-target 模型
  覆盖、纪律块正文与 hash、版本取证（哪次派发用了哪版）。

## 7. 主会话驱动（行为规约，落到 handoff skill 改写）

- 唤醒后先 `card show` + `decision list` 从账本重建现场（含未读答复），不信
  会话记忆。
- **回合末结构化落账**（蓝图 §3.6 四分法）：完成项→状态/验收事件；更正→
  comment(kind=更正)；请示裁决→`decision open`；阻断需人工→等人标记。聊天
  prose 照旧，账本是结构化副本。
- 派发前查账防重复开工；派发即认领；挂账本单流多路 wait。
- 子任务完成 → 推「待审阅」→ 调节点执行器（审阅→裁决→continue 或合并→已完成）
  → 查阻塞图派下一个。
- 全部完成 → 整功能验收（主会话亲自）→ 父卡进「待合并」等用户合 main。
- 验收后发现 bug：开新卡挂关联，不 reopen。
- 出问题（等人标记、状态冲突）：协调、裁决、或转人工，全部动作落事件。

## 8. 存量迁移（按序执行）

1. 迁移前对齐汇流点分支（web-console）实测 merge-base，确认无分叉遗漏。
2. backlog.md 未完成条目入库为 open 卡，历史 done 条目归档入库（只读，历史
   共享 spec 的批次不重建合并关系，只保留 spec 附件引用——考古够用，别造史）；
   **B 号→卡 id 映射表落库**，历史考古接得上。状态映射表：💡→待办、
   📋→已出 spec、🔨→进行中、✅done(已验)→已完成+已验、done(待真机验)→
   已完成+待验、🗄️/🧊→终止(搁置·可复活)；「验收」列→acceptance_criteria+
   验收事件；「变更痕迹/备注」→首条 comment；「见 B17/由 B115 发现」类引用
   尽力解析为 relations（解析不了的保留在 comment 原文里）。
3. backlog.md 顶部加冻结注记；**全局 skill（~/.claude 下 product-backlog）与
   CLAUDE.md §4 同一批次切换为指针**——先切 skill 再冻结文件，避免其他在途
   worktree 的旧 skill 副本继续追加。
4. 抽查 N 条历史条目字段无损 + 映射表可反查。

## 9. 验收判据（逐条真机判法）

> **执行归属**：本节判据全部需要驱动 handoff 自身（agentd/dispatch/wait），
> 按派发纪律**一律由审核者本地执行，不写进派发 plan**（B105 教训）；派发 plan
> 里另设不依赖真机的单测/集成测试判据。**开工前判据先在基线上重跑一遍**
> （判据会过期，写下时对不等于开工时对）。判定尽量用正向断言 +
> 事件流计数对照，不用「不存在 X」类反面断言。

① **标准例全程一个 wait**：测试项目建 5 卡依赖图（1→2→(3∥4)→5），主会话
  单条 `wait --card <父> --subtree` 推完全程。判法：card_events 审计——
  dispatch 事件 ≥5 条且 actor 均为主会话；人工 actor 的事件只出现在
  审 spec / 整功能验收 / 合 main 三个位置。
② **审阅 3 轮封顶**：用判据故意造 fail 的审阅模板连跑；第 3 次 `review_verdict
  (fail)` 后卡带等人(reason=审阅超轮)。判法：`card show` 显示等人；事件流中该
  卡×审阅节点的 review_verdict 恰 3 条。
③ **blocker 终止不解锁**：`card close --reason 取消` 一个 blocker。判法：下游
  `card show` 仍 blocked 且新增等人(reason=前置终止)；`card list --needs`
  能过滤出它。
④ **看板不说谎**：kill 主会话进程，等 task 判 failed。判法：看板该卡亮
  「状态冲突」（账面进行中 × task failed 的 join 结果），而非仍显示正常推进。
⑤ **多路 wait 不漏事件**：wait 挂起期间另一会话向子树 dispatch 新 task。
  判法：wait 输出含该 task 的镜像事件；结束后按 seq 对照账本单流，子树事件
  无缺号。
⑥ **并发认领恰一成功**：两会话并发 `card dispatch` 同一张卡（脚本同时发）。
  判法：恰一个 exit 0；另一个非零退出且报「已被 <会话> 认领」；事件流只有
  一条 dispatched。
⑦ **镜像断链恢复**：停掉 target 的 agentd ≥60s 再拉起。判法：断链期看板该
  target 亮「事件流滞后」；恢复后 `card_events` 按（target,task,seq）无重复
  （幂等键约束在，count 与来源事件数一致），滞后标记消失。
⑧ **迁移无损**：迁移脚本跑完后随机抽 10 条历史条目对照 backlog.md 原文行，
  字段逐列一致；用映射表把任一卡 id 反查回 B 号并在原文中 grep 命中。
⑨ **双端一致**：CLI `card move` 后看板 3s 内呈现新列；看板一键转移后
  `card show` 状态一致（同一账本库，无中间缓存）。
⑩ **双协调机对等**：本机与 mac-02 指向同一账本库；A 机认领派发，B 机看板
  实时可见。停 A 机 agentd。判法：≤60s 内 `mirror_lease.holder` 变为 B；
  切换前后子树事件按幂等键去重比对，不丢不重；B 机可接续驱动（continue 成功）。
⑪ **裁决闭环**：主会话 `decision open` 一条请示。判法：看板「需要你」筛选态
  可见并可答复；答复后事件流有 decision_answered、卡 timeline 可见；另一会话
  `decision list` 读到答案且 `--open` 不再列出它。
⑫ **合并/拆回无损**：三卡 merge 入承载卡。判法：看板列内被并卡消失、仅承载卡
  流动（列 count 少 3）；列表视图三卡显示「跟随 <承载卡>」；unmerge 一张后其
  恢复自主状态流转，且其 acceptance_recorded 事件数与合并前一致。
⑬ **workflow gate**：feature 流卡无 spec 附件时 `card move <id> 已出spec`
  被拒且报错文案指明缺附件；`card update --attach spec:<path>` 后同命令成功。
⑭ **基线分支并行**：epic 父卡设 base_branch=集成分支，其子卡与一张顶层
  main 热修卡并行派发。判法：两个 task 的工作分支 `git merge-base` 实测分别
  落在各自基线上；合并节点把子卡合回集成分支（自动），热修卡不自动合、进
  「待合并」；跨基线 `card merge` 被拒且报错指明两侧基线。
