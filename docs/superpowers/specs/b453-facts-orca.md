# orca 订阅/账号额度用量功能调研报告

> 调研对象：https://github.com/stablyai/orca（浅克隆，`orca/` 目录，只读未运行）。
> 范围：`src/main/rate-limits/`（各 provider 额度抓取 + 轮询服务）与 `src/renderer/src/components/status-bar/`（底栏 UI）、设置页账号区。
> 约定：行号为克隆时 `HEAD` 的文件行；未找到的记为「查不到」并列出查过的位置；推测处标「推测」。

## 1. 支持的 provider 与每家取用量方式

额度类型定义：`src/shared/rate-limit-types.ts:89-134`（`ProviderRateLimits`：`session` 5h、`weekly` 7d、`fableWeekly`、`monthly`、`extraUsage`、`buckets`、`rateLimitResetCredits`、`planType`、`status: idle|fetching|ok|error|unavailable`）。

### Claude
- 接口：`GET https://api.anthropic.com/api/oauth/usage`（`src/main/rate-limits/claude-oauth-usage-request.ts:18`），`Authorization: Bearer <token>` + `anthropic-beta: oauth-2025-04-20` + `User-Agent: claude-code/2.1.0`（同文件 `:82-88`），超时 10s（`:19`）。
- 鉴权来源：macOS 走 keychain（`claude-accounts/keychain.ts` 经 `claude-oauth-credentials.ts:99-101`），否则读 `~/.claude/.credentials.json` 的 `claudeAiOauth.accessToken`（同文件 `:106-114`，schema `:29-36`）。多账号时按 `CLAUDE_CONFIG_DIR`（account dir）选 keychain item / credentials 文件目录（同文件 `:120-137`）。
- 取字段：`five_hour`→session（300min）、`seven_day`→weekly（10080min）、`fable_weekly/fable_seven_day/seven_day_fable/limits[]` 中 `kind=weekly_scoped` 且 model `fable`→`fableWeekly`（同文件 `:46-113`）；`spend/extra_usage`→`extraUsage`（`:109`，映射见 `claude-usage-credits.ts`）。窗口内字段 `utilization|used_percentage, resets_at`（`claude-usage-window.ts:4-8`，秒/毫秒自适应见 `:11-30`，上限钳制 0–100 见 `:70-74`）。

### Codex
- 主路径：本机 `codex` CLI 的 RPC 探测（短命 app-server 参数，`codex-fetcher.ts:90-117`，RPC/初始化超时 10s/30s，WSL 下 25s/40s，`:39-42`）；HTTP 兜底 `GET https://chatgpt.com/backend-api/wham/usage`（`codex-backend-usage-client.ts:69`，cookies 显式 `omit`，`codex-fetcher.ts:81-84`）。
- 鉴权：`<CODEX_HOME>/auth.json`（默认 `~/.codex`，`codex-backend-auth.ts:62-64`）中 `tokens.access_token` 作 `Bearer`，外加 `ChatGPT-Account-Id`（`tokens.account_id`）、`User-Agent: codex-cli`、`OpenAI-Beta: codex-1`、`originator: Codex Desktop`（同文件 `:80-89`）。先探 `auth.json` 是否存在，不存在就不起进程（`codex-auth-presence.ts:51-56`）。
- 取字段：`plan_type`→`planType`；`rate_limit.primary_window/secondary_window`（`{used_percent, limit_window_seconds, reset_at}`）经 `classifyCodexRateLimitWindows` 归类为 session/weekly（同文件 `:74-102`）；`rate_limit_reset_credits`→`rateLimitResetCredits`（`:93-98`）。还有会话窗口补充（`supplementCodexSessionWindow`）与重置额度消费（`codex-reset-credit-client.ts`）。

### Gemini
- 接口：`POST https://cloudcode-pa.googleapis.com/v1internal:retrieveUserQuota` body `{project}`（`gemini-usage-fetcher.ts:20,57-68`）；project 由 `POST ...:loadCodeAssist` 取 `cloudaicompanionProject`（`gemini-oauth-sources.ts:126-146`）。401 时刷 token 重试（`gemini-usage-fetcher.ts:137-146,194-214`）。
- 鉴权：`~/.gemini/oauth_creds.json`（`access_token/refresh_token/expiry_date`）或 opencode 的 `auth.json` 中 `google: {type:oauth, access, refresh}`（`gemini-oauth-sources.ts:31-79`），refresh 经 `https://oauth2.googleapis.com/token`（`:10,93-124`）。注意：后台轮询默认**关闭**，需设置里显式启用 Gemini CLI OAuth 才读（`gemini-usage-fetcher.ts:218-232`）。
- 取字段：`buckets[]` 中 `{remainingFraction, resetTime, modelId}`（`:22-43`），去重后 `deriveSessionSummary` 得 session，多 buckets 按模型分组展示（`:80-91`）。

