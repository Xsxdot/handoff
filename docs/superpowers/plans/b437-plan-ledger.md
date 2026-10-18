== 2026-10-18T15:37:03+00:00 worktree=/root/.handoff/worktrees/53a52f47 HEAD=82d997c3 branch=cards/B437-charter
codegraph 二进制: /root/.local/bin/codegraph（已装）；基线图仓内缺失，从 /root/.handoff/repos/handoff/codegraph（meta: commit 18061f2c, scannedAt 2026-09-14, mtime 10-17 14:51）拷贝入本工作树后 --repo . 可用；validate --stale 报 1546 条行漂移，行号一律以当前文件为准

## 图查询记录

- `codegraph --repo . sym m_orchestration_Approver` → 命中：internal/orchestration/approver.go:78（再锚定），签名与字段（names/model/models/timeout/env/shots）与当前文件一致。
- 图 snapshot 陈旧（meta commit 18061f2c、scannedAt 2026-09-14、文件 mtime 10-17 14:51，落后于 B439 合入 31d0fe51 10-18 23:34）；`validate --stale` 报 1546 条行漂移。处置：结构查图、行号一律以当前工作树文件复核；下述「现状读数复核」全部亲读当前文件。

## spec 现状读数逐条复核（当前 HEAD 82d997c3）

| spec 读数 | 复核结果 |
|---|---|
| config.go:212-229 approver 段（executor 候选有序表、链级 model、候选级 models、单候选 timeout、blacklist） | ✓ 结构体 ApproverConfig 在 config.go:223-229（注释自 212 起）；候选级 models=B405（226）；默认 timeout 60s（newDefaultConfig:524） |
| cmd/agentd.go:207、:641-676 启动绑 OneShot | ✓ :206-210 调 bindApproverOneShot；函数体 :641-676 逐字吻合（未注册/未实现 OneShot → WARN 跳过，全绑不上 → 启动失败） |
| internal/executor/oneshot_cap.go:40-47 无「机器」概念 | ✓ OneShotReq{Prompt,Model,Workdir,HomeDir,Env,Limits} 在 :40-47 |
| 配置启动读一次、改链要重启 | ✓ cmd/agentd.go:162 `orchestration.NewApprover(cfg.Approver, …)` 启动期一次构造，Manager 持有该实例（manager.go:299-305） |
| devicesource.go / pool.go:151 按名取连接 | ✓ internal/agentd/devicesource.go（B439 WU2 DeviceTableSource，Names/Target + last-good）；pool.go:151 `For(name)`，不发网络（:150 注释） |
| permgate $TMPDIR 形态硬升级（permgate.go:240-243、cdbase.go:70-73） | ✓ cd 相对落点不可静态证明 → Escalate（permgate.go:238-243）；不可解释字符 → false（cdbase.go:70-73）。与 B437 改动面不重叠，维持 spec 备注 |
| b160 spec:58 审批链是安全裁决链不进控制台 | ✓ `Approver.*` 行（约 :58）：「它是安全裁决链……不该是一个下拉框顺手能干的事」 |
| 控制台/CLI 无 approver 编辑入口、无 handoff approver 命令 | ✓ grep cmd/ 全目录无 approver 子命令；README.md:296/465 只文档化 config.yaml approver 段 |

## 承重基线事实（亲读）

