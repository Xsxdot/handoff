# W5b-1 桌面薄壳核心 — 执行 ledger

任务：2a2f7169-c5ac-4ed5-87c0-1257adc7648f
分支：feat/w5b1-desktop-shell
基线：a03f5605（merge-base main）
计划：docs/superpowers/plans/2026-08-16-w5b1-desktop-shell-core.md
范围：执行者只做 Task 1-6；Task 7（真机走查）由审核者本地完成

## 进度

- 2026-08-16 Task 1（薄壳骨架与模块隔离）完成，commit cfa527c3。审查双 APPROVED。Minor 记账 5 条：M1 根 Taskfile.yml 删了 windows/ios/android include 但三个 build 模板目录原样入库成孤儿（`go build ./...` 在 desktop 内会因 build/ios/main_ios.go 报 main 未声明，但 spec 只要求 `wails3 task build` 且已通过，非阻断，留终审 triage）；M2 frontend/index.html 死 Greet 控件与永不触发的 time 事件残留（模板残留）；M3 desktop/README.md 是 wails 模板原文结构描述不符；M4 build/config.yml 保留 "My Company" 占位；M5 main.go `//go:embed all:frontend/dist` 使裸 `go build ./...` 在新克隆无 dist 时报 embed 无匹配文件（构建唯一路径本就是 `wails3 task build`）。
- 2026-08-16 Task 2（定位 agentd 配置）完成，commit 9eeafa95。审查双 APPROVED。实现者发现一处比 task 描述更糟的事实：`config.Load` 文件不存在时不仅返回默认配置 err==nil，还会 randToken 生成真令牌写盘；本实现 os.Stat 守卫正好避开。Minor 记账 4 条：M6 非 NotExist 的 Stat 错误分支返回的 state 无意义（语义已文档化）；M7 missing-file 用例未断言 Endpoint 零值；M8 单测运行时输出三条 Info 日志（spec 强制要求，预期）；M9 Task 1 预告的 build/ios main_ios.go 问题在 `go test ./...` 下未触发。
- 2026-08-16 协调者清理：Task 2 实现过程中遗留了裸 `go build` 产物 `desktop/desktop`（15MB 二进制），已删除；desktop/.gitignore 未覆盖该文件名，记 M10 留终审 triage。
- 2026-08-16 Task 3（鉴权握手）完成，commit 3d4939a4。审查双 APPROVED。变异验证：去掉「向 agentd %s…」包装测试仍绿（底层 client 报文已含 agentd+baseURL，addr 维度仍在），去掉 handoff-desktop 前缀正确红。Minor 记账 2 条：M11 TestConsoleURLSaysAgentdUnreachable 只断言含 agentd 未断言含 addr（底层已带，可加强）；M12 handshake.go:27 host=="" 且 err==nil 时打 cause=nil，且「服务端会补」注释未在仓库验证。
- 2026-08-16 Task 4（agentd 生命周期）完成，commit 896cd18c。审查双 APPROVED。变异验证：去掉 if st.Running 提前返回红在 DoesNothing 用例；吞平台错误红在 UnsupportedPlatform 用例；**判据 Running→Installed 三条用例全绿未被捕获**（已装未跑需 Install 是承重分支但当前无测试钉死）。Minor 记账 2 条：M13 建议补第 4 条 TestEnsureRunningInstallsWhenInstalledButStopped（Status{Installed:true,Running:false}→必须 Install，低成本高价值）；M14「已在运行」Info 未被测试断言（spec 要求实现已满足，仅未断言）。
- 2026-08-16 Task 5（目录选择可测半边）完成，commit 57b22b1d。审查双 APPROVED。变异验证：空串改静默成功红在 RejectsEmpty；报文改「无效路径」红在 RejectsFile；去掉 TrimSpace 实测 RejectsEmpty 仍 PASS（`"   "` 落入 Abs→Stat 失败分支以「路径不可用」报错）。Minor 记账 1 条：M15 TestNormalizeProjectDirRejectsEmpty 只断言 err!=nil 未断言「没有选择任何目录」，TrimSpace 删除检测不到——本 task 核心卖点就是报文区分，建议断言该文案。
- 2026-08-16 Task 6（装配）完成，commit 2da9277d。审查双 APPROVED。11/11 spec 条款 PASS；`wails3 task build` 与隔离测试与 Linux 交叉编译全过；无裸 go build 残留；go.sum tidy 噪音已还原。实现者适配：OnClick 需闭包包装 `func(*application.Context)`（spec 直传无参函数不编译，必要适配）。Minor 记账 2 条：M16 specFor 文档注释声称「BinPath 取可执行文件所在目录旁的 handoff…取不到退回 PATH」但代码只返回裸 "handoff"，注释描述未实现行为且与内联注释自相矛盾，建议删未实现描述；M17 BinPath "handoff"（相对）与 internal/service.Spec 文档「绝对路径+EvalSymlinks」有张力，计划明文的本轮策略、注释声明 W5b-2 承接，不阻塞，但真机走查若 agentd 未装会走 Install 写不出来的 plist——修复落 W5b-2。
- 2026-08-16 全部六个 task 完成（Task 7 真机走查由审核者本地做）。进入终审。