### Cursor
- 接口：`GET https://cursor.com/api/usage-summary`，无 allowance 时回退 `GET https://cursor.com/api/usage?user=<subject>`（`cursor-fetcher.ts:18-21,196-235`）。鉴权为 session cookie（`Cookie` 头），必须带 `Origin: https://cursor.com` + `Referer: .../dashboard`（CSRF 要求，`:62-69`），`credentials: omit`、redirect manual（`:83-90`）。
- 鉴权来源（只读不写，`:176-179`）：macOS keychain（`cursor-access-token/cursor-user`）→ 旧 CLI `~/.cursor/auth.json`（`accessToken`）→ Cursor IDE 的 `state.vscdb`（`cursorAuth/*`），身份取 `cli-config.json` 的 `authInfo`（`cursor-auth.ts:137-212`，路径见 `cursor-auth-paths.ts:21-34`）。401→`stale-token`，429 解析 `Retry-After`（`cursor-fetcher.ts:91-118`）。
- 取字段：新版 `individualUsage`/spend 按月（`monthly`）+ 命名 pools（`buckets`）；旧版按模型 request-quota 取上限最大者（`cursor-usage-mapping.ts:192-228`）。无 allowance 的 200 → `unavailable`（`:237-243`）。

### opencode（opencode-go / Zen）
- API key 路径：`GET https://opencode.ai/zen/go/v1/usage`，`Authorization: Bearer <apiKey>`（`opencode-go-usage-api.ts:11,71-88`）；401→unauthorized，403/`EntitlementError`→no-subscription（`:48-59`）；body `{usage:{rolling, weekly, monthly:{percent, resetsAt}}}`（`opencode-go-status-parsing.ts:58-101`）。
- Cookie 路径：workspace 发现 `GET https://opencode.ai/_server?id=<hash>`（`opencode-go-usage-fetcher.ts:22-23,200-227`）→ `GET https://opencode.ai/console/api/go/status`（header `x-org-id`，console session 认证，`:234-251`），body `access.meters.{fiveHour,week,month}`（`{usedMicroCents, limitMicroCents, resetsAt}`，`opencode-go-status-parsing.ts:158-186`）；另并行取 `GET .../console/api/billing/status` 余额（5s 超时，`:27,138-166`）。只收白名单 cookie（`auth/__Host-auth/__Host-console_session`，`:34,57-71`）。
- 鉴权来源：Orca 设置里的 API key，或 `OPENCODE_API_KEY`，或 OpenCode `/connect` 存的 key，或用户粘贴的 Cookie（`opencode-go-usage-source-selection.ts`；状态见 `rate-limit-types.ts:198-205`）。

### Grok
- 接口：`GET https://cli-chat-proxy.grok.com/v1/billing?format=credits`（base 可被 `GROK_CLI_PROXY_BASE_URL` 覆盖），回退 `GET .../billing`（`grok-fetcher.ts:17-24,238-288`）。头 `Authorization: Bearer` + `X-XAI-Token-Auth: xai-grok-cli` + 可选 `x-userid`（`:181-190`）。401/403→error（`:251-256`）。
- 鉴权：只读 `~/.grok/auth.json`（或 `GROK_HOME`）中首选 issuer `https://auth.x.ai` 条目的 `key/user_id/email/team_id/expires_at`（`grok-auth.ts:67-131`）；过期（5min 偏斜，`:133-141`）不自己刷新，等 grok CLI 刷新（`grok-fetcher.ts:302-311`，`failureKind: delegated-refresh-required`）。
- 取字段：`config.creditUsagePercent`→weekly（10080min）；`monthlyLimit/used`→monthly（43200min）；`onDemandCap/onDemandUsed/prepaidBalance` 等仅做「有无用量」判断（`:104-179`）；`subscriptionTier`+email 拼展示名（`:220-235`）。