- 单一汇合缝：两条审批消费路径都收敛到 `m.approver.Decide(ctx, permission, summary)`——Manager.consultApprover（manager.go:2341-2447，非 OpenCode）与 approval.Client.consult 的 hooks.Decide（approval_client.go:34-47，OpenCode）。环泛化只动 Approver 一处。
- per-attempt 审计：consultApprover 对每个 attempt 各写一条 approver_decision（manager.go:2383/2396）；payload 双投影——orchestration/contracts.go:174 ApproverDecisionPayload 与 approval/client.go:548 approverDecisionPayload（刻意复制避免 approval 反向 import orchestration）；toApprovalAttempts 投影（approval_client.go:57-67）。machine 字段须三处加、一处投影。
- 失败计数/停用：maxApproverFails=3（manager.go:647）、countApproverFail（manager.go:2527）、approver_disabled payload（manager.go:620）——S3「连续 3 次全败停用」机制已在，S3 只补环级失败归类。
- nonce/提示词：nonce 由跑 OneShot 的那侧生成并校验（approver.go:177-189 randNonce、:356 校验、:387-394 模板）；parseDecision fail-closed（:338-371）。→ 对端端点自己生成 nonce，wire 不含 nonce；提示词模板+解析只有一份实现（orchestration）。
- grok EffortLow：decideWithCandidate 对 HarnessGrok 设 Limits.Effort=low（approver.go:287-289）——单环裁决导出面必须保留。
- registry 原语：RegistryPut CAS（ledger/registry.go:31-77，expectVersion 0=新建，冲突 ErrCASConflict）、RegistryGet（:80）/RegistryList（:95）/RegistryDelete（:116）；哨兵 ErrNotFound/ErrCASConflict（ledger.go:22-23）；文件头「域外不得直接使用本文件」→ 经 ledgerapi.Facade（api/registry.go:29-58 Put/Get/List/Delete）；cmd/room.go:39 `ledgerapi.New(st)` 先例。
- CLI 直连账本先例：cmd/ledgercli.go:26-47 openLedger（DSN→OpenShared，空→DataDir/ledger.db 回退；文件头写明有意不经本机 agentd HTTP）；ledgerActor()=cli:<user>@<host>。
- 设备表读面：ledger/devices.go ListDevices(:183)/DeviceTargets(:203)/DeviceTarget(:221)——S1 写链校验「机器名不在设备表」的取数来源。
- B439 自名判据：config.IsSelfName（selfname.go:86-91，Name 空→false）；Server.IsSelfMachine/ResolveTarget（server.go:562/579）；共享链读者只在「DSN+身份」机器存在（挂载门 cmd/agentd.go:489-496）。
- agentd 路由与鉴权：Handler（server.go:887）内 api mux 无兜底——未命中 /api 路径保持 ServeMux 原生 404/405（server.go:888-891、1024-1032 注释，SPA 不吞）→ 旧版对端 404 = 免费降级信号；鉴权 `s.auth` 包全部 /api（Bearer token，server.go:1060-1093）。
- 端点先例（B434）：POST /api/desktop/deliver（server.go:997，独立注册 + withLedger）；wire 类型 proto.DesktopDeliverReq（desktop_coordinator.go:69-83）；validate 函数（desktopwake.go:749）。本端点不依赖账本 → 不挂 withLedger。
- client 404 降级先例族：client.go:526-569 ErrStatusUnsupported/ErrFootprintUnsupported/ErrReclaimUnsupported（404=预期结论非异常）。
- 时限先例：client.go:54 dialTimeout=10s（包级 var，测试可注入）；:283 transport DialContext 带 10s 拨号上限；hc.Timeout=0（:327-329，超时全走 ctx）。machineProbeBudget=3s（agentd/machines.go:32）、addMachineProbeBudget=5s（:189）；relay 冷隧道首建装不进 3s 探活预算故有 Warm 预热（cmd/agentd.go:274-277）→ 连接时限不能取 3s，取 10s 复用拨号上限。
- orchestration 不 import internal/ledger（grep 无命中）——spec「接口注入、不反向依赖 ledger」的现状前提成立。
- 既有审批回归钉（S5）：orchestration/approver_test.go（B376 failover :896、B405 per-candidate model :1036、per-attempt 事件 :390、停用 :336/:445）、approval/client_test.go（:532/:584）。

## 图覆盖债

- 图 snapshot 落后 B405/B439 合入；本轮仅 sym 查询一次命中（Approver），其余接缝（devices、desktop/deliver、selfname、ledgercli）直接读当前文件核实。实现卡动这些缝时以本 plan §2 行号 + 符号名双锚，行漂以符号为准。

## 基线亲跑

（见下一条追记）
- `go build ./...` 退出 0（2026-10-18 15:4x，HEAD 82d997c3）。
- `go test ./internal/orchestration/ -run 'Approver|Consult|Decide' -count=1` → ok 2.842s；`go test ./internal/approval/ ./internal/config/ -count=1` → ok / ok（S5 回归钉基线绿）。
- yaml.v3 对 `timeout: 60s` → time.Duration 的原生解码亲测通过（临时模块在 $TMPDIR/yd，go test ok）——链文件/registry body 的时限字段可直接用 time.Duration。

## 计划期发现与裁决记录

- **启用门缺口（写进 §4.2 的依据）**：亲读 manager.go:2296-2313——`shouldConsultApprover` 在 `m.approver == nil` 时恒 false；`NewApprover` 对空配置返 nil（approver.go:150-152）。若不加门，本地 `approver:` 为空的机器永远走不到共享链，S1「配一次全队生效」落空；反过来若共享机一律照旧 consult，无链 DSN 机器会凭空多出 error attempt/失败计数（S5 逐字一致被破坏）。裁决：共享机恒构造 + `Available()` 门（§4.2），空配置返 nil 的既有构造语义保留（S5 钉 approver_test.go:71）。
- F2 model 语义定稿：wire 携带对端单环裁决的模型标注（B405「默认」占位），发起方原样入审计——避免发起方反标注（无法区分字面名为「默认」的模型与占位）。
- 计划收尾修正：clear/ErrNotFound 必须清 last-good（否则 clear 后机器永远跑已删的链）——已回写 §4.2/§6-4/§8。
- 任务上下文所列合并目标分支 claude/agitated-bun-cd484f 在本仓无本地引用（`git rev-parse --verify` 失败）；工作分支 cards/B437-charter（HEAD 82d997c3）为唯一在案事实，计划头部已如实标注，未阻塞（合并目标是协调者事务）。
- 基线图目录 codegraph/（未跟踪）系临时拷贝自主仓，供 --repo . 查图；提交前删除，来源与 meta 已记于本台账首条，后续节点可按同法重建。
