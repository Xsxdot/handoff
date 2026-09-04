# B319 spec：协调者流程走查演示（演示卡，可关闭）

级别：L2（单子系统：账本/协作走查，不碰生产代码）
选档：plan → implement（演示仅跑 plan 一轮即收口，implement 不派发）

## 背景

用户 2026-09-03 要求：更新仓库到最新后建卡，走一遍协调者流程（含房间收发、真实派发）。
仓库已从 91ffc4a 快进到 a930e3e14。卡 B319 为演示卡。

## 范围

- 只动演示资产：`docs/superpowers/plans/b319-plan.md` 由 plan 节点自动产出；不改 `.go`/`.ts` 生产代码。
- 协调者动作：bind（预期在无会话身份终端失败并记录）、room send/read、decision open/answer、card dispatch --step plan、card wait、show/diff、note。
- 回复演示：decision 应答 + 房间跟帖；如派发出现 task 工单则用 reply 应答。

## 验收判据（卡级持久字段，节点中立）

- 整张卡算通过当且仅当：plan 节点跑完且裁决 pass、plan 附件自动挂卡、卡自动进 implement 列、房间有启动与收口两条叙事消息（答得上发生了什么/为什么/意味着什么，证据给链接、正文不贴代码）。
- 本节点该干什么一律以注入的纪律块为准。

## 超出范围

- 不做 implement 派发、不合并主线、不改生产代码与配置。
- 演示结束关闭 B319（搁置），不进 backlog 统计。