### Kimi（Moonshot Kimi Code）
- 接口：`GET ${KIMI_CODE_BASE_URL}/usages`（默认 `https://api.kimi.com/coding/v1/usages`，可用 `KIMI_CODE_BASE_URL` 覆盖，`kimi-fetcher.ts:21,339`），头与 CLI 的 `fetchManagedUsage` 一致（Bearer + Accept，无 UA，`:340-342`）。
- 鉴权：只读 `<kimi home>/credentials/kimi-code.json` 的 `access_token/expires_at`，**绝不刷新/重写**（15min TTL，CLI 拥有 token 生命周期，`:291-336`）；Windows 上默认读宿主 home，可选 WSL home（`:306-316`）。
- 取字段：顶层 `usage`→weekly；`limits[]`（`{window:{duration,timeUnit}, detail:{limit,remaining,used,resetTime}}`）中取最接近 5h 的→session（`:232-260`）。

### Antigravity
- 接口：无 HTTP——跑本机 `agy` CLI：`agy --version`（≥1.1.11，否则不可用，`antigravity-usage-command.ts:49,51-55`）→ `agy -p /usage --output-format json --print-timeout 20s`（`:14-27`）。旧版 `/usage` 会被当普通 prompt 花掉一次 quota，因此有版本门 + 模型-turn 闩锁（`antigravity-usage-fetcher.ts:32-43,189-191`）。
- 取字段：print-mode 信封 `command.data.groups[]`（`{name, buckets:[{id,name,window,remaining_fraction,reset_time,disabled}]}`），`used=(1-remaining)*100`（`antigravity-usage-response.ts:97-128`）；`5h`→session、`weekly`→weekly（`:188-195`），分组（Gemini Models / Claude and GPT models）作 `buckets`。

### MiniMax
- 接口：`GET {https://platform.minimax.io | https://www.minimaxi.com}/v1/api/openplatform/coding_plan/remains`（`minimax-request-context.ts:6-17`，测试 `:433-448`），cookie（session jar，`_token` 必需，`minimax-fetcher.ts:86-91`）或 API key（Bearer，优先，`:74-80`）。浏览器 UA/Referer 伪装（`minimax-request-context.ts:60-66,127-135`）。
- 设置页：session cookie + groupId + models + endpoint（overseas/cn）+ API key（`service-types.ts:59-65`，设置 UI `accounts-pane-minimax-credentials.tsx`）。

### ZCode（GLM Coding Plan）
- 接口：`GET {origin}/api/monitor/usage/quota/limit`，origin 仅允许 `api.z.ai/open.bigmodel.cn/dev.bigmodel.cn`（`zcode-usage-credentials.ts:70-98`），头 `Authorization: <apiKey原文>`（非 Bearer，`zcode-usage-fetcher.ts:173-182`）。
- 鉴权：`~/.zcode/cli/config.json`（`provider.<main>.options.{apiKey,baseURL}`，`:40-68`）或 v2 credential，或 Orca 设置里存的 GLM API key（`:100-127,131-164`）。跨账号串扰防护：换绑后刷新会比对 `authProvenance`（HMAC 指纹，`:153-163`；`zcode-usage-fetcher.ts:154-163`）。
- 取字段：`data.limits[]`（`TOKENS_LIMIT/CREDIT_LIMIT`→session/weekly 按分钟排序；`TIME_LIMIT`→monthly，月编码为 1 分钟的特殊处理，`:226-234`），`{usage,currentValue,remaining,percentage,nextResetTime}`（`:79-95`）；`data.level`→`planType`（`:248`）。5h 窗口重置时间若距今 >301min 则丢弃（防不合理值，`:124-127`）。

### 不支持
- Copilot：`src/main/copilot/` 只有 hook-service（managed hook 安装），无用量 fetcher——查不到 Copilot 额度支持（查过 `src/main/rate-limits/` 目录列表与 `copilot*` 文件名）。

