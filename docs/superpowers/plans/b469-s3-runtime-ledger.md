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
