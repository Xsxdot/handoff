# README 双语化实现计划（B89）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 392 行的中文 `README.md` 拆成英文主版 + 中文副版，GitHub 默认展示英文，两份内容对等可切换。

**Architecture:** 先 `git mv README.md README.zh-CN.md` 保住中文原文的修改历史，再新建英文 `README.md`；英文版按章节分批翻译，每批完成即验、即提交。全部改动只涉及 3 个 Markdown 文件，**不含任何代码改动**。

**Tech Stack:** Markdown、git、grep/sed（验证用）。

**Spec:** [docs/superpowers/specs/2026-08-14-readme-bilingual-design.md](../specs/2026-08-14-readme-bilingual-design.md)

## Global Constraints

以下约束对**每一个** task 都生效。

### C1. 不含代码改动，因此不含日志与代码注释步骤

本计划只改 3 个 `.md` 文件，无函数、无错误分支、无外部调用。`instrumenting-code` 的适用范围（"有错误路径、状态变更或外部调用"）不覆盖纯文档改动，故各 task 不设"加日志"与"加代码注释"步骤。**若执行中发现需要改任何 `.go` 文件，说明理解偏了——停下上报，不要自行扩大范围。**

### C2. 不可变字面量（翻译时逐字照抄，一个字符都不改）

- 全部 shell / PowerShell / yaml 命令与参数：`handoff dispatch --target devbox --new-worktree plan.md`、`curl -fsSL https://handoff.gosuper.dev/install | bash`、`irm https://handoff.gosuper.dev/install.ps1 | iex`
- 配置键与路径：`~/.handoff/config.yaml`、`~/.handoff/env/dev.env`、`~/.handoff/agentd.log`、`repo_root`、`reserve_ratio`、`path_dirs`、`proc_fence`、`listen`、`token`、`sync.auto`、`%LOCALAPPDATA%\Programs\handoff\handoff.exe`、`~/.local/bin/handoff`
- 状态与事件名：`pending`、`running`、`waiting_answer`、`waiting_review`、`completed`、`failed`、`permission_request`、`question`、`archived`、`delivery_failed`、`stalled`、`progress`、`permission_reuse`、`pending_tickets`、`update.pull_state`
- 专有名词：`handoff`、`agentd`、`opencode`、`Claude Code`、`grok`、`codex`、`launchd`、`systemd`、`Tailscale`、`WireGuard`、`WSL2`、`SmartScreen`、`Developer ID`
- URL、版本号（`v0.1.0`、`Go 1.26+`）、backlog 编号（`backlog B37`）、退出码与错误码（`404`、`409`、`502`、`401`、`1008`、`400`）
- **邀请链接必须原样保留**：`https://opencode.ai/go?ref=3AMC8DKNGP`（含 `?ref=` 查询串，出现 2 次：`## 各 executor 须知` 与 `## 友情链接`）

### C3. 术语表（英文版全篇统一，不得同词异译）

| 中文 | 英文 | 说明 |
|------|------|------|
| 协调者 | coordinator | 人或 AI 会话这一角色 |
| 协调机 | coordinator machine | 跑协调者的那台机器 |
| 执行机 | executor machine | 跑 agentd + executor 的那台机器 |
| executor | executor | 不译，小写 |
| 派发 | dispatch | 动词名词同形 |
| 工单 | ticket | 对应 `--ticket` |
| 权限门 | permission gate | |
| 裁决 | rule on / adjudicate | `reply` 的动作 |
| 待审核 | pending review | 状态值本身仍写 `waiting_review` |
| 一轮 / 回合 | turn | |
| 续改 / 续接 | follow-up (`continue`) | |
| 归档 | archive | |
| 现场 | live state | "断网不丢现场" 一类 |
| 审批链 / 审批者 | approval chain / approver | 对应 `approver` 配置段 |
| 进程围栏 | process fence | 对应 `proc_fence` |
| 安全闸 / 闸门 | safety gate | |
| 巡检 | survey | `handoff upgrade` 无 `--now` 时的行为 |
| 自拉 / 自拉更新 | self-pull | 执行机自己下载升级包 |
| 就绪判据 | readiness check | |

