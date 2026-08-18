# B134 + B137 执行 ledger

职责：记录每个 task 的完成裁决、修复轮次、提交范围与待复核项。

边界：只记录本次执行分支的证据，不替代测试输出、git log 或审核者的真机复验。

- [开始] 基线：c34ecc04；工作区已确认干净；Task 1 开始。
- [Task 1 / 双裁决通过] spec 符合：覆盖重定向形态、引号状态、~ 展开、fd 复制/关闭排除、/dev 丢弃识别；质量：纯函数、中文 why 注释、无新增依赖；测试：沙箱内指定环境变量运行通过；commit range：c34ecc04..HEAD（本 task 提交）。
- [Task 2 / 双裁决通过] spec 符合：bash 先判 req.Paths 与自摘重定向落点，越界复用既有文案并升级，干净落回 judgeCommand；质量：三条 Debug 均带 path，归一化失败带 cause，越界日志去重说明完整；测试：internal/permgate 全包通过；commit range：4732a591..HEAD（本 task 提交）。
