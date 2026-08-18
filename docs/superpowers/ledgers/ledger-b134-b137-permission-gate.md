# B134 + B137 执行 ledger

职责：记录每个 task 的完成裁决、修复轮次、提交范围与待复核项。

边界：只记录本次执行分支的证据，不替代测试输出、git log 或审核者的真机复验。

- [开始] 基线：c34ecc04；工作区已确认干净；Task 1 开始。
- [Task 1 / 双裁决通过] spec 符合：覆盖重定向形态、引号状态、~ 展开、fd 复制/关闭排除、/dev 丢弃识别；质量：纯函数、中文 why 注释、无新增依赖；测试：沙箱内指定环境变量运行通过；commit range：c34ecc04..HEAD（本 task 提交）。
- [Task 2 / 双裁决通过] spec 符合：bash 先判 req.Paths 与自摘重定向落点，越界复用既有文案并升级，干净落回 judgeCommand；质量：三条 Debug 均带 path，归一化失败带 cause，越界日志去重说明完整；测试：internal/permgate 全包通过；commit range：4732a591..HEAD（本 task 提交）。
- [Task 3 / 双裁决通过] spec 符合：四条绝对路径/家目录重定向模式为 ask，未加入 `*>*`；质量：why 注释与逐条生成配置断言同步，保留 external_directory=ask；测试：internal/executor/opencode 全包通过；commit range：db4f1b94..HEAD（本 task 提交）。
- [Task 4 / 双裁决通过] spec 符合：Adapter 五实现、manager 四调用点、测试 stub 全部同步 reason；fake 记录 Reason 且未实现可选能力位；质量：协议无消息字段/老端点丢字段 why 完整，go.mod/go.sum 未改；验证：go build/go vet 通过，fake、claudecode、codex、grok、opencode 与沙箱外 TMPDIR=/tmp 的 agentd 全包通过。待复核：工作区 TMPDIR 下全量 Go 测试另有计划预列的 root TestInstallScriptUnits 与 cmd TestProjectAddRejectsNonRepo 假红，未修改其文件；commit range：359b0263..HEAD（本 task 提交）。
- [Task 5 / 双裁决通过] spec 符合：turn.DenyGuidanceText 为唯一渲染点，claude 拒绝理由与裁决同帧、空理由回退通用句并自报能力位，manager 带外出口共用正文；质量：socket 日志不抄理由原文，B128 seq32→seq33 与 B50/B91 注释保留；验证：turn+claudecode 全包通过，agentd `TMPDIR=/tmp go test -p=1` 全包通过；并行 agentd 首次 WS 单测超时后单测与串行复跑通过；commit range：bac53f9b..HEAD（本 task 提交）。