### C4. 文风

意译，不直译。保留原文的直接与具体（"你批一条它才动一条" → "it moves one step per approval you give"，不是 "you approve one and it moves one"）。按英文技术文案习惯：主动语态、短句、第二人称 "you"。**禁止**逐字翻译造出的中式英语和无主语被动句。

### C5. 结构对等

英文版必须有且只有 16 个二级标题（`^## `），顺序与中文版完全一致：

1. Installation（安装）2. Quick Start（快速开始）3. Connecting a Remote Executor Machine（连接远程执行机）4. Remote Executor Machine（远程执行机）5. Command Reference（命令速查）6. Task States and Events（任务状态与事件）7. Configuration Reference（配置参考）8. Executor Notes（各 executor 须知）9. Upgrading（升级）10. Session Recovery（会话恢复）11. Troubleshooting 12. Uninstall（卸载）13. Coming Soon（即将推出）14. Documentation（文档）15. Links（友情链接）16. License

代码块数量也必须一致（中文版 18 个 ` ``` ` 围栏对，即 36 行围栏）。

---

## File Structure

| 文件 | 动作 | 责任 |
|------|------|------|
| `README.zh-CN.md` | 由 `README.md` 重命名而来，仅在顶部插入语言切换条 | 中文原文，面向中文读者 |
| `README.md` | 内容整体重写为英文 | 英文版，GitHub 默认展示 |
| `CONTRIBUTING.md` | 改 1 行链接 + 加 1 条同步约定 | 中文，面向贡献者 |

---

## Task 1: 迁移骨架与链接修正

先把文件位置、语言切换条、跨文档链接、同步约定这些**机械且可精确验证**的部分做完。此后 `README.md` 是一个只有骨架的英文空壳，Task 2–7 逐节填内容。

**Files:**
- Rename: `README.md` → `README.zh-CN.md`
- Create: `README.md`（英文骨架）
- Modify: `CONTRIBUTING.md:4`（README 链接）、`CONTRIBUTING.md` 末尾（同步约定）

**Interfaces:**
- Produces: `README.md` 的 16 个二级标题骨架（章节标题按 C5 的英文名），Task 2–7 只往对应标题下填正文，不再增删标题。

- [ ] **Step 1: 重命名，保住历史**

```bash
git mv README.md README.zh-CN.md
```

- [ ] **Step 2: 验证历史没断**

```bash
git log --follow --oneline README.zh-CN.md | wc -l
```

Expected: 输出 > 1（能看到原 README 的多条历史提交）。若输出为 0 或 1，说明 rename 没被 git 识别，**停下**，不要继续。

- [ ] **Step 3: 给中文版加语言切换条**

在 `README.zh-CN.md` 的 `# handoff` 与 License badge 之间插入。改动后开头必须是：

```markdown
# handoff

[English](README.md) | [简体中文](README.zh-CN.md)

[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
```

`README.zh-CN.md` 除这一处外**不做任何其他改动**。

- [ ] **Step 4: 建英文骨架 `README.md`**

写入以下内容，正文留空（Task 2–7 填）：

```markdown
# handoff

[English](README.md) | [简体中文](README.zh-CN.md)

[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

## Installation

## Quick Start

## Connecting a Remote Executor Machine

## Remote Executor Machine

## Command Reference

## Task States and Events

## Configuration Reference

## Executor Notes

## Upgrading

## Session Recovery

## Troubleshooting

## Uninstall

## Coming Soon

## Documentation

## Links

## License
```

- [ ] **Step 5: 验证标题数量与顺序对齐**

```bash
diff <(grep -c '^## ' README.md) <(grep -c '^## ' README.zh-CN.md) && echo "标题数一致"
```

Expected: 输出 `16` 前的 diff 无差异 + `标题数一致`。

- [ ] **Step 6: 改 CONTRIBUTING 的 README 链接**

`CONTRIBUTING.md:4` 当前是 `[README](README.md)。`，改为：

```markdown
[README](README.zh-CN.md)。
```

