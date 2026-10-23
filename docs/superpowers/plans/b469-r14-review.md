# B469 / B463 r1.4 修复阶段复审

## 偏离与自主决定

- 本文由协调者转录两名独立 Sol 审查者的只读裁决与 root 系统实跑；不冒称协调者是自己的独立审查者。基准为 spec/plan r1.4、S3 contract v1.1，冻结源码 `381911e18cb3ec04e384129a229baa645a0252fa`。
- 用户批准撤回不安全 env-cut 取名例外，使用真正只取名的 Bash `compgen -e`；原行式投影继续人工门禁。通知重试按用户走查移到状态右侧。没有扩大静态 Allow。
- S3 实施者自行做过一次 SQL overlay，角色偏离已进实施台账首节，不计独立验收；Sol 后来独立从冻结源码复验红/撤绿。
- 本期规则确认按用户 C：现行 cookie/主令牌显式管理动作即可，不要求独立可信人类在场。S1/S4 产品实现未在本修复阶段交付，不能借本阶段通过宣布四卡完成。

## 两轴裁决与逐项核销

| 维度/审查项 | 裁决与证据 |
|---|---|
| 偏离与权限 | 已核。I5 创建序、一次迁移 marker、registry CAS、活责任缓存、transport normal-close 精确查验和只读 preflight 均在核定范围；不新增共享 PG 表，不改 2→3 升版承诺。 |
| S2 I1 | 通过。删除 env-cut 例外；合成多行值回归与 compgen Consult 守卫成立。模型前两轮为 18/23、22/23 红，最终冻结源码真实模型 23/23；13 条历史 help、compgen 批准，9 条危险反例升级。见 [最终真实模型报告](../evidence/b469/r14-model-final.json)。 |
| S2 I2 | 通过。权限类别固定映射/unknown，原权限、env/cause/output/nonce 不进候选日志；默认 Warn 可见开始/结果/失败/跳过。独立 overlay 重新引入任意前缀或降环跳过为 Info，对应日志测试编译后红、撤后绿。真实默认 Warn 见 [日志](../evidence/b469/r14-model-default-warn.log)。 |
| S2 语法 Minor | 通过触及包与真实 Gate 回归。命令替换、进程替换、if/function 中真 handoff 变更会拦；grep 参数不再冒充命令；动态无法证明形态仍 Consult。未缩黑名单/包装器边界。 |
| S3 I3 | 通过。候选 SQL、wire target、当前归属各有生产接缝反例；独立变异关闭 wire 身份检查会错收 other 并使 CLI 测试红，撤变异绿。 |
| S3 I4 | 通过。真实 source store 的 gate/ask 在通知 delivered 后仍 pending；独立注入 AnswerTicket(allow) 令两子测红，撤后绿。 |
| S3 I5 | 通过。CreateTask 与创建序同事务、旧任务一次回填；精确 discovery wire/分页；启动0重建、稳态增量、不保留无票终态历史；waiting_review/断链/1000 clean close 不作终态证明。相同事实在 mutate 外只读返回，真实 PG 第二连接持全局锁时仍返回且保持更新时间/退避；关闭预检的独立 overlay 编译后锁等待断言红，撤后绿。见 [PG三测](../evidence/b469/r14-pg-baseline.json)、[变异](../evidence/b469/r14-pg-mutation.json)、[恢复](../evidence/b469/r14-pg-restored.json)。 |
| S3 I6 | 通过。bad JSON 在 SQLite 候选层安全进入消费者，默认 Warn 只含 kind/seq/source、不含正文，游标推进且 good notice 继续投递；独立 ELSE1→ELSE0 overlay 编译后增强测试红、撤后绿。未知 kind 同口径；PG JSONB 保证 JSON 合法。 |
| S3 I7 | 通过。POST任务→store→GET detail 的 origin_session 穿透及非法输入400；真实双机原生发起链同时验证 origin 正确。 |
| S3 Minor | 退避持久化/同址不擦退避、task-scoped活动票/void、原事件超过10000分页、retry读错503、版本冲突与普通故障日志区分已核。旧证据补 commit/入库manifest；架构债已记 roadmap。 |
| 架构/契约 | Sol 独立技术两轴通过。approval 不读会话内部，ledger 不解释授权，原工单仍是唯一回答载体；B435/S3 v1.1坏通知跳过语义一致。 |
| 触及测试 | 实施方触及 Go 包及全量编译/构建通过；Sol 新鲜复跑承重定向和独立变异；root PG 三测0skip。前端相关3文件21条与 tsc 通过。具体命令/环境假红归因见各实施与 runtime 台账。 |
| 原型 | 用户确认后指定重试置右；fork与真实源码同布局，base README为“确认中”。原生桌面门铃按用户决定未验，真实网页视觉对照未验；未以源码/单测宣称视觉通过。 |
| Scope | 本次 S2/S3 修复阶段通过；S1/B458、S4/B470仍未交付。本文件不是父卡最终裁决、合并或机队升级授权执行记录。 |

## 冻结源码的双机实跑

root 使用 `git archive 381911e18...` 构建原生与 Linux/amd64 二进制，远端 SHA256 与上传物一致，三组均使用新建专属 PG schema、独立 HOME/端口/工作区；真实 Handoff/OpenCode 运输与生命周期使用 loopback 模型夹具，**不替代上面的真实审批模型判词**。

- [卡路由/无源/离线收件](../evidence/b469/r14-dual-routes.json)：自身push、card wait pull、空座push祖先、无归属needs_human/重试、A离线session wait；通知交付不回答原票。
- [持久游标](../evidence/b469/r14-dual-cursor.json)：第二个同HOME/config的CLI进程不重复收件、不降低游标。
- [权限与恢复](../evidence/b469/r14-dual-recovery.json)：原生permission/ask、准入释放、源回复、A离线后重启补投、换驻场提高归属版本，仍答原票。

三组均成功，任务按支持入口收尾；本机agentd退出记录在phase、远端按PID/argv身份核验停止，SSH隧道关闭。独有schema保留供证据复核；不触碰在役实例。完整生产源hash见[入库manifest](../evidence/b469/r14-source-manifest.json)，旧8cd源证据保留但不抵本次新代码验收。
