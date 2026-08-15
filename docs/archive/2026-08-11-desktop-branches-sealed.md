# 桌面端分支封存清单（2026-08-11）

基于 Orca 快照的 Electron 桌面端实现路线于本日封存。决策见
`docs/adr/0009-desktop-console-is-agentd-hosted-web-ui.md`，证据见
`docs/superpowers/reviews/2026-08-11-desktop-form-factor-assessment.md`。

**分支一律保留，未删除，未推送。** 本文记录它们的状态与相互关系，
以便任何时候能准确取回其中的代码。

## 一、只封存代码，设计资产已迁出

`main` 上**没有** `desktop/` 目录——桌面端代码只存在于下列分支。

但设计资产此前处于**从未入库**的状态（只在 codex 工作树里以未跟踪文件存在），
本次已迁至本分支 `archive/desktop-console-2026-08-11`：

| 资产 | 内容 |
|---|---|
| `CONTEXT.md` | 桌面端领域通用语言（项目 / 项目位置 / 代码宿主 / 工作树 / 执行器 / 任务现场 / 工作台 …）。**与前端形态无关，改用 Web UI 后依然全部适用。** |
| `docs/adr/0001`–`0008` | 产品决策记录。同样与形态无关。 |
| `prototypes/desktop-console/` | 原型站源码与 26 张成品设计图，含 `implementation-task-board-final.png`（任务看板终稿）。已排除 `node_modules` 与 `dist`。 |

桌面端的 spec 与 plan（`docs/superpowers/specs/2026-08-09-handoff-desktop-vertical-slice-design.md`、
`docs/superpowers/plans/2026-08-09-handoff-desktop-*.md` 共 6 份）**此前只在
`codex/plan02-workspace-resources-rest` 上，main 上一份都没有**——本文初版误记为
「本就已在 main 中」，2026-08-11 订正。它们已随 Web UI 开工分支
`handoff/web-console` 迁出，因为 spec §150 正是 ADR-0009 的承重理由，
不能和被封存的代码绑在同一个分支上。

## 二、分支拓扑

`codex/plan02-workspace-resources-rest` **严格包含其余全部分支**——其他分支均无独有提交。
需要取回代码时只看它一个即可。

| 分支 | HEAD | 相对 main | 说明 |
|---|---|---|---|
| **`codex/plan02-workspace-resources-rest`** | `74091cbd` | +37 | **唯一的顶点**。工作区资源 REST 层，含本次五个缺陷修复 |
| `codex/phase2-desktop-completion` | `b027816b` | +27 | plan02 的祖先，0 个独有提交 |
| `codex/phase2-desktop-01-control-plane-r5` | `0a9088f0` | +22 | 控制平面第 5 轮，祖先 |
| `codex/phase2-desktop-01-control-plane-r1`–`r4` | `4cdd564e` | +2 | 四个分支同一 SHA，早期迭代，祖先 |

`codex/repair-opencode-sse-oversize`（+5）不属于桌面端工作（executor SSE 修复），不在本次封存范围，未作处理。

工作树 `/Users/xushixin/.codex/worktrees/4dc2/handoff` 目前检出在 `codex/plan02-workspace-resources-rest`。
它先前检出在 `codex/phase2-desktop-completion`；因后者是前者的祖先且无独有提交，未做还原。

## 三、封存前补提交的内容

`74091cbd` 是本次封存时新增的唯一提交，内容是本地补跑 E2E 后发现并修复的五个缺陷。
提交它是为了避免这些工作以未提交状态留在工作树中随清理丢失。

计划 02 的执行者自陈从未实跑 E2E；这五个缺陷全部位于该未执行层。修复后 E2E 6/6 通过。

| # | 缺陷 | 换成 Web UI 后是否仍然存在 |
|---|---|---|
| 1 | 主进程路径守卫拒绝空串（workspace 根），文件树永久停在「加载中…」，全工作区搜索不可用 | **是**。空串=工作区根是 Go → 客户端 → 前端全链路约定，任何客户端都要遵守 |
| 2 | E2E fake 的 `close()` 未关闭 resource-stream 套接字，每个场景耗尽 120s 超时 | 测试夹具问题，形态无关 |
| 3 | 错误码在 IPC 与 contextBridge **两道**边界均被裁剪，保存冲突对话框从不出现，用户编辑被静默丢弃 | **否，从构造上消失**。浏览器直接拿 JSON，无序列化边界 |
| 4 | 预览 webview 的 partition 未登记进白名单，`will-attach-webview` 直接 `preventDefault`，guest 从不 attach，无报错无日志无事件；生产环境同样永久白屏 | **否**。Electron `<webview>` 专有 |
| 5 | `resource-client` / `pty-client` 在 401/403 路径留下未读响应体，Node undici 会挂住 HTTP/1 解析器并可能崩进程 | **否**。浏览器 fetch 不走 Node undici |

即五个中有三个随该层一并消失。它们仍是真缺陷、修得也对，
且正是实跑它们的过程提供了「主进程只是转发层」与「中心注册表会静默失败」这两条判断依据。

**教训中可迁移的部分**：
- 缺陷 #1 的空串约定，任何新客户端都要遵守。
- 缺陷 #4 的类型——**中心注册表式门禁若失败时不报错，就会变成永久静默故障**。新架构里凡引入白名单/注册表，必须让未登记项在失败时留下可见信号。

## 四、如何取回

```bash
# 查看桌面端代码
git checkout codex/plan02-workspace-resources-rest

# 只取某个文件而不切分支
git show codex/plan02-workspace-resources-rest:desktop/src/renderer/src/features/handoff/HandoffApp.tsx

# 列出 handoff 自有的全部前端代码（迁移到 Web UI 时的主要素材，约 6970 行，估计 80% 可直接搬）
git ls-tree -r --name-only codex/plan02-workspace-resources-rest -- desktop/src/renderer/src/features/handoff
```

迁移时应带走的东西：
- `desktop/src/renderer/src/features/handoff/`（6970 行，主体）
- `desktop/src/renderer/src/components/terminal-surface/XtermSurface.tsx`（194 行，handoff 自建）
- `desktop/config/patches/` 五个 xterm 补丁与 `package.json` 的 `patchedDependencies` 段——
  已实测在浏览器中全部可用，其中输入法补丁在 Web 下更重要
- `desktop/src/renderer/src/lib/pane-manager/pane-terminal-options.ts`（76 行 xterm 配置）

不需要带走的：`desktop/src/main/handoff/`（2553 行）、`desktop/src/preload/handoff*`（325 行）、
`desktop/src/shared/handoff/ipc-error.ts`（87 行）——这些是 IPC 转发层，Web 形态下整体消失。

## 五、遗留事项

1. **`SSH_AUTH_SOCK` 未透传进 agentd 的 PTY 会话**——该终端内 `git push` / `ssh` 会失败，
   且行为随 agentd 托管方式静默变化。与形态无关，是**现存代码的真缺陷**，与 B54.2 的服务化托管直接相关。
   详见评估记录第六节。
2. **ADR-0003 与 spec §41 冲突**且未标注取代关系。见 ADR-0009 末节。
3. 本次全部工作**均未推送**。分支只存在于本地。