理由（不写进文件，仅供执行者理解）：CONTRIBUTING 是中文文档、读者是中文读者，链到英文版是倒退。

- [ ] **Step 7: 在 CONTRIBUTING 末尾加同步约定**

追加一节（中文，与该文档语言一致）：

```markdown
## 文档同步

README 有中英两份：`README.md`（英文，GitHub 默认展示）与 `README.zh-CN.md`（中文）。
**改动 README 的 PR 必须同时更新这两份**，先改哪一份不限。只改一份的 PR 会被要求补齐。
```

- [ ] **Step 8: 验证切换条互链可通**

```bash
grep -c '\[English\](README.md) | \[简体中文\](README.zh-CN.md)' README.md README.zh-CN.md
```

Expected: 两个文件各输出 `1`。

- [ ] **Step 9: Commit**

```bash
git add README.md README.zh-CN.md CONTRIBUTING.md
git commit -m "docs(readme): 中文原文迁到 README.zh-CN.md，建英文骨架与语言切换条"
```

---

## Task 2: 英文版——标题区、流程图、安装

**Files:**
- Modify: `README.md`（`## Installation` 及其之前的全部内容）
- Source: `README.zh-CN.md:5-62`（含切换条后的行号偏移，即原 `README.md:5-62`）

**Interfaces:**
- Consumes: Task 1 建立的骨架与切换条。
- Produces: 英文版的定位句与流程图，后续章节的术语以此为准（尤其 coordinator / executor machine / dispatch 的首次定义）。

- [ ] **Step 1: 翻译标题区（原文 L5–7、L19–26）**

覆盖：一句话定位（`**把实现计划派发给另一个 AI 执行，你只负责审。**`）、两角色说明、"为什么不直接开个终端让 AI 跑？" 的 4 条 bullet、以及结尾那句自举声明（"本项目自身除第一期外的全部功能……"）。

自举那句是这个项目最有说服力的一句话，英文版必须保留它的分量，**不要**弱化成 "this project uses itself"。

- [ ] **Step 2: 翻译 ASCII 流程图（原文 L9–17）**

中文原图：

```
写 plan → handoff dispatch → executor 独立执行
                ↑                    │
        reply 裁决/回答 ←── 权限门/提问 唤醒你
                │                    │
        handoff diff 审核 ←──── 一轮干完
                │
   不满意 continue 续改 / 满意 done 归档
```

英文文本长度与中文不同，**箭头与竖线的列位置必须按新文本重新对齐**。只换字不挪线会得到错位的图。完成后在终端里目视确认竖线上下对齐。

- [ ] **Step 3: 翻译 `## Installation`（原文 L28–62）**

包含：macOS/Linux 与 Windows 两条安装命令、Windows 只能当协调者的整段限制说明（含 WSL2 与端口转发那段）、安装位置与 `HANDOFF_INSTALL_DIR`、`handoff version` 的判读（`v0.1.0` vs `unknown`）、源码构建的 Go 1.26+ 要求。

代码块内容按 C2 逐字照抄，**一个字符都不改**。

- [ ] **Step 4: 验证本节无中文残留**

```bash
sed -n '1,/^## Quick Start/p' README.md | grep -nP '[\x{4e00}-\x{9fff}]'
```

Expected: 无输出（例外：语言切换条里的 `简体中文` 四个字。若只匹配到切换条那一行，视为通过）。

- [ ] **Step 5: 验证安装命令逐字一致**

```bash
for t in 'curl -fsSL https://handoff.gosuper.dev/install | bash' \
         'irm https://handoff.gosuper.dev/install.ps1 | iex' \
         'go build -o handoff . && sudo mv handoff /usr/local/bin/' \
         '%LOCALAPPDATA%\Programs\handoff\handoff.exe'; do
  a=$(grep -Fc "$t" README.md); b=$(grep -Fc "$t" README.zh-CN.md)
  [ "$a" = "$b" ] && echo "OK   $t" || echo "FAIL $t: en=$a zh=$b"
done
```

Expected: 全部 `OK`。

- [ ] **Step 6: Commit**

