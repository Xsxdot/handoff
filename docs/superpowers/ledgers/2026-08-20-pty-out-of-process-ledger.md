# PTY 托管出 agentd 进程实现 ledger

| Task | 轮次 | 结果 | commit 范围 |
|---|---:|---|---|
| Task 1 | 1 | 双裁决通过：已确认 executor 围栏、机器级压力与任务级压力的统计口径；spec 已回写，追加后续排除 ptyhost task | `504f5d12` |
| Task 2 | 1 | 双裁决通过：帧布局、JSON 控制帧、EOF 区分、1 MiB 上限与未知字段/类型兼容测试通过 | `internal/ptyhost/wire/**` |
