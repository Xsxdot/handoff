# Handoff 二期设计：分级审批链、executor 选择、dispatch 参数扩展、可观测性

日期：2026-08-08
状态：已与用户确认
前置：一期设计见 `2026-08-07-handoff-design.md`（MVP 已实现并经真实 e2e 验证）

## 1. 问题与目标

MVP 跑通后的四个优化方向（均已与用户逐条确认）：

1. **分级审批链**：目前所有权限请求都唤醒审核者（顶级模型，成本高）。引入廉价模型「审批者」
   前置过滤初级权限，审批者拿不准才升级审核者，审核者拿不准才升级用户本人。
2. **executor 选择**：派发时指定执行者与模型——复杂任务给 Claude Code，简单任务给
   opencode + 便宜模型（本期只做机制，新 adapter 单独立项）。
3. **dispatch 参数扩展**：除 plan.md 外支持附加提示词与工作区参数（指定分支/新建分支/
   指定 worktree/新建 worktree），由 agentd 准备好工作区后交执行者。
4. **可观测性**：派发后默认弹终端窗口看实况；`handoff attach` 列表选择进入任意任务的
   tmux 现场。

## 2. 核心决策（已确认）

| 决策点 | 结论 |
|--------|------|
| 审批者位置 | agentd 侧（executor 所在机）：审核者离线时初级权限也能秒批，executor 不阻塞 |
| 审批者调用形态 | 一次性 CLI 调用（复用 executor 的 one-shot 调用方式映射），不常驻 |
| 审批者出口 | 仅 approve / escalate 两个，**无 deny 权**；误批方向由硬规则黑名单前置兜底，误拒不如升级 |
| 黑名单 | 命中即无条件升级审核者，审批者根本见不到该请求 |
| fail-closed | 审批者超时/解析失败/非零退出，一律按 escalate 处理 |
| executor+model 二元组 | 派发与审批者配置统一为「执行者 + model」两参数；model 可空 = 用该执行者自己的默认模型，handoff 不管 |
| 缺省 executor | 可配置（config 中 executor.default / executor.model），dispatch flag 覆盖 |
| 新 adapter 范围 | 本期只做机制（注册表 + 路由 + 参数透传 + 校验）；Claude Code、grok adapter 之后各自单独立项 |
| prompt 与 plan | 至少给一个；同给时 prompt 拼在 plan 之后作附加指令；可只给 prompt 派发简单任务 |
| 分支参数 | `--branch`（已存在，不存在拒发）/ `--new-branch [--base]`；缺省自动建 `handoff/<id8>`（现行为） |
| worktree 参数 | `--worktree <已存在路径>` / `--new-worktree`（agentd 建在 `~/.handoff/worktrees/<id8>`）；缺省原地（现行为，本期不翻转缺省） |
| worktree 清理 | `handoff done` 归档时自动 `git worktree remove`（分支保留） |
| 任务命名 | `--name` 可选；缺省从 plan 文件名（去日期/扩展名）或 prompt 前 20 字符派生 |
| attach 语义翻转 | `handoff attach` 改为进终端实况（人类直觉语义）；原快照命令改名 `handoff show`，会话恢复流程与文档同步更新 |
| 默认弹终端 | dispatch 成功后 osascript 弹本机终端自动 attach；`--no-terminal` flag 与配置项可关 |

## 3. 功能 1：分级审批链

> **第 0 层已提前落地（2026-08-08 dogfooding 修正）**：真实派发暴露「全 ask 配置导致
> 初级权限连环唤醒审核者」，已在 opencode adapter 的任务级配置中实现静态分级
> （edit 放行、bash 危险模式表 ask 其余 allow、webfetch/external_directory 仍 ask，
> 见 taskenv.go bashPermissionRules）。本节审批者是其上的第 1 层：只处理静态规则
> 放行之外、又不在黑名单内的中间地带。

### 3.1 流程

```
permission 事件（SSE → adapter → manager）
  → 硬规则黑名单命中？ ── 是 → 直接升级审核者（现行为，审批者不参与）
  → 否 → 审批者已配置？ ── 否 → 直接升级审核者（现行为）
  → 是 → 一次性调用审批者 CLI
        ├─ {"decision":"approve"}  → RespondPermission(once)，事件只入库不唤醒
        └─ escalate / 超时 / 解析失败 / 非零退出 → 升级审核者（fail-closed）
审核者拿不准 → 升级用户本人（现有机制，不变）
```

### 3.2 配置（agentd 侧 config.yaml）

```yaml
approver:
  executor: opencode        # 空 = 不启用审批链，全部升级审核者（现行为）
  model: some/cheap-model   # 可空 = 用该执行者自己的默认模型
  timeout: 60s              # 单次裁决超时，超时按 escalate
  blacklist:                # 追加规则（正则），与内置规则合并
    - "kubectl .*delete"
```

内置黑名单（不可关闭）：`rm -rf`、`git push --force`/`-f`、`sudo`、`git reset --hard`、
`DROP TABLE`/`DROP DATABASE`、生产环境关键词（prod/production）。匹配对象为权限请求
描述原文（bash 命令即命令原文）。

### 3.3 审批者调用契约

- **输入**（stdin 或 prompt 参数）：固定裁决提示词 + 权限请求原文 + 任务 plan/prompt 摘要；
- **输出**：单行 JSON `{"decision":"approve|escalate","reason":"..."}`（从输出中扫描最后一个
  合法 JSON 行，容忍模型多话）；
- **调用方式**：复用「executor one-shot 调用映射」（§4.3），如
  `opencode run -m <model>`、`claude -p --model <model>`；
- **审计**：每次裁决落 events 表（type=`approver_decision`，payload 含 decision/reason/耗时），
  只入库不唤醒；审核者与用户经 `handoff show` 可查全部裁决记录。