```bash
git add README.md
git commit -m "docs(readme): 英文版标题区、流程图与安装章节"
```

---

## Task 3: 英文版——快速开始、连接远程执行机、远程执行机

**Files:**
- Modify: `README.md`（`## Quick Start` / `## Connecting a Remote Executor Machine` / `## Remote Executor Machine`）
- Source: `README.zh-CN.md` 原 L64–155

**Interfaces:**
- Consumes: Task 2 定下的 coordinator / executor machine / dispatch 译法。

- [ ] **Step 1: 翻译 `## Quick Start`（原文 L64–112）**

5 个编号步骤全部保留：init 配置、service 托管 agentd、首个 dispatch、wait/reply 裁决、diff/continue/done 审核收尾。含三段易漏的散文：

- 不托管的 agentd 重启不回来、PATH 取决于启动它的 shell（"重启后第一次派发报 executor 未安装"的成因）
- 只当协调机时不需要本机 agentd，首次派发会顺带登记项目、没有本机 agentd 时自动跳过
- 末尾那条引用块（`>`）：协调者是 AI 会话时不必记命令；Claude Code / grok 有后台唤醒可挂 `wait --follow`，opencode / codex 没有

代码块里的中文行内注释要译成英文（如 `# 不写 plan 文件的小任务` → `# small task, no plan file`），但**注释前的命令本体逐字不动**。

- [ ] **Step 2: 翻译 `## Connecting a Remote Executor Machine`（原文 L114–128）**

含三种连通方式、`listen` 三档（含单网卡 IP 那档的辅助 loopback 监听与"IP 不在时起不来"的已知限制），以及**安全红线**整段。

安全红线段落是全文最不能弱化的内容：明文 HTTP/WS + Bearer token、无 TLS、token 被截获等于任意代码执行、带公网 IP 的云主机现阶段不要当执行机。英文版要保持同等强度，`**bold**` 强调照搬。

- [ ] **Step 3: 翻译 `## Remote Executor Machine`（原文 L130–155）**

三步配对流程 + yaml 片段 + 派发前必须 `git push` + 自动登记/自动 clone + "只 fetch 不合并"。

yaml 片段里的中文注释译成英文，键名 `targets` / `addr` / `token` / `user` 逐字不动。

- [ ] **Step 4: 验证三节无中文残留**

```bash
sed -n '/^## Quick Start/,/^## Command Reference/p' README.md | grep -nP '[\x{4e00}-\x{9fff}]'
```

Expected: 无输出。

- [ ] **Step 5: 验证关键字面量成对出现**

```bash
for t in 'handoff init' 'handoff service install' 'handoff dispatch --new-worktree plan.md' \
         'handoff wait <task> --notify' 'handoff done <task> --note' '127.0.0.1:7777' \
         '0.0.0.0:7777' 'handoff dispatch --target devbox --new-worktree plan.md'; do
  a=$(grep -Fc "$t" README.md); b=$(grep -Fc "$t" README.zh-CN.md)
  [ "$a" = "$b" ] && echo "OK   $t" || echo "FAIL $t: en=$a zh=$b"
done
```

Expected: 全部 `OK`。

- [ ] **Step 6: Commit**

```bash
git add README.md
git commit -m "docs(readme): 英文版快速开始与远程执行机章节"
```

---

## Task 4: 英文版——命令速查表、任务状态与事件

这两节是**表格密集**的，逐格翻译且列结构必须严格对齐。

**Files:**
- Modify: `README.md`（`## Command Reference` / `## Task States and Events`）
- Source: `README.zh-CN.md` 原 L157–203

- [ ] **Step 1: 翻译命令速查表（原文 L159–184，24 行数据行）**

表头 `| 命令 | 用途 | 关键参数 |` → `| Command | Purpose | Key flags |`。

**第一列（命令）与第三列里的所有 flag 逐字不动**，只译第二列与第三列的中文说明。特别注意这些必须原样保留的写法：

