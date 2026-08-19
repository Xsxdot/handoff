# B160 控制台常规分区与机器级缺省执行者执行记账

职责：记录本次实现各 task 的双裁决、修复轮次、提交范围与亲自执行的验证结果。
边界：只记录本分支证据；真机验收与 B158 合并后的复验留给计划附录中的审核者。

- 基线：`d45e102d`（`docs(plan): B160 控制台常规分区与机器级缺省执行者（8 task）`）；当前分支 `claude/b160-general-settings`，开工前工作树干净。
- Task 1 / 修复轮 0：先追加活配置与 status 回归测试；按计划执行目标测试，原始失败为 `TestManagerReadsLiveExecutorDefault` 改之后仍为 `name = "fake"`，`TestStatusReportsLiveExecutorDefault` 为 `default_executor = "fake"`。
- Task 1 / 完成裁决：spec 符合性通过（Manager 保留 `NewManager` 签名、SetManager 注入 `conf`、全部 Executor 读点走活快照、status 两处同步、Targets 陈旧读只记 backlog）；代码质量通过（保留构造快照兜底、注释说明快照边界、SetManager 只在非 nil 时注入并记录关键日志）。验证：`go test ./internal/agentd/ -run 'TestManagerReadsLiveExecutorDefault|TestStatusReportsLiveExecutorDefault' -count=1`、`go test ./internal/agentd/ -count=1` 均实际通过；`grep -n "m\\.cfg\\.Executor\\." internal/agentd/*.go | grep -v _test.go` 无输出。Commit 范围：`HEAD^..HEAD`（本 task 提交）。
- Task 2 / 修复轮 0：新增 `ExecutorDefaultReq/Resp`、Go fixture 样本与 TS 契约；目标 fixture 测试按计划原始失败，两份文件均报告 `读取 fixture ... 失败（契约尚未生成？请运行 -update）`，随后 `-update` 生成并逐字确认 `ExecutorDefaultReq.json` 保留 `"model": ""`。
- Task 2 / 环境失败记录：一次校验命令误在 `web/` 工作目录执行 Go 测试，原始错误为 `stat .../web/internal/proto: directory not found`；前端首次因 npm 缓存权限原始错误为 `npm error code EPERM ... /Users/sycm/.npm/_cacache/tmp/***`，之后在工作树内恢复依赖（`npm ci` 实际安装 287 packages）。
- Task 2 / 完成裁决：spec 符合性通过（线格式字段、整体替换与空模型语义、fixture、TS 镜像及两条契约断言齐全）；代码质量通过（新文件头与导出类型注释完整，无运行时日志）。验证：`go test ./internal/proto/ -count=1`、`cd web && npx vitest run src/api/contract.test.ts && npx tsc -b` 实际通过（25 tests）。Commit 范围：`HEAD^..HEAD`（本 task 提交）。
- Task 3 / 修复轮 0：先追加六条端点用例；路由尚未注册时原始失败为 `json: cannot unmarshal number into Go value of type proto.ExecutorDefaultResp`（GET/PUT 均为 404 原文无法按 JSON 响应解码）。
- Task 3 / 完成裁决：spec 符合性通过（GET/PUT 支持 `?machine=` 转发、manager 未就绪返回 503、default 校验并列可选名单、model 仅 TrimSpace 不校验、整体替换走 swapConf、响应复用最新活快照）；代码质量通过（新文件头/handler why 注释完整，日志只记 `has_model` 不记模型值，ExecutorConfig 未添加无意义深拷贝，路由表同步）。验证：`go test ./internal/agentd/ -run TestExecutorDefault -count=1`、`go test ./internal/agentd/ -count=1` 实际通过。Commit 范围：`HEAD^..HEAD`（本 task 提交）。
- Task 4 / 完成裁决：spec 符合性通过（新增 `fetchExecutorDefault` / `saveExecutorDefault`，均按 machine query 路由，保存请求保留空 model 语义并返回后端原文错误）；代码质量通过（复用既有 request/putJSON，无日志、注释说明整体替换）。验证：`cd web && npx tsc -b && npx eslint src/api/client.ts` 实际通过。Commit 范围：`HEAD^..HEAD`（本 task 提交）。
- Task 5 / 修复轮 0：新增 hook 与 3 条测试后，hook 定向测试 `3 passed`；整组 tree 测试首次原始失败 `10 files / 105 tests` 中 3 条 `ProjectTree` 用例，症状为找不到「已隐藏 1 个目录」、`main`，以及搜索前隐藏项目仍在树中。按计划给测试专用 `__resetTreePrefsForTest()` 并接入 `ProjectTree.test.tsx` 的 beforeEach。
- Task 5 / 完成裁决：spec 符合性通过（模块级单例 + Set 订阅、落盘/通知成对、ProjectTree 改用共享 hook、测试隔离 reset，未引入跨标签同步）；代码质量通过（文件头/why 注释完整，退订按引用删除，测试专用入口明确标注）。验证：`cd web && npx vitest run src/app/tree/ && npx tsc -b && npx eslint src/app/tree/` 实际通过（10 files / 105 tests，eslint 0 errors / 3 个既有 fast-refresh warnings）。Commit 范围：`HEAD^..HEAD`（本 task 提交）。
- Task 6 / 完成裁决：spec 符合性通过（GeneralPage 只展示三项客户端偏好、范围文案明确、项目树未加载时保留其他控件、设置页传入已有 tree 并替换占位）；代码质量通过（排序标签与左栏同源、隐藏名单取反方向有 why 注释、无新增日志或服务端设置）。验证：`cd web && npx vitest run src/app/settings/` 实际通过（3 files / 15 tests）、`npx tsc -b`、`npx eslint src/app/settings/` 实际通过。Commit 范围：`HEAD^..HEAD`（本 task 提交）。
