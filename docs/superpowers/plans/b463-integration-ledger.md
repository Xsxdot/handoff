# B463 协调者隔离集成证据

## 偏离与自主决定

- 采用 HEAD `5ba19a794` 加 S2 文件的独立临时快照构建，避免把并行施工的 S3 混入验收二进制。隔离 agentd、HOME/XDG、工作区和测试 PG；未使用生产模型凭据。真实 CLI 是 opencode 1.18.32，模型只使用 loopback 确定性协议 fixture，不能证明实际模型判断质量。
- 准入探测首次把任务工具模型用作载体健康模型，因健康请求触发工具而返回“回合无输出”；改用纯文本健康模型后重新完整运行。首次失败保留于实验脚本修订范围，没有跳过载体健康守卫。

## 真实调用链

原始报告 `/tmp/b469-p0-runtime/s2-adapter/report-five-cases.json`，实际流程为真实 opencode bash permission → adapter → Authority/Gate → 真实 opencode OneShot → 原生权限回答/执行；不是直接调用 helper 的夹具测试。新 agentd 进程未设置 `HANDOFF_LOG_LEVEL`，独立退出码 0，所有实验任务经支持的 stop API 清理。

| 输入 | 结果 |
|---|---|
| `for w in tasks show; do n=$(grep -c "handoff $w" README.md); printf "%s %s\\n" "$w" "$n"; done` | 原生权限触发，非自指令；审批 fixture approve，真实命令执行后产生完成 ask。8.046 秒 |
| 改名真实二进制 `"/tmp/b469-p0-runtime/s2-adapter/handoff-fc" tasks --help` | 审批 approve，真实 help 执行。9.489 秒 |
| `env \| cut -d= -f1` | 审批 approve，只枚举隔离进程的名称。7.371 秒；不读取宿主环境值 |
| `handoff dispatch T1; handoff tasks` | 原 gate 工单，原因 self-command dispatch；没有进入审批 fixture。2.694 秒 |
| `sh -c "handoff $action"` | 原 gate 工单，执行包装器中的动态自指令仍硬升级；没有进入审批 fixture。2.651 秒 |

前三项只证明原本误报的输入可以经现有审批路径放行；没有扩充静态白名单，不能据此承诺所有 help/env 命令自动批准。危险两项保持等待 gate，实验没有回答批准。

该实验同时补上 P0 原 permission 工单真实产生证据；P0 早前重启后回答/短回合释放证据属于 ask 分支，不把两条实验拼成“permission 重启恢复也已实测”。

## 变异

协调者以 Go overlay 将 S2 AST 回调的首次命中守卫改为 `if hit && false`，未更改共享工作树。`TestB463ShellSyntaxFalsePositives` / `TestAuthorityB463ShellSyntaxFalsePositiveConsults` 编译成功后红：后续只读节点和循环体覆盖先前变更命中。撤掉 overlay 同命令绿。原始 `/tmp/b469-p0-runtime/s2-mutated.log`、`s2-restored.log`。

## 未验

- 真实模型判断、非本机平台。
- 本轮失败日志脱敏及超时分类 Important 已由实现者修复，独立审查经原真实 CLI 四场景复跑核销，详见 `b463-review-ledger.md`。
- 本证据是 S2 的集成阶段；不是 S3 路由/跨机验收，也不是全仓测试通过记录。

## 历史样本逐条集成补证

`/tmp/b469-p0-runtime/s2-adapter/report.json` 后续完整运行 15 例：13 个 B461 原子命令的 `--help` 和 B453 原始两条 `env | cut -d= -f1 | sort` / `env | cut -d= -f1 | wc -l`。全部真实 opencode adapter 产生权限请求，经 Authority/Gate 进入 OneShot 审批，协议 fixture approve 后真实命令执行、完成 ask；单例 6.867–13.933 秒，无 gate 误报或实例异常，最后 agent exit0。

隔离重现将原样本的 `$TMPDIR/handoff-fc` 替为本实验固定绝对路径下同名真实二进制，避免写进宿主 TMPDIR；原始 `$TMPDIR` 命令文本另外经 Authority 表测。两种证据分别声明，不把替换后的路径称为原历史载荷。没有运行任何真实任务变更命令，dispatch/reply/upgrade 均只执行 `--help`。

原始历史来源与完整回放方法见 `b463-samples-ledger.md`。全部样本都保持 Consult/模型审批路径，不能据 fixture approve 声称已放宽静态允许或真实模型一定批准。
