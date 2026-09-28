# B409.5 单机端到端等价链路证据（真 CLI 二进制 × 两进程 × 共享隔离 PG）

- 日期：2026-09-26；分支 codex/session-reliable-mentions；HEAD 见下文
- CLI 构建物：go build -o /tmp/b409-u5-e2e/handoff . （真二进制，非测试进程）
- 共享账本：postgres://127.0.0.1:59849/handoff_b409_test（专用可丢弃库，PostgreSQL 16.15）；两进程各自经 config.ledger.dsn 打开独立连接
- member：agent:e2e-u5（跨进程同身份）；生产者身份：--agent e2e-producer（会话 owner，actor 出示语义见 B358.9）
- 覆盖：首挂空等(124) → 停听投递（@有效×2、无效 token、@他人）→ 重挂补收（冻结 session_backlog 格式，恰 2 hits）→ 再挂不回放(124) → 另一进程 follow 实时（冻结 SessionWake 形状，@他人不叫醒）→ 空闲超时 124
- 最终持久游标：140132（probe 直读 session_delivery_cursors，恰为最后一条已输出命中 seq）
- 跨机器真机重挂：留 S3 故事级验收（协调者），本证据为同机两进程等价链路

```
+ BIN=/tmp/b409-u5-e2e/handoff
+ CFG=/tmp/b409-u5-e2e/data/config.yaml
+ echo '== 环境 =='
== 环境 ==
+ git -C /Users/xushixin/.codex/worktrees/session-loading-diagnosis/handoff rev-parse HEAD
9bc2ce9246af0fc319dae56042364f6ba8da468b
++ go version
++ true
+ echo 'PG: '
PG: 
+ echo '== 步骤1 listener 首挂（无积压，空等 300ms→124）=='
== 步骤1 listener 首挂（无积压，空等 300ms→124）==
+ /tmp/b409-u5-e2e/handoff --config /tmp/b409-u5-e2e/data/config.yaml session wait agent:e2e-u5 --timeout 300ms
+ echo exit=124
exit=124
+ echo '== 步骤2 listener 已停，生产者建会话（agent:e2e-producer 出示身份）=='
== 步骤2 listener 已停，生产者建会话（agent:e2e-producer 出示身份）==
+ /tmp/b409-u5-e2e/handoff --config /tmp/b409-u5-e2e/data/config.yaml session create 'U5 E2E 场' --owner agent:e2e-producer
{"id":"session:2","title":"U5 E2E 场","owner":"agent:e2e-producer","archived":false,"members":["agent:e2e-producer"],"created_at":"2026-09-26T19:02:24.950122+08:00","updated_at":"2026-09-26T19:02:24.950122+08:00"}
+ echo '== 步骤3 停听期间投递：@e2e-u5 ×2（含无效 token 行）、@other-b ×1 =='
== 步骤3 停听期间投递：@e2e-u5 ×2（含无效 token 行）、@other-b ×1 ==
+ /tmp/b409-u5-e2e/handoff --config /tmp/b409-u5-e2e/data/config.yaml session send session:2 '@agent:e2e-u5 停听期间第一条' --agent e2e-producer
{"ok":true,"seq":140127}
+ /tmp/b409-u5-e2e/handoff --config /tmp/b409-u5-e2e/data/config.yaml session send session:2 '@agent:e2e-u5 停听期间第二条，@B23x 无效不寻址' --agent e2e-producer
{"ok":true,"seq":140128}
+ /tmp/b409-u5-e2e/handoff --config /tmp/b409-u5-e2e/data/config.yaml session send session:2 '@agent:other-b 发给别人' --agent e2e-producer
{"ok":true,"seq":140129}
+ echo '== 步骤4 同 member 同 DB 重挂：补收 backlog（应恰 2 hits、@other 不在）=='
== 步骤4 同 member 同 DB 重挂：补收 backlog（应恰 2 hits、@other 不在）==
+ /tmp/b409-u5-e2e/handoff --config /tmp/b409-u5-e2e/data/config.yaml session wait agent:e2e-u5 --timeout 5s
{"type":"session_backlog","member":"agent:e2e-u5","from_seq":0,"to_seq":140128,"hits":[{"hit":{"seq":140127,"room":"session:2","actor":"agent:e2e-producer","body":"@agent:e2e-u5 停听期间第一条"}},{"hit":{"seq":140128,"room":"session:2","actor":"agent:e2e-producer","body":"@agent:e2e-u5 停听期间第二条，@B23x 无效不寻址"}}]}
+ echo exit=0
exit=0
+ echo '== 步骤5 再次重挂：已交付不得回放（应 124 无输出）=='
== 步骤5 再次重挂：已交付不得回放（应 124 无输出）==
+ /tmp/b409-u5-e2e/handoff --config /tmp/b409-u5-e2e/data/config.yaml session wait agent:e2e-u5 --timeout 400ms
+ echo exit=124
exit=124
+ echo '== 步骤6 follow 常驻（另一进程）+ 生产者实时投递 3 条 =='
== 步骤6 follow 常驻（另一进程）+ 生产者实时投递 3 条 ==
+ FOLLOW_PID=25999
+ sleep 1.5
+ /tmp/b409-u5-e2e/handoff --config /tmp/b409-u5-e2e/data/config.yaml session wait agent:e2e-u5 --follow --timeout 6s
+ /tmp/b409-u5-e2e/handoff --config /tmp/b409-u5-e2e/data/config.yaml session send session:2 '@agent:e2e-u5 follow 实时第一条' --agent e2e-producer
{"ok":true,"seq":140130}
+ /tmp/b409-u5-e2e/handoff --config /tmp/b409-u5-e2e/data/config.yaml session send session:2 '@agent:other-b follow 别人的' --agent e2e-producer
{"ok":true,"seq":140131}
+ /tmp/b409-u5-e2e/handoff --config /tmp/b409-u5-e2e/data/config.yaml session send session:2 '@agent:e2e-u5 follow 实时第二条' --agent e2e-producer
{"ok":true,"seq":140132}
+ wait 25999
+ echo follow_exit=124
+ echo '--- follow 进程 stdout 原文 ---'
--- follow 进程 stdout 原文 ---
+ cat /tmp/b409-u5-e2e/follow.out
{"session":"session:2","hit":{"seq":140130,"room":"session:2","actor":"agent:e2e-producer","body":"@agent:e2e-u5 follow 实时第一条"},"unread":6}
{"session":"session:2","hit":{"seq":140132,"room":"session:2","actor":"agent:e2e-producer","body":"@agent:e2e-u5 follow 实时第二条"},"unread":6}
follow_exit=124
```
