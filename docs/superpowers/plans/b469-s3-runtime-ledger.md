# B469 S3 协调者隔离运行

## 偏离与自主决定

- 全部控制由本 Codex 会话完成，未交房间协调者或远程 executor。实验中房间驻场 CLI 只作为被测产品执行路径；采用真实 opencode 1.18.32，模型端是 loopback 协议 fixture，无生产模型凭据。
- 单机隔离 agentd/HOME/XDG/工作区和本批测试 PG。初次准备脚本时替换实验机器名误改临时根路径，在启动任何服务前失败；修正路径后完整重跑。
- 本台账不以单机或双 PG 连接代替计划规定的两台执行机器实测。

## 原 permission 票的实际闭环

`/tmp/b469-p0-runtime/s3-live/report.json`：

- 实际驻场创建 `session:8`，5.443 秒返回。驻场 bash 调用隔离任务 API，消费真实进程环境 `HANDOFF_ORIGIN_SESSION`；任务持久化 origin=`session:8`。
- 原任务 `4a35a575-f987-424b-8b31-ffa0a7078c3f` 由实际 opencode adapter 发 permission，形成 `gate`；通知从 pending v1 经目标驻场 print 短回合变为 delivered v1。模型 fixture 不自行回答原工单。
- 发起回合与通知回合结束后，协调载体及小队准入均 0；原任务载体计数 1，原 gate 仍 waiting_answer。通知到达不等于批准，不需要占用协调者回合等人。
- 隔离 agentd 重启后 origin、原 gate 和 delivered v1 保留；经原 reply API 返回 `ok=true,relayed=true`，原任务转 running、待答 0。随后支持的 stop API 清理任务，agent exit0。
- provider 请求记录观察到首次协调工具回合之后，只有一次额外协调续轮；原工单未被转成新的协调者权限票。

该结果证明这个版本的单机原 permission 工单、驻场回退、短回合释放与重启后的原票回答链。完成后的源码修订仍需按触及范围复验；不证明真实模型会作出何种审批，不证明跨机门铃或桌面 OS 行为。

## 原生 question 工具补证

`/tmp/b469-p0-runtime/s3-question-v2/report.json`：真实 task 的 `question` 工具产生原生 `que_...` 工单（明确一问一选项，不是仅用最终文本模拟 ask）。实际任务 `f2acd871-8d00-4817-844c-e52b0a40a34c` 原 ask 从待答进入 delivered v1，重启后仍为同票；回答选项原文 `Continue` 返回 `ok=true,relayed=true`、待答清零并继续运行，agent exit0。驻场创建 4.537 秒。

第一次补跑复用了旧隔离机器名但随机改了端口/令牌，触发已有自登记防撞名守卫，编制域返回503；没有绕过守卫。改用实验唯一机器名后重跑，通过上述原生 question 链。

## 其他检查

- Node 24.19.0 下 web `tsc -b` 通过；TicketsPanel、useTaskSession、TicketsOverlay、CardDrawer、API client 共 5 个测试文件 92/92。
- 根协调者完整触及 10 个 Go 包首次全绿（`s3-touched-go.log`），使用隔离 HOME。宿主 HOME 下 `TestHostWakeResolvesLoginProxyOnHostMachine` 会读取现有 `~/.handoff/env`，实际 NO_PROXY 多出私网网段；在不含 S3 的 S2 快照也同样失败（`s3-proxy-baseline.log`）。没有改宿主配置或测试期望，干净 HOME 下通过。后续源码去重/换责修订需新鲜定向或整包复验，不能复用旧绿作最终快照证据。PG 行为另见 `b469-s3-pg-ledger.md`。

## 尚未验收

- 两机同测试 PG 下断连、补发、换驻场、只有目标机按铃；实际桌面信箱与深链操作。
- 真实模型行为、其他操作系统/shell 方言。
- 卡 push/pull/祖先和通知重试已有生产组件测试，仍需实际 CLI/房间联合实跑；不得以本节单个驻场场景代替全部矩阵。

## 最终源码快照与双物理机基础链

