# 顶部标签关闭独立审查记录

阶段：bug 源码修复审查；基线 b6c4d684；计划 docs/superpowers/plans/2026-10-12-terminal-group-close.md。

## Important R1：取消等待中的 DELETE 后旧回调覆盖新关闭请求

独立审查发现：ConfirmDialog 按钮在 busy 时 disabled，但 Escape 与遮罩仍触发 onCancel。组关闭 DELETE 等待期间 Escape 会清掉 groupCloseRunRef；用户接着点另一个组 × 会创建新队列/新确认。旧 confirmClosePty 的成功回调仍执行 setClosingPty(null)，可清掉新弹窗，而新 run 的 waitingTabId 仍非空，造成后续关闭卡住。

证据（修复前工作树行号）：ConfirmDialog.tsx:45-47/60-64；Shell.tsx:598-641/1777-1783。需要从真实 Shell 入口用 deferred DELETE 复现，并保证 pending 时取消/后续请求不能替换 in-flight 目标，或旧请求只推进其自己的 run。

处置：交实现 agent 核验和修复；尚未核销。

## 复审与核销

R1 已核销：PTY 结束请求用同步 ref 标记；pending 时取消处理为 no-op，组关闭入口在任何已有关闭确认或请求时不启动。独立 reviewer 亲跑顶部组关闭 10 例：10 passed、131 skipped，exit 0；真实测试包含 Escape、点击另一组 ×、旧请求返回、再确认第二组并验证 DELETE pty-second。日志的旧布局快照剩余计数亦已移除。

目标轴：单/多终端组完成关闭、取消、首项 DELETE 失败、远程 machine、混合脏文件、确认期间新增终端保留、并发互斥均有真实 Shell 消费入口自动化证据，无 scope drift；不新增页面形态。架构与证据轴：TabBar → WorkbenchPage 注入回调 → Shell 现有 guard/DELETE；布局仍纯粹，TerminalTab 卸载保活未改，无新增 wire 字段或外部冻结对象。无未解决 Critical/Important finding。

证据边界：本轮审查不代表已部署或真机验收；中间项失败、第一项成功后取消、移动/替换窗格尚无专用运行证据，仅做源码核验。最终自动化复跑与变异由协调者留于相邻 ledger。

## R2 复审（一次组级确认）

本节对应 plan R2 和用户实际反馈，取代旧版逐项确认的目标轴结论。

- Important R2.1：confirmCloseGroup 以全局 tabId 查找快照资源，未核对其仍属于 snapshot.groupId。确认前移入其他组的终端仍会 DELETE，脏文件仍可能被 closeById 丢弃。必须在确认前核对组成员、请求返回后保留已移出的布局，并从真实生产布局移动入口补红绿断言。源码核验为 Shell.tsx:577-623（修复前行号），尚不称设备实测。
- Important R2.2：请求期间新增窗格虽因不在快照而保留，但 skippedCount 只算快照项变化，完成提示为空。R2 承诺的「新增或修改内容保留且告知」需要以新增项数量产生提示，而不扩大本次 DELETE 集合。真实 Shell 新增终端用例已有保留断言，应补提示断言。
- 三个既有单项关闭测试的文案断言误替换为组级文案：父 agent Shell/Workbench 复跑 177 pass、3 fail（Shell.test.tsx:1215 等）；单项生产流程仍用原弹层，应恢复原测试断言，不改单项用户结果。
- 新组级文案原含「本次快照」，执行者已改为普通用户用语；以最终复跑版本核销。

上述条目已交实现 agent 核验；R2 尚未核销。

## R2 最终核销

R2.1 已核销：确认前与请求返回后均核对资源身份及原组成员，确认期间移动的资源不删除；真实 Shell 拖拽移动入口测试已补。R2.2 已核销：新增/变化/失败的剩余窗口保留并告知；通知改为「其余 N 个窗口已保留」，避免重试时误称全部为新建。三个单项测试已恢复原文案。

独立 reviewer 最终未发现未解决 Critical/Important；亲跑 R2 组用例12项与原单项/路由9项通过。执行者随后新增一项并复跑 Shell/Workbench 183项通过；协调者最终亲跑全Web 144文件/1868项通过，并以仅结束首个终端的变异验证「一次确认结束两个」断言红，恢复后13个组用例全绿。原逐项队列已删除，无新增协议或后端生命周期规则。源码两轴通过。

部署与行为证据：协调者更新本机嵌入Web服务，浏览器同服务真实创建两个终端，取消保持会话清单，单次确认后两个会话与测试组全部消失，原有Linux会话保留。原生桌面加载新版和单项远端关闭已验证；原生顶部×及iPhone列表尚无设备点验证据，不能由浏览器结果代替。全仓图validate仍被既有diffs/cards子目录阻塞，本修复独立视图validate/check通过。
