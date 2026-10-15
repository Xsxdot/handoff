# 拖拽覆盖终端的生命周期修复

基线：c34cad96，codex/terminal-group-close，初始工作树干净。用户明确授权修复，指定 gpt-6-luna 子 agent 实现。该 bug 沿既有关闭职责修复，不新增外部契约或自动回收策略。

## 已核事实与目标

WorkbenchPage.placeFromDrop/moveGroup 直接 api.place → useWorkbench.place → tabs.placeSource。center 和满列上下分屏回落会直接覆盖旧 tab；TerminalTab 卸载只断连接，不 DELETE。Shell 现有终端/脏文件关闭确认未接入此路径。图查到 placeFromDrop、TerminalTab，flow degraded（无 flows）；setContent 未命中，记为图覆盖债，行为以源码为准。

目标：从左栏拖内容或移动已打开窗格到占用格时，若实际替换终端，确认一次；取消不改任何布局或会话；确认成功结束旧会话再完成原拖拽。DELETE 失败保留原布局、错误可行动。脏文件不能被静默丢弃。边缘分屏、空格、自身拖回自己、会话去重仅聚焦/移动不得误杀。满列 top/bottom 回落 center 也必须同一守卫。

## 边界与责任

实现者拥有 web/src/app/shell/Shell.tsx、web/src/app/workbench/WorkbenchPage.tsx 及必要 Shell/Workbench/useWorkbench/tabs 测试；必要最小内部接缝可在同目录类型/hook 内调整，但布局函数保持纯粹，不发 HTTP，不因普通卸载结束会话。其他文件扩面需说明理由。协调者拥有本文、ledger、图视图与roadmap。多人共享工作树，保留他人改动。

复用 Shell 已有确认/机器感知 DELETE 职责，统一承接实际替换，不在各个 MIME 分支复制关闭逻辑，不引入万能资源管理器或独立第二套生命周期。可逆内部实现由执行者判断。新外部接口/持久化状态/后台回收/更改移动语义须升级，不在本修复实施。无需新原型：复用已有确认框，无新页面或布局设计。

## 顺序与验收

1. 先从真实 Shell 拖放事件写断言红：已有终端占用目标，drop 后应显示确认、尚无 DELETE 且旧布局未被覆盖；记录当前失败原文，再写生产代码。
2. 实现实际替换的统一守卫与确认后续动作；先 DELETE 成功再替换，失败不移走源。按 tab 自己的 machine 路由。
3. 回归：取消、失败重试、远端、满列回落、自身放回自己、边缘新增分屏、空格、脏文件；确认期间源或目标改变/移动/被替换/消失不得误杀其他资源或覆盖新内容。等待 DELETE 时重复确认、Escape、新替换/关闭请求不能覆盖 in-flight 目标。若请求已发期间布局变化无法撤回，保留已变化布局且明确告知，不能把旧 continuation 应用到新内容。
4. 局部迭代 Shell/Workbench/useWorkbench/tabs 相关测试，全Web typecheck。协调者独立审查、承重变异、最终全Web测试/构建。未亲跑的真实拖放客户端行为明确写未验证，不把 mocked DELETE 当真实后台会话结束。

- [x] 日志：替换入口/确认/取消/DELETE前后/失效/失败/完成携带 source/target tabId、sessionId、machine 和原因；沿现有 console.debug/info/warn/error，不使用 console.log。
- [x] 注释：解释替换为何复用显式关闭职责、为何不在卸载或纯布局内杀会话、为何确认后必须重核身份；不复制实现正文。

图覆盖债：setContent 未命中；本轮若未修改不新增假节点。placeFromDrop/TerminalTab 原锚 moved；触及函数由协调者更新图增量。未跑客户端场景落roadmap。

## 本轮交付记录

gpt-6-luna 实现完成；独立审查 R1/R2 核销，来源身份测试缺口经补测和同一可编译变异核销。协调者最终原版全Web 144文件/1877项通过，build/lint通过；真实PTY host的detach保活与kill清理测试通过。源码与自动化阶段通过，未部署或操作客户端，尚不称真实UI到后台资源结束的整链验收；部署/设备点验与全仓图历史问题已落roadmap。