最终源码在 `/tmp/b469-p0-runtime/s3-final-snapshot` 冻结；manifest 覆盖 2067 个 Go/TS/JSON/模块文件，协调者核对工作树无源码差异。该快照的 10 个触及 Go 包整包测试全绿，PG 双连接/显式重试测试全绿，触及包 vet、Mac 与静态 Linux amd64 构建、web 类型检查和 5 文件 92/92 均通过。全仓 Go（含未触及包）测试未运行，未将整仓空测编译等同全量行为测试。

用户指定第二物理机 linux-01 后，协调者经现有 SSH 连接在其临时目录启动独立 agentd，未覆盖已安装二进制或在役配置。Mac 与 Linux 各自独立 HOME/XDG/工作区，SSH 双向端口转发连接临时 peer 和 loopback provider，同一个专用测试 PG；测试 Handoff token 均临时生成。Mac 实际 opencode 1.18.32，Linux 1.19.3。

- `dual-86367ad6e9`：原生 permission/gate，真实 Mac 驻场进程的 origin_session 传到 Linux 任务；通知 delivered v1，Mac 协调者计数0、Linux任务计数1；显式 deny 经原票回复 `ok/relayed=true`、原票待答清零。驻场创建7.876秒。
- `dual-b4f4a7f00c`：原生 question/ask，同样 delivered v1、Mac协调者0/Linux任务1，Continue 经原票回传并清零待答。
- 两轮均支持的 task stop、远端 PID 身份核验停止、Mac wait/reap 和 SSH tunnel 关闭成功，未报告清理失败。

脱敏结果已入库：[`dualhost-permission.json`](../evidence/b469/dualhost-permission.json)、[`dualhost-question.json`](../evidence/b469/dualhost-question.json)，包含二进制与源码 manifest 摘要。原始完整报告留 `/tmp/b469-p0-runtime/<run-id>/report.json`。fixture 决定模型输出，只能证明真实双机 CLI/工单/投递/原生答复链；不能据此声称真实模型会批准某项操作。

前节“双机尚未验”在此更新为：基础 permission/question 已验，离线补投、跨机换驻场、实际桌面门铃及卡路由的 CLI 联合矩阵仍待补齐。

## 双物理机离线补投与跨机换驻场

协调者在同一冻结二进制上亲自运行 `python3 /tmp/b469-p0-runtime/dual_host_recovery_probe.py`，exit0。脱敏事实见 [`dualhost-recovery.json`](../evidence/b469/dualhost-recovery.json)，原始报告 `/tmp/b469-p0-runtime/dual-6690594252/report.json`。

- 先重跑实际 Mac 驻场→Linux 原生 permission/gate，原票 deny 回传成功，再停止该任务释放 Linux 载体。
- 停止 Mac 隔离 agentd 后，在 Linux 创建原生 question。原 ask 仍 waiting_answer；只读 PG 查询实际通知实体为 pending v1，目标是原 Mac 驻场身份，没有在 Linux 错标 delivered。重启同 HOME/config 的 Mac 后，同票 delivered v1；继续观察3秒，只有一次 coordinator 主请求，Mac 协调槽位0，原 ask 的 Continue 回传 `ok/relayed=true`。
- 再停止 Mac。另一张 Linux question 的通知持久化为旧驻场 pending v1；经既有 `session stand <session> --as <old identity>`、B 本机 `resident/launch` 换到 Linux 驻场。PG 实际通知 target 改为新身份，ownership_version=2；目标机与当前 SessionResident 一致，Mac 仍关闭。继续观察3秒后保持 delivered v2，原票 Continue 仍由 Linux 原答复入口回传成功。
- 两次 Mac stop 均 exit0；三张自有任务 stop 后载体计数0；远端 PID 核验停止成功，SSH 隧道停止，无清理失败。最终清理时 A 已按实验步骤停止，最终 `local_process_exit` 不再重复记录。

本轮仅通过只读 SELECT 核对本实验 machine/task/ticket 的通知实体；不直接写账本。没有制造源票回答或伪造目标。它证明真实双物理机的 print 驻场离线恢复与迁责；尚不覆盖桌面 OS 门铃或卡席位联合矩阵。此前最终快照的 `go build -mod=readonly ./...` 也已完成 exit0。

## 用户环境裁决

2026-10-23 用户对实际桌面门铃验收回复「保持全 Codex，桌面项暂留未验」。据此不启动 Claude 桌面测试会话，保留该项未验；并未把 print 或 CLI wait 视作桌面等价验收。其余双机收件矩阵继续。