- `handoff service install\|uninstall\|status`（管道符的反斜杠转义不能丢，否则表格列会断）
- `handoff project add\|ls\|rm`
- `--executor=opencode\|claude\|grok\|codex\|fake`
- `--branch\|--new-branch <b>`、`--worktree <路径>\|--new-worktree`（后者中的 `<路径>` 译成 `<path>`）

`handoff run` 那格的说明含一条硬约束（"handoff 自有 flag 必须在任务名之前，任务名之后的一切原样透传"），不能压缩掉。

表格后那行全局参数说明（`--agentd` / `--target` / `--config`）一并译。

- [ ] **Step 2: 翻译 `## Task States and Events`（原文 L188–203）**

含状态机那段（`pending` → `running` → …）、"回合以失败收尾也进 `waiting_review`" 的关键澄清、`continue`/`done` 要求 `waiting_review` 否则 409、以及 6 行事件表。末尾 `progress` 与审批链审计事件"只入库不唤醒"那句要保留。

- [ ] **Step 3: 验证表格行数一致**

```bash
a=$(grep -c '^| ' README.md); b=$(grep -c '^| ' README.zh-CN.md)
[ "$a" = "$b" ] && echo "OK 表格行数 $a" || echo "FAIL en=$a zh=$b"
```

Expected: `OK`（此时英文版仅完成到 Task 4，中文版是全文，故此检查在 Task 8 才必然相等；本步若不等，记下差值，在 Task 8 复核）。

- [ ] **Step 4: 验证状态与事件名一个不漏**

```bash
for t in pending running waiting_answer waiting_review completed failed \
         permission_request question archived delivery_failed stalled permission_reuse; do
  grep -Fq "$t" README.md && echo "OK   $t" || echo "FAIL 缺失 $t"
done
```

Expected: 全部 `OK`。

- [ ] **Step 5: 验证管道转义没被吃掉**

```bash
grep -Fc 'handoff service install\|uninstall\|status' README.md
grep -Fc 'handoff project add\|ls\|rm' README.md
```

Expected: 各输出 `1`。若为 `0`，说明反斜杠丢了，表格会渲染错列——**必须修**。

- [ ] **Step 6: Commit**

```bash
git add README.md
git commit -m "docs(readme): 英文版命令速查表与任务状态事件表"
```

---

## Task 5: 英文版——配置参考、各 executor 须知

**Files:**
- Modify: `README.md`（`## Configuration Reference` / `## Executor Notes`）
- Source: `README.zh-CN.md` 原 L205–286

- [ ] **Step 1: 翻译 `## Configuration Reference`（原文 L205–274）**

标题带路径：`## Configuration Reference (~/.handoff/config.yaml)`。

主体是一大块 yaml（原文 L209–242）。**所有键名、默认值、示例值逐字不动**（`listen`、`token`、`executor.default`、`approver.timeout: 60s`、`blacklist` 里的 `"kubectl .*delete"`、`env.opencode: dev.env`、`terminal.auto`、`sync.auto`、`repo_root`、`path_dirs: ["/opt/tools/bin"]`、`proxy`、`proc_fence.disabled`、`reserve_ratio: 0.1`），只译行尾注释。注释译后**保持 `#` 对齐**，别让 yaml 块看起来参差。

随后三段散文全部保留：

- **`proxy` 段**：作用范围只有更新链路与 agentd 的 git clone/fetch；比 `HTTPS_PROXY` 环境变量实用的理由（agentd 由 launchd/systemd 拉起读不到 shell env）；三条边界（不作用于协调者↔agentd 链路、不作用于 executor、SSH 协议 remote 吃不到，含那条 `git config --global url."https://github.com/".insteadOf git@github.com:` 命令）；以及"值写错时 agentd 启动就失败"是**刻意设计**的理由。
- **`env` 段**：纯文件名写法、文件放执行机的 `~/.handoff/env/`、dotenv 格式示例块（含 `export` 前缀可选、`${VAR}` 单层展开、单引号内不展开三条注释）、同一份 env 也注入审批者的原因。
- **权限分级**：三级分流、审批者连续失败 3 次自动停用、同一权限请求批过一次自动复用（`permission_reuse` 留痕）。

- [ ] **Step 2: 翻译 `## Executor Notes`（原文 L276–286）**