## 2. 多账号建模与切换
- 只有 **Claude / Codex** 有「托管多账号 + 非活跃账号预览」：`RateLimitState.inactiveClaudeAccounts/inactiveCodexAccounts: InactiveAccountUsage[] {accountId, rateLimits, updatedAt, isFetching}`（`rate-limit-types.ts:148-154,222-223`）；主栏按所选 target 拉取，非活跃账号按需拉取（`service-inactive-accounts.ts`，防抖见下）。
- 账号标识：Claude 用设置里的托管账号 `id`（`activeClaudeManagedAccountId(+ByRuntime)`，`claude-accounts/runtime-selection.ts:28-62`）+ 运行时 target `{runtime: host|wsl, wslDistro}`（`:7-27`）；Codex 同理（`codex-accounts/runtime-selection.ts`，`service-types.ts:25-29`）。切换时旧账号快照进 inactive 缓存、新账号清空重试计划（`service-account-refresh.ts:65-95,149-189`），并有 generation/来源比对防竞态（`service-full-cycle-application.ts:138-170`）。
- 其余 provider 均为**单会话**：Cursor/Grok/Gemini/Kimi/ZCode/MiniMax/opencode-go/Antigravity 每次读当前本机登录（见 §1）；Cursor/Grok 设置页只展示单条 sign-in 状态（`CursorAccountsSection.tsx`，`GrokAccountsSection.tsx`）。WSL 视为第二运行时（Claude/Codex/Kimi 有 `wslDistro` 解析），不是第二账号。

## 3. 刷新策略
- 轮询：默认 15min（`DEFAULT_POLL_MS`），可设 30s–24.8d（`MIN_POLL_MS/MAX_POLL_MS`，`service/service-types.ts:97-99,137-143`）；仅当主窗口可见、未最小化、聚焦时才后台轮（`service-polling.ts:60-69`），focus/show/restore 时补刷（`service-configuration.ts:86-111`），启动延迟 1s（`DEFERRED_STARTUP_ACTIVE_REFRESH_MS`，`service-types.ts:121`）。
- 手动刷新：renderer `refreshRateLimits → window.api.rateLimits.refresh()`（`store/slices/rate-limits.ts:31-38`），另有 `refreshGrok/refreshClaudeForTarget/refreshCodexForTarget`（`:40-107`）；设置页 Cursor/Grok 有 “Refresh usage” 按钮（`CursorAccountsSection.tsx:78-83`，`GrokAccountsSection.tsx:46`）；状态栏刷新按钮走 `use-status-bar-controller.ts:93`。
- 节流/退避：手动/恢复连击防抖 5min（`MIN_REFETCH_MS`）；失败按 provider 指数退避 30s×2ⁿ、上限 15min、 streak 上限 8（`ACTIVE_FAILURE_REFETCH_MS/MAX_*`，`service-types.ts:101-104`）；`Retry-After`（`retryAtMs`）未到期不重取（`service-fetch-policy.ts:42-46,105-107`）；Claude 有 live-session（statusline 推送）时跳过 OAuth 轮询（`:48-64`），live 推送 30s 去重（`LIVE_CLAUDE_INGEST_DEDUPE_MS`，`service-types.ts:115`）；非活跃账号 fetch 防抖 60s、Codex 逐个 stagger 2s（`INACTIVE_*`，`service-types.ts:116-121`）。
- 缓存/离线：成功快照保留，失败时沿用旧数据并标 `error`（stale policy，`service-result-policy.ts:55-67`）；stale 丢弃阈值 30min，`rate-limited` 失败放宽到 24h（`STALE_THRESHOLD_MS/RATE_LIMITED_STALE_THRESHOLD_MS`，`service-types.ts:111-113`）；`unavailable`（未配置/未登录）直接清旧数据（`service-result-policy.ts:27-30`）。无专门离线模式——断网表现为 `failureKind: network` 的 error + 缓存（30min 内）或错误文案（查过 `usage-request-failure-kind.ts` 与各 fetcher catch 分支）。

## 4. 状态分档与无数据展示
- 阈值（硬编码，查不到可配项——查过 settings appearance/status-bar 搜索项，只有显隐开关与百分比显示模式）：`USAGE_WARNING_PERCENT=60`、`USAGE_URGENT_PERCENT=80`（`provider-extra-usage-section.tsx:9-20`，颜色 灰→黄→红）；芯片 urgency 与之对齐（`StatusBarProviderSegment.tsx:111-122`）。`extraUsage` 余额仅当某窗口 ≥99.5% 才在紧凑栏露出（`CAP_THRESHOLD_PERCENT`，同文件 `:206-222`）。
- 百分比显示模式（used/remaining，可配的是**显示**而非阈值）：`usage-percentage-display.ts`（`getDisplayedUsagePercentage`），状态栏可切。
- 无用量时：`idle/fetching` 显示 `···` 脉冲（`StatusBarProviderSegment.tsx:273-291`）；`unavailable` 显示 `--`（`:293-300`）；有缓存的 error 显示旧数据 + ⚠（`:328,366`），`tooltip.tsx:386-392` 标 “Refresh failed — showing cached data”（`tooltip.tsx:135-142`）；无数据的 error 显示状态文案（`getProviderUsageStatusLabel`，`usage-error-copy.ts`）。