## 卡路由、缺归属重试与显式 session wait 双机矩阵

协调者亲跑 `card_route_probe.py`，run=`route-78355cfa`、exit0，报告见 [`dualhost-card-routes.json`](../evidence/b469/dualhost-card-routes.json)。Mac、linux-01、临时公开 LinkTask helper 和只读 PG 取证均连专用测试 DB 中本轮新 schema `b469_route_route_78355cfa`；启动前已核 `current_schema()`。源票全部由真实 Linux opencode 原生 question 产生，模型仍是 loopback fixture。

| 路径 | 实测结果 |
| --- | --- |
| P 自身 push | 真实 coordinator/launch 建 BR1 push 席；来源票初始无归属 v1，公开 LinkTask 关联后通知转 BR1 seat v2，3秒稳定窗协调主请求只增1；原 ask 仍待答。 |
| L 独立 pull | 实际 card bind 建 BR2 pull 席，无 session 成员关联；通知先为目标 BR2 pending v2，实际 `card wait --since` 输出唯一原票 notice 后才 delivered；无新增协调主请求。 |
| C 空座子卡 | 实际创建 BR1.1、parent=BR1，空席；通知沿祖先解析至 BR1 push v2，3秒窗主请求增1，没有误投给空子卡。 |
| 无归属/重试 | target=null、needs_human/origin_unknown；空 body retry HTTP200，目标/版本不变且原票仍 waiting_answer；调用者塞 target 返回400；人工答后 retry409、通知 answered。 |
| 显式 session wait | A 隔离 agentd 停止 exit0，B 仍产生指向真实 A resident 的原 ask；Mac CLI `session wait --since` 输出精确原票通知，PG delivered；无协调主请求，原票仍待答。 |

每张原票人工 Continue 均 `ok/relayed=true`，任务停止后 B 载体计数0。远端 PID 核验停止及隧道停止成功，无 task-stop/cleanup 失败。schema 暂保留供只读核证，不声称测试数据已删除。

实验的两次环境准备失败保留原始报告：`route-5755c04b` 因 Python note 元数据名冲突；修后 `route-5bdd5856` 因默认卡号 B 被测试账本的 handoff 项目占用。两轮都未创建源任务且进程清理成功。协调者随后采用独立 schema，并通过 CLI 为测试项目设置 BR 前缀，消除旧实验多协调小队与前缀残留；未修改产品源码或旧 roster。

边界：卡任务关联由临时 helper 调用公开 `ledger.OpenShared + Store.LinkTask` 建立，真实路由、镜像、通知、CLI 收件和原票答复走生产代码；未声称跑过完整 `card dispatch`。pull 的会话身份是明确的隔离 CLI fixture 标识，不冒称模型生成会话。`--since` 只证明显式回放，默认持久游标正在独立补证。sol 已只读核本报告/脚本，目标轴与证据轴建议本轮技术阶段通过；桌面按用户裁决保留未验，真实模型判断也未验。

## 默认持久游标的独立双机补证

根协调者只补尚未覆盖的默认游标路径，没有重复整套已通过矩阵。`session_cursor_probe.py` 在新 schema `b469_route_cursor_ce88fa5d` 运行 exit0；脱敏报告 [`dualhost-session-cursor.json`](../evidence/b469/dualhost-session-cursor.json)。

A 实际驻场创建后停止 agentd（exit0），B 原生 question 形成指向该驻场的 pending v1。第一次实际 `session wait <sid> --timeout 30s` **不带 --since**，输出1张匹配通知并使 PG delivered，原票仍 waiting_answer。另一个新 CLI 进程在相同 HOME/config 再次不带 --since 等待3秒，退出124、输出0张重复通知；原票仍待答。只读 PG 核精确 `resident:session:1/agent:b469cursor-ce88fa5dsquad` 游标，last_seq 为2→2，未丢失或回退。随后原票 Continue `ok/relayed=true`，B任务槽0，远端PID核验停止和SSH隧道停止成功，无清理失败；schema保留供核证。

该补证核销“只有 --since 回放、未验默认持久游标”的运行缺口。它仍不替代完整 card dispatch、真实模型判断或用户选择暂留未验的桌面 OS 行为。