四个 executor 各自的就绪判据与坑：

- opencode：**邀请链接 `https://opencode.ai/go?ref=3AMC8DKNGP` 原样保留**，并保留"这是邀请链接，经它注册你我各得 $5 额度"的披露——**这条披露不能删**，删了就是隐瞒返利关系。
- claude：已登录判据 `claude -p "hi"`；权限策略文件不含凭证。
- grok：已登录判据 `grok -p "hi"`；会读执行机上 Claude Code 的 `~/.claude/settings.local.json` allow 规则导致部分操作被放行；断连时未决权限请求不重发、任务落 failed。
- codex：三条须知全保留（清理 `~/.codex/AGENTS.md` / `hooks.json` / `[mcp_servers]`，但 `model`/`sandbox_mode` 不用清；权限模型不同——工作区内含 `rm -rf` 走 OS 沙箱不进审批；必须经 `env` 段配代理，漏配的症状是 `serve.log` 里刷 `failed to refresh available models`）。

- [ ] **Step 3: 验证两节无中文残留**

```bash
sed -n '/^## Configuration Reference/,/^## Upgrading/p' README.md | grep -nP '[\x{4e00}-\x{9fff}]'
```

Expected: 无输出。

- [ ] **Step 4: 验证配置键与邀请链接一个不漏**

```bash
for t in 'listen:' 'token:' 'repo_root' 'path_dirs' 'proc_fence' 'reserve_ratio' \
         '"kubectl .*delete"' 'dev.env' 'sync.auto' 'socks5h://' \
         'https://opencode.ai/go?ref=3AMC8DKNGP' 'failed to refresh available models'; do
  a=$(grep -Fc "$t" README.md); b=$(grep -Fc "$t" README.zh-CN.md)
  [ "$a" = "$b" ] && echo "OK   $t" || echo "FAIL $t: en=$a zh=$b"
done
```

Expected: 全部 `OK`。`https://opencode.ai/go?ref=3AMC8DKNGP` 此时英文版只出现 1 次（另 1 次在 Task 7 的 Links 节），故本步该项显示 `FAIL en=1 zh=2` 属预期——记下，Task 8 复核必须为 2。

- [ ] **Step 5: Commit**

```bash
git add README.md
git commit -m "docs(readme): 英文版配置参考与 executor 须知章节"
```

---

## Task 6: 英文版——升级、会话恢复、Troubleshooting

**Files:**
- Modify: `README.md`（`## Upgrading` / `## Session Recovery` / `## Troubleshooting`）
- Source: `README.zh-CN.md` 原 L288–362

- [ ] **Step 1: 翻译 `## Upgrading`（原文 L288–313）**

要点：升级由人触发无定时自动更新；默认执行机自拉（协调者只下发 tag + sha256，省带宽，云中转时是决定性差别）；完整性由协调者下发的 sha256 把关（两条信任路径，本机代理被投毒当场抓住）；`--push` 回退；对端过旧自动降级推送；4 条 `handoff upgrade` 命令；两道安全闸（有活跃任务默认拒升 `--force` 可越过；agentd 非托管拒升且 `--force` 也不越过）；三平台都能自更新含 Windows 的 rename 手法；CLI 每天最多后台查一次。

- [ ] **Step 2: 翻译 `## Session Recovery`（原文 L315–324）**

协调者不持有状态 → 崩溃/断网/换机是同一套动作；`handoff tasks` + `handoff show <task>`；清完 `pending_tickets` 后按状态办。

- [ ] **Step 3: 翻译 `## Troubleshooting`（原文 L326–362）**

含日志位置那段（`~/.handoff/agentd.log`、`HANDOFF_LOG_LEVEL=debug`、任务目录下 `render.log` / `serve.log` 各自的用途）、14 行症状表、以及 macOS 隔离标记与 Windows SmartScreen 两小节。

症状表第一列的报错原文（`not found in $PATH`、`resource temporarily unavailable`、`task not found`）与错误码逐字不动；`xattr -d com.apple.quarantine ~/.local/bin/handoff` 逐字不动。