## 终审

- 2026-08-16 收尾终审（相对 3ab1b087 完整 diff）。承重项全部 PASS：模块隔离成立（`go list ./...` 无 /desktop）、CGO 不泄漏（主模块 go.mod/go.sum 零改动）、wails3 task build / 隔离测试 / Linux 交叉编译全绿、单测 13 条全 PASS、托盘恰好两项、无「停止 agentd」路径、日志全 slog、凭据（token/URL）不入日志。终裁 NEEDS-FIX：修复波 6 项 = M13（补 TestEnsureRunningInstallsWhenInstalledButStopped 承重分支）、M15（RejectsEmpty 断言「没有选择任何目录」文案）、M16（specFor 文档注释对齐实现）、M4（config.yml 填真实值防 update:build-assets 覆写回占位）、M10（.gitignore 加 desktop 防裸 build 产物）、M1（删 build/{windows,ios,android} 孤儿模板目录）。其余 Minor 留 W5b-2/弃。
- 2026-08-16 修复波 commit 3c358440。**附带改动（带出）**：generate:icons 任务 `-windowsfilename ""`——wails3 该 flag 有默认值 build/windows/icon.ico，删 windows 目录后不置空 wails3 task build 会挂，是删孤儿目录与保持构建成功的最小配套。二次复审 APPROVED：6 项全符合、承重闸门全绿（wails3 task build / 14/14 单测 / go vet / 隔离测试 / 交叉编译）、go.sum 还原后 go build ./... 仍过。新发现 Minor：M18 build/Taskfile.yml 残留 ios:device:list / ios:run:device 死任务（pre-existing，引用已删目录，不影响主路径，留后续）。

## Minor 总账（终审 triage 后）

- **已修**：M1 / M4 / M9(随 M1) / M10 / M13 / M15 / M16。
- **留 W5b-2**：M2 前端死控件；M3 desktop/README.md 模板原文；M5 embed 依赖 dist（构建路径本就是 wails3 task build）；M6 Stat 错误分支 state 无意义（语义已文档化）；M7 missing-file 用例未断言 Endpoint 零值；M11 unreachable 测试未断言 addr（底层已带）；M12 host=="" 且 err==nil 时 cause=nil；M17 BinPath 相对与 service.Spec 绝对路径张力（W5b-2 承接，真机走查需预知）；M18 ios 死任务。
- **弃**：M8（spec 强制打日志）、M14（日志断言过度测试）、M9 已随 M1 处理、N1 go.sum 非幂等（构建会脏 go.sum，已知）。
- **协调者 2026-08-16 交付**：docs/ledger-w5b1.md 入库（计划文件清单不含本台账，但仓库 ledger 惯例全入库、且「恢复现场以 ledger+git log 为准」，终审建议随分支提交）。

