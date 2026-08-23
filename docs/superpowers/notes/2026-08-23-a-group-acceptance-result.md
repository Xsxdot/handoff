# A 组真机验收结果

> 2026-08-23 执行。被验对象：分支 `feat/a-group-fixes`，提交 `45d5114de23b`。
> 清单原文见同目录 `2026-08-23-a-group-acceptance-checklist.md`。

## 靶场怎么搭的（复现用）

不动本机生产 agentd（launchd 托管、旧二进制 `985f37135b96`），另起一套隔离实例：

```bash
OUT=/tmp/hoacc/bin/handoff bash scripts/build-deploy.sh   # 版本戳按当前 worktree 注入
# config: listen 127.0.0.1:7788, datadir /tmp/hoacc/data, ledger.enabled: true, executor.default: fake
/tmp/hoacc/bin/handoff agentd --config /tmp/hoacc/config.yaml
```

- DataDir 放 `/tmp` 下的短路径（PTY socket 有 104 字节上限）。
- 账本是空的，seed 出来的模板里没有 `charter-default`——从生产账本导出、把
  `executor` 改成 `fake` 再 `template put`；纪律块 md 从 `~/.handoff/discipline/` 拷过去。
- 靶仓库 `/tmp/hoacc/proj`（带 bare origin），`project add` 登记。
- `linux-01` 本轮全程够不着：relay 报 `QUOTA_EXCEEDED`。所以真机验收全在本机隔离实例上跑。

## 逐条结果

| 卡 | 结论 | 判据与证据 |
|---|---|---|
| **B180** | ✅ 通过 | 真派一轮 grok。事件流 `seq12 completed → seq13 progress`，**无 `turn_failed`**，任务停 `waiting_review`。agentd 日志同秒（10:33:06.877→.886）出现守卫行「回合已收口后到达的失败结果，判为传输层假警报并丢弃 state=waiting_review fail_reason=ACP 连接断开…EOF」。缺陷现场自然发生、被当场拦下 |
| **B168** | ✅ 通过 | 新 CLI 对 `charter-v4.json`（nodes-only）put 成功：`nodes=12 dispatch_nodes=7`。同一份文件、同一个 agentd，旧 CLI `985f37135b96` 拒「状态序列至少两个状态」。反向：`{}` 被拒「nodes 与 states 至少给一个」 |
| **B182** | ✅ 通过 | 同一张卡两个环节 A/B：plan（`omit_acceptance=true`）prompt 里判据 marker 命中 **0** 次；implement（`false`）命中 **2** 次 |
| **B183** | ✅ 通过（带保留） | `--step review` 派发行：`purpose=review purpose_overridden=true branch=cards/B1-review-1 base=cards/B1-charter`；第二轮得 `-review-2` 不撞名；审阅工作树尖端就是实现提交 `ab56095`，README 里有 `IMPLEMENTED-BY-EXECUTOR`。非审阅重跑得 `cards/B1-charter-2`。**保留项见下面 B195** |
| **B184** | ✅ 通过（带保留） | 三态同屏：有驱动无订阅 →「⚠ 无人订阅（卡 B2 驱动 cli:…#14647，心跳 0m 前）」；无驱动无订阅 →「⚠ 无人值守」；`waiting_review` 与有订阅的任务静默。**保留项见下面 B196** |
| **B169** | ✅ 通过 | 浏览器 1280x900 真实鼠标点击第三列的「不注入验收判据」label（504,282），前后快照 diff **只有** `flow-node-2-omit-acceptance` 翻转。控件 id 已是下标键 |
| **B186** | ✅ 通过 | `go test -count=1 ./...` 三轮，`internal/ptyhost` 及 4 个子包全绿；三轮后 `git status --porcelain internal/ptyhost/` 均空 |
| **B159 / B162** | ⏸ 未验 | 真机侧判据是「后续几次 ubuntu CI 上是否还偶发」，本轮做不了。已在卡上落 `--unverified` 与说明，等分支进 CI 后再改 |

## 新发现（都已开卡，不 reopen 原卡）

| 卡 | 优先级 | 事 |
|---|---|---|
| **B195** | 高 | **审阅环节远程派发必被拒**。B183 让审阅轮 `base` = 卡的工作分支，而 agentd 的 `ResolveBaseBranch` 按设计**无条件从 origin 补拉**；工作分支只存在于执行机本地，要到 merge 环节才第一次推 origin。两条设计相撞 → `--step review --target …` 稳定 400。决定性实验：手工把工作分支 push 到 origin 后同一条命令立刻成功 |
| **B196** | 中 | **`card dispatch --step` 不认领驱动**。`cmd/card_dispatch.go` 里 `--step` 非空就直接 `return runStepDispatch(...)`，认领写在它下面的普通派发路径。后果：① B184 的「无人订阅」在 charter 节点流上永远走不到；② 节点流没有并发认领保护 |
| **B197** | 低 | `omit_acceptance` 的替换文案与模板尾括号并置，prompt 第 2 行变成「（本节点不注入整卡验收判据…）（这是整卡的最终验收判据…）」自相矛盾。功能不受影响 |
| **B193** | — | 原卡「未识别的偶发红」**身份抓到了，是两族**：① 本机临时端口耗尽 `connect: can't assign requested address`（40 处，横跨 agentd/grok/opencode，属环境压力）；② `TestPtyWSResumeSince` 的 `TempDir RemoveAll cleanup: directory not empty`（用例体通过，红在 cleanup，说明返回时还有活写者）。已把两族签名落进卡 |

## 还没做的部署动作

本轮**没有**动生产：没换本机二进制、没 `handoff skill install`、没往生产账本
`workflow put charter`。`linux-01` 也因 relay 配额够不着，九个任务仍停在
`waiting_review`。这些是操作者的决定，清单第 0 节的顺序仍然成立
（先二进制 → 再 skill → 最后工作流）。

**建议**：B195 是 B183 主功能在真实远程流程里的拦路石，可以先修 B195 再往
生产推 charter v4；否则推上去之后审阅环节一派就 400。