- [ ] **Step 4: 验证三节无中文残留**

```bash
sed -n '/^## Upgrading/,/^## Uninstall/p' README.md | grep -nP '[\x{4e00}-\x{9fff}]'
```

Expected: 无输出。

- [ ] **Step 5: 验证排障关键字面量**

```bash
for t in 'HANDOFF_LOG_LEVEL=debug' '~/.handoff/agentd.log' 'render.log' 'serve.log' \
         'not found in $PATH' 'resource temporarily unavailable' \
         'xattr -d com.apple.quarantine ~/.local/bin/handoff' \
         'handoff upgrade --now --push' 'update.pull_state'; do
  a=$(grep -Fc "$t" README.md); b=$(grep -Fc "$t" README.zh-CN.md)
  [ "$a" = "$b" ] && echo "OK   $t" || echo "FAIL $t: en=$a zh=$b"
done
```

Expected: 全部 `OK`。

- [ ] **Step 6: Commit**

```bash
git add README.md
git commit -m "docs(readme): 英文版升级、会话恢复与 Troubleshooting 章节"
```

---

## Task 7: 英文版——卸载、即将推出、文档、友情链接、License

尾部五节，含全部跨文档链接——这是最容易留下失效引用的一批。

**Files:**
- Modify: `README.md`（`## Uninstall` 起至文末）
- Source: `README.zh-CN.md` 原 L364–392

- [ ] **Step 1: 翻译 `## Uninstall`（原文 L364–370）**

三条命令逐字不动，`rm -rf ~/.handoff` 那行的行尾警告注释（"含配置、任务数据与日志，确认不要了再删"）必须译出来，**不能省**——这是删除操作的唯一护栏。

- [ ] **Step 2: 翻译 `## Coming Soon`（原文 L372–375）**

两条：云服务器中转连接、桌面端。

- [ ] **Step 3: 翻译 `## Documentation`（原文 L377–383）**

5 条链接。**路径全部逐字不动**：

- `docs/superpowers/specs/2026-08-07-handoff-design.md`
- `skills/handoff/SKILL.md`
- `deploy/handoff-agentd.service`（含 `KillMode=process` 是硬要求的说明）
- `CONTRIBUTING.md`
- `SECURITY.md`

后三份是中文文档，链接文字后加 `(Chinese)` 标注，避免英文读者点进去扑空。前两份中 `SKILL.md` 也是中文，同样标注；设计文档同理。**逐条确认目标文件存在**（见 Step 6）。

- [ ] **Step 4: 翻译 `## Links`（原文 L385–388）**

两条：Linux Do 社区、opencode。**opencode 那条的邀请链接与返利披露原样保留**（同 Task 5 Step 2 的理由）。

- [ ] **Step 5: 写 `## License`（原文 L390–392）**

```markdown
[Apache License 2.0](LICENSE)
```

- [ ] **Step 6: 验证全部相对链接指向真实存在的文件**

```bash
grep -oP '\]\(\K[^)#]+(?=\))' README.md | grep -v '^http' | while read -r p; do
  [ -e "$p" ] && echo "OK   $p" || echo "FAIL 不存在: $p"
done
```

Expected: 全部 `OK`。同一命令对 `README.zh-CN.md` 跑一遍，也应全 `OK`。

- [ ] **Step 7: Commit**

```bash
git add README.md
git commit -m "docs(readme): 英文版卸载、文档与链接章节，英文版全文完成"
```

---

## Task 8: 全文终验

对照 spec §4 的 8 条验收标准逐条跑一遍。这一步不写新内容，**只验证与修缺**。

**Files:**
- Verify: `README.md`、`README.zh-CN.md`、`CONTRIBUTING.md`
- Modify: 仅在发现缺陷时回改

- [ ] **Step 1: 历史完整**

```bash
git log --follow --oneline README.zh-CN.md | wc -l
```

Expected: > 1。

- [ ] **Step 2: 二级标题数量与顺序对应**

```bash
paste <(grep -n '^## ' README.md) <(grep -n '^## ' README.zh-CN.md)
```