## 4. 功能 2：executor 选择机制

### 4.1 派发与路由

- `handoff dispatch --executor <name> --model <model>`：两者均可省略，缺省取 agentd 配置；
- agentd 维护 adapter 注册表（name → Adapter 工厂），未注册的名字拒发并报错列出可用项；
- tasks 表新增 `executor`、`model` 字段；`tasks` / `attach` 列表展示 executor。

### 4.2 配置

```yaml
executor:
  default: opencode   # 缺省执行者
  model: ""           # 可空 = 该执行者自己的默认模型
```

`HANDOFF_OPENCODE_MODEL` 环境变量保留兼容，优先级：dispatch flag > env > config > 执行者自身默认。

### 4.3 one-shot 调用映射（供审批链复用）

handoff 内部维护每种执行者的一次性调用方式（name + model → argv 模板），与 adapter 注册表
同源登记。本期实现 opencode 与 claude 两个映射（claude 的 one-shot `claude -p` 可用于审批者，
不依赖其任务级 adapter 实现）。

### 4.4 范围外

Claude Code adapter、grok adapter 的任务级实现（Start/Events/Send/RespondPermission/Stop
全链路）各自单独立项，本期仅保证机制就绪。

## 5. 功能 3：dispatch 参数扩展

### 5.1 参数总览

```
handoff dispatch [plan.md]
  --prompt <text>        附加提示词；与 plan 至少给一个，同给时拼在 plan 后
  --name <可读名>         缺省：plan 文件名去日期/扩展名，或 prompt 前 20 字符
  --executor <name>      见 §4
  --model <model>        见 §4
  --branch <name>        切到已存在分支（不存在拒发）
  --new-branch <name>    以指定名建新分支；--base <rev> 指定起点，缺省当前 HEAD
  --worktree <path>      在已存在的 worktree 中执行（须是该 repo 的 worktree，校验后拒发）
  --new-worktree         agentd 用 git worktree add 建到 ~/.handoff/worktrees/<id8>
  --no-terminal          派发成功后不弹终端（见 §6）
```

约束：`--branch` 与 `--new-branch` 互斥；`--worktree` 与 `--new-worktree` 互斥；
`--base` 仅与 `--new-branch` 连用。

### 5.2 语义

- 分支与工作树是正交维度，均缺省 = 现行为（原地自动建 `handoff/<id8>`）；
- 所有准备（建/切分支、建 worktree）由 agentd 在启动 executor **之前**完成，任一步失败
  即拒发并携带 git stderr 原文；
- `--new-worktree` 下脏主仓不阻塞派发（新工作树天然干净），且支持同 repo 并行派发——
  这是原地模式做不到的（第二个任务会切走第一个的分支）；
- `handoff done` 归档时：任务若持有 agentd 创建的 worktree，自动 `git worktree remove`
  （失败降级为警告不阻塞归档），分支保留；用户自带的 `--worktree` 路径不代删。

### 5.3 数据

tasks 表新增：`name`、`executor`、`model`、`worktree_path`、`worktree_managed`（是否
agentd 创建，决定 done 时是否代删）。

## 6. 功能 4：可观测性

- **`handoff attach [task]`**（语义翻转）：
  - 带 task：本机任务 `tmux attach -t <session>`；远程任务 `ssh -t <host> tmux attach -t <session>`；
  - 不带 task：列出任务（running 优先分组），每行 `序号  name  executor  状态  最近事件时间`，
    输入序号回车进入；非 TTY 环境降级为仅打印列表与建议命令；
- **`handoff show <task>`**：原 attach 快照语义原样迁移（plan 摘要、事件历史、未处理挂起项）。
  会话恢复验收标准改为 `handoff tasks` + `handoff show`，一期 spec 与 README 同步更新；
- **默认弹终端**：dispatch 成功后 osascript 起 Terminal/iTerm 窗口执行
  `handoff attach <task>`；`--no-terminal` flag 或配置 `terminal.auto: false` 关闭；
  osascript 失败降级为打印 attach 命令提示，不影响派发结果。

## 7. 错误处理

- 审批者 CLI 不存在/不可执行：首次调用失败即在日志与事件中记录，该次按 escalate；不重试
  风暴（同任务连续失败 3 次后本任务内直接走 escalate 并记 `approver_disabled` 事件）；
- `--branch` 不存在、`--worktree` 非法路径、`--base` 非法 rev：拒发，git stderr 原文回传；
- worktree remove 失败（脏工作树等）：警告事件 + 归档继续，提示用户手动清理；
- ssh attach 失败（远程不可达）：打印可重试的完整命令，退出码非零。

## 8. 测试策略

- 审批链：fake 审批者命令（脚本回固定 JSON / 超时 / 乱输出）覆盖 approve、escalate、
  fail-closed、黑名单前置、审计落库；
- 注册表路由：未注册名拒发、缺省配置回退链（flag > env > config > 自身默认）单测;
- 分支/worktree 准备：真实 git 仓库临时目录集成测试（互斥校验、并行派发、done 清理）；
- attach 列表：非 TTY 降级路径可测；tmux/ssh/osascript 实际弹窗为手动验证清单；
- 会话恢复回归：`show` 改名后跑一遍一期恢复流程验收。

## 9. 范围外（YAGNI）

- Claude Code / grok 任务级 adapter（单独立项）；
- 审批者多级链（多个廉价模型接力）、投票制审批；
- `--new-worktree` 变为缺省行为（等用顺后再议）；
- Linux/Windows 弹终端支持（osascript 仅 macOS，其他平台直接降级为打印命令）；
- 审批者对 question 事件的代答（仅管 permission）。