## 5. 代理与网络
- 全局代理（无按账号代理——查过各 fetcher 签名，只有统一的 `NetworkProxySettings`）：`GlobalSettings.httpProxyUrl/httpProxyBypassRules`（`shared/global-settings-types.ts:201-203`，默认 `''`，`default-global-settings.ts:125`），经 `buildConfiguredProxyEnv` 注入 Codex CLI 子进程环境（`codex-fetcher.ts:104-105`），经 `ensureElectronProxyFromEnvironment` 作用于 Electron 网络（Claude `claude-oauth-usage-request.ts:40-44`，opencode-go `opencode-go-usage-api.ts:75-77`）；opencode-go/minimax 的隔离 session 按配置应用一次（`opencode-go-usage-fetcher.test.ts:277-302`）。WSL 探测不用 Windows 代理 env（`codex-fetcher.ts:104` 注释）。
- 超时多为 10s（Claude/Cursor/Gemini/Grok/Codex backend/Kimi），opencode-go/ZCode/MiniMax/Antigravity 15–30s（见各 fetcher `API_TIMEOUT_MS`）。

## 6. UI
- 底栏芯片：`StatusBarProviderSegment.tsx`（`ProviderSegment`：icon + 百分比/MiniBar + 超额余额 + ⚠；`UsageOverflowChip` 窄栏溢出收纳 `+N`，`:128-174`；`getUsageTone` urgency，`:114-122`）。行内用量条另见 `InlineProviderUsage.tsx`（session/weekly/fable 三条）。
- 悬停/点击：`ProviderDetailsMenu.tsx`——点击芯片开 dropdown（`DropdownMenuTrigger`，`:98-110`），面板为 `ProviderPanel`（`tooltip.tsx:261-395`：各窗口进度条、倒计时、Updated x ago、Codex reset credits、extraUsage、错误）；用法菜单可进 Settings（`use-status-bar-controller.ts:235-259`：Manage accounts→accounts pane，Usage details→stats pane，按 provider 直达 section）。
- 溢出：`status-bar-usage-collapse.ts`（按宽度折叠，placeholder 先让位，`use-status-bar-controller.ts:230-231`）+ `UsageOverflowChip`（title 显示被隐藏的 provider 与百分比）。
- 设置页：`AccountsPane.tsx` + 各 provider 区（`accounts-pane-claude-section.tsx`、`accounts-pane-codex-section.tsx`、`accounts-pane-opencode-credentials.tsx`、`accounts-pane-minimax-credentials.tsx`、`AntigravityAccountsSection.tsx`、`CursorAccountsSection.tsx`、`GrokAccountsSection.tsx` 等）；stats pane 看用量明细；appearance 下有按 provider 的状态栏显隐开关（`appearance-status-bar-*-toggle-search.ts`）。

## 7. 跨机器同步/上报
- 未发现用量数据跨机器同步或上报。证据：各 fetcher 只读本机凭据文件/keychain（见 §1）；注释明确 key“never leaves main”（`rate-limit-types.ts:193-218` 的各 `*Configured` 布尔字段说明）、opencode key“never reaches a log, a returned message, or telemetry”（`opencode-go-usage-api.ts:64-66`）；`rate-limits/` 内搜 `telemetry|upload|syncRemote|reportUsage` 仅命中无关项（cookie 白名单测试、failure streak 命名）。renderer/mobile 拿到的只是脱敏快照（Cursor 指纹为 sha256 截断，`cursor-fetcher.ts:37-39`；ZCode 用 HMAC 指纹，`zcode-usage-credentials.ts:91-94`）。远程执行场景下用量归属执行宿主（`owner-usage-rate-limits.ts`、`status-bar-runtime-targets.ts`），非同步——「推测」为按宿主各自拉取。

## 8. 许可证
- MIT，`Copyright (c) 2026 Lovecast Inc.`（`LICENSE:1-3`）。