Expected: 16 行，逐行左右语义一一对应（人工目视，顺序不能错位）。

- [ ] **Step 3: 语言切换条互链可通**

```bash
head -5 README.md; echo '---'; head -5 README.zh-CN.md
```

Expected: 两份的第 3 行都是同一条切换条。

- [ ] **Step 4: 英文版无中文残留**

```bash
grep -nP '[\x{4e00}-\x{9fff}]' README.md
```

Expected: **只允许**一行命中——语言切换条里的 `简体中文`。其余任何一行命中都必须修掉。

- [ ] **Step 5: 代码围栏数量一致**

```bash
a=$(grep -c '^```' README.md); b=$(grep -c '^```' README.zh-CN.md)
[ "$a" = "$b" ] && echo "OK 围栏 $a" || echo "FAIL en=$a zh=$b"
```

Expected: `OK`，且数值为偶数（围栏成对）。

- [ ] **Step 6: 表格行数一致**

```bash
a=$(grep -c '^| ' README.md); b=$(grep -c '^| ' README.zh-CN.md)
[ "$a" = "$b" ] && echo "OK 表格行 $a" || echo "FAIL en=$a zh=$b"
```

Expected: `OK`。这一步是 Task 4 Step 3 记下差值的复核点。

- [ ] **Step 7: 全量字面量对账**

```bash
for t in 'curl -fsSL https://handoff.gosuper.dev/install | bash' \
         'irm https://handoff.gosuper.dev/install.ps1 | iex' \
         'https://opencode.ai/go?ref=3AMC8DKNGP' \
         '~/.handoff/config.yaml' '~/.handoff/env/' '~/.local/bin/handoff' \
         'waiting_review' 'permission_request' 'delivery_failed' 'update.pull_state' \
         'reserve_ratio' 'path_dirs' 'proc_fence' 'KillMode=process' \
         'xattr -d com.apple.quarantine ~/.local/bin/handoff'; do
  a=$(grep -Fc "$t" README.md); b=$(grep -Fc "$t" README.zh-CN.md)
  [ "$a" = "$b" ] && echo "OK   $t" || echo "FAIL $t: en=$a zh=$b"
done
```

Expected: 全部 `OK`。`https://opencode.ai/go?ref=3AMC8DKNGP` 必须两边都是 `2`。

- [ ] **Step 8: 全仓无失效 README 引用**

```bash
grep -rn 'README\.md' --include='*.md' --include='*.go' --include='*.sh' --include='*.ps1' \
  --include='*.yml' --include='*.yaml' . | grep -v '^\./docs/superpowers/' | grep -v '^\./README'
```

Expected: 逐条人工确认——指向英文 README 的保持不变；`CONTRIBUTING.md` 那条必须已改成 `README.zh-CN.md`；测试文件里作为测试夹具的 `README.md` 字符串（`cmd/dispatch_dirty_test.go`、`internal/localsync/localsync_test.go`、`internal/executor/opencode/adapter_test.go`）**不动**——它们是临时文件名，与本仓的 README 无关。

- [ ] **Step 9: CONTRIBUTING 同步约定已就位**

```bash
grep -A3 '^## 文档同步' CONTRIBUTING.md
```

Expected: 输出那条"必须同时更新这两份"的约定。

- [ ] **Step 10: 渲染目视检查**

在 GitHub 的 Markdown 预览（或本地渲染器）里打开英文 `README.md`，确认：

- ASCII 流程图竖线对齐、没有错位
- 命令速查表所有列对齐、没有因 `\|` 丢失而断列
- yaml 配置块的 `#` 注释对齐整齐
- 语言切换条可点，点进中文版能点回来

- [ ] **Step 11: Commit（若有修缺）**

```bash
git add -A
git commit -m "docs(readme): 双语终验修缺"
```

若 Step 1–10 全过且无改动，跳过本步。

---

## 完成后

回填 backlog B89：`🔨 doing` → `✅ done`，`验收` 列写清 Task 8 各步的实际输出（哪几条 `OK`、有无修缺）。`原型/流程图` 为 `—`，自动免除对照。
