<!-- 来源：熟练工 W2（code-investigate，mini@mac-mini，任务 7b28300e，基线 fad1aadf）产物 report.md 原样入仓。外部事实，执行节点复核。 -->

# 订阅/额度主动探测：provider 外部事实调研

日期：2026-10-21。范围：只做联网/文档调研（A 项按协调者指示改用另一路结论，本路未验证）。
本报告不输出任何密钥值。

---

## A. opencode-go（`opencode-go/...`）与 opencode zen（`opencode/xxx-free`）

### 官方口径（已核实）

来源：https://opencode.ai/v2/docs/console/go/

- 订阅制：Go $10/月、Go Plus $40/月；单人（Personal）workspace，不能邀请成员。
- 额度按**模型**分别设定月度美元上限（文档表格：如 DeepSeek V4.1 Flash $80、GLM-5.3 $15、MiMo-V2.6-Pro $15、Kimi K3 $15、GLM-5.3-Flash $60；Go Plus 翻倍左右）。
- 每个模型的窗口比例（文档原文）：**5 小时 = 月度上限的 20%，每周 = 50%，每月 = 100%**。这直接给出了三档阀值的口径（不是"请求数"，是折算美元用量）。
- 文档只写：`You can track your current usage in the console.` → https://opencode.ai/console 。**没有任何公开的用量/额度 HTTP API 文档**。
- 达到上限后请求被阻断（文档 Callout：「If you reach the usage limit, you can continue using the free models」）；若 Console 余额开启 **Extra usage**，超限后回落到按量计费余额而不是阻断。
- 模型清单接口（文档给的，非额度接口）：`GET https://opencode.ai/inference/go/v1/models`（无鉴权说明，返回模型元数据，无额度字段）。
- 推理端点（文档）：`https://opencode.ai/inference/go/openai/v1`（chat/completions、responses）、`/inference/go/anthropic/v1/messages`、`/inference/go/google/v1beta/...`。

### 用量接口（转述，未验证）

协调者另一路调研结论：用量接口见 orca 仓 `src/main/rate-limits/opencode-go-usage-api.ts`，走 **zen/billing**。本工作树与 `~/workspace` 下均无 orca 仓，**本路无法核对**，按转述记录。

### 分档探测建议（推断，未实测）

- 正常/低额度：只能靠上述 zen/billing 接口或 console 页面；官方文档未描述百分比字段，字段名以 orca 实现为准。
- 无额度：最小请求被阻断。文档未给出状态码/错误体，**未验证**；按 OpenAI 兼容惯例推测为 429 + `insufficient_quota` 一类错误体（推测）。
- 重置时间：文档未给；窗口是 5h/周/月，重置由服务端滚动窗口决定，**未验证**。

### opencode zen（`opencode/xxx-free`）

- 文档只有"free models"（如 LongCat 2.5 Preview Free / Beam Free / MiniMax M3.1 Flash Free），**没有公开额度查询接口**；这些模型在 Go 文档里标 Unlimited。
- 无订阅额度的 `opencode/xxx-free` 是否可无限调用、限速头如何，官方未写，**查不到**。

---

## B. DeepSeek 官方 API（api.deepseek.com）

**有官方余额接口。**

- 接口：`GET https://api.deepseek.com/user/balance`
- 鉴权：`Authorization: Bearer <DEEPSEEK_API_KEY>`
- 文档：https://api-docs.deepseek.com/api/get-user-balance

返回样例（文档 schema）：

```json
{
  "is_available": true,
  "balance_infos": [
    {
      "currency": "CNY",
      "total_balance": "110.00",
      "granted_balance": "10.00",
      "topped_up_balance": "100.00"
    }
  ]
}
```

字段：`is_available`（余额是否够调）、`balance_infos[].currency`（CNY/USD）、`total_balance`、`granted_balance`、`topped_up_balance`（均为字符串）。

- **重置时间：没有**。DeepSeek 是预付费余额，没有周期重置；充值即恢复。`granted_balance` 会过期但接口不返回过期时间（文档未给）。
- 分档：`is_available=false` 或 `total_balance` 低于阈值 → 无额度/低额度；三档只能自己按余额数值划线。

**最小请求的错误区分**（https://api-docs.deepseek.com/quick_start/error_codes）：

| 状态码 | 含义 | 用途 |
|---|---|---|
| 402 | Insufficient Balance（余额耗尽） | 无额度（明确） |
| 429 | Rate Limit Reached（并发超限，非额度） | 限速 |
| 401 | Authentication Fails | 凭据坏 |
| 503/500 | 服务过载 | 与额度无关 |

429 的官方/第三方样例错误体形如 `{"error":{"message":"Rate Limit Reached","type":"rate_limit_error","code":"rate_limit_reached"}}`（第三方整理：https://inferenceapis.com/reference/errors/deepseek-429-rate-limit-reached-503-server-overloaded ，官方错误码页只给状态码表）。官方未承诺 `Retry-After` 头，**未验证**。

---

## C. 阿里云百炼 Token Plan（provider `alibaba-token-plan-cn`）

provider 元数据（opencode 1.19.3 内置 models.dev 目录）：`alibaba-token-plan-cn` → `api: https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1`，env `ALIBABA_TOKEN_PLAN_API_KEY`，doc `https://www.alibabacloud.com/help/zh/model-studio/token-plan-overview`。API Key 形如 `sk-sp-`，与按量付费 `sk-` 完全隔离。

### 查额度/重置时间（有，但两条路都不是"推理 API Key 直接查"）

1. **官方 CLI（个人版可用，Console 登录态）**
   - `bl usage token-plan --output json`（可加 `--console-region cn-beijing --console-site domestic`）
   - 文档：https://help.aliyun.com/zh/model-studio/cli/usage-quota
   - 文档只说"查看 Token Plan 配额使用情况"，**未列字段**。第三方实测（CodexBar issue #3020，https://github.com/steipete/CodexBar/issues/3020 ）给出字段样例：
     ```json
     {"per5HourPercentage":0.70,"per5HourResetTime":1787001180000,
      "per1WeekPercentage":0.55,"per1WeekResetTime":1787600000000}
     ```
     百分比是**已用比例**（剩余 = 1 - value），重置时间是 epoch 毫秒；两个窗口可能只返回一个。第三方结论，官方未文档化。
2. **团队版 OpenAPI（AccessKey 签名，不是 API Key）**
   - `GET https://modelstudio.cn-beijing.aliyuncs.com/tokenplan/subscription/stats`
   - 鉴权：阿里云 AccessKey + `ACS3-HMAC-SHA256` 签名（`x-acs-action: GetSubscriptionStats`、`x-acs-version: 2026-02-10` 等）。**不支持** `Authorization: Bearer sk-sp-...`。
   - 文档：https://docs.bailian.console.aliyun.com/en/model-studio/get-subscription-stats （中文页同路径 `/zh/...`）
   - 返回：`Data.SubscriptionStartTime/EndTime`、`Items[]{SeatType, SeatRefreshTime(毫秒), TotalSeats, AssignedSeats, SeatCredits, SeatRemainingCredits}`。`SeatRemainingCredits/SeatCredits` 即剩余/总额度，`SeatRefreshTime` 即下期重置时间。
   - 权限：RAM `AliyunTokenPlanReadOnlyAccess`（文档 token-plan-team-quickstart）。
3. **个人版重置语义**：官方 FAQ 明确个人版是**月限额**（Lite 11,500 / Essential 25,500 / Standard 45,000 / Pro 180,000 Credits），**按订阅日滚动、不是每月 1 日固定重置**，未用完不结转；额度用尽即阻断，不转按量。来源：https://help.aliyun.com/zh/model-studio/token-plan-personal-faq 。

### 最小请求的错误区分（官方 FAQ 的错误表，https://help.aliyun.com/zh/model-studio/token-plan-personal-faq ）

| 状态码 | 错误体关键字 | 含义 |
|---|---|---|
| 429 | `insufficient_quota: Your token-plan quota has been exhausted.` | **无额度**（本期套餐耗尽） |
| 429 | `Requests rate limit exceeded` | **限速**（等待一分钟） |
| 401 | `InvalidApiKey` / `Incorrect API key provided` | 用错 Key 或 Base URL（如误用 `sk-` 按量 Key） |
| 403 | `AccessDenied.Unpurchased` | 模型不在套餐档位内 |
| 404 | `model_not_found` | 模型名错 |
| 502 | Bad Gateway | 服务端偶发 |

区分"无额度 vs 限速"完全靠 429 错误体的 message 前缀，**HTTP 状态码相同**；没有文档化的额度/重置响应头。低额度只能靠 1) 的百分比自己划线。

---

## D. ClinePass（`cline-pass` provider）

provider 元数据（opencode 1.19.3 目录）：`cline-pass` → `api: https://api.cline.bot/api/v1`，env `CLINE_API_KEY`，doc `https://docs.cline.bot/getting-started/clinepass`。

### 官方口径

- 官方文档只写「To check your current usage, visit your Cline dashboard」，并列出三个限额窗口：**5 小时滚动 / 每周 / 每月**（https://docs.cline.bot/getting-started/clinepass#usage）。
- 官网未把"查用量"写成公开 API；但 Cline 自己的 dashboard 与多个第三方工具在用下面的接口（属**未文档化但稳定**的账户接口）。

### 用量接口（社区实测，dashboard 同款）

- `GET https://api.cline.bot/api/v1/users/me/plan/usage-limits`
- 鉴权：`Authorization: Bearer <CLINE_API_KEY>`；或 Cline CLI 登录态（`~/.cline/data/settings/providers.json` 内的 workos bearer token，`cline auth` 写入）。
- 返回（第三方 CodexBar 文档 https://github.com/steipete/CodexBar/blob/main/docs/clinepass.md ；AxonHub PR https://github.com/looplj/axonhub/pull/2012 ；magpie 实现）：
  ```json
  {"limits":[
    {"type":"five_hour","percentUsed":42.5,"resetsAt":"2026-10-21T12:00:00Z"},
    {"type":"weekly","percentUsed":18.0,"resetsAt":"..."},
    {"type":"monthly","percentUsed":9.1,"resetsAt":"..."}
  ]}
  ```
  `percentUsed` 是 0–100 已用百分比；`resetsAt` 为 RFC3339（部分实现里可能缺省）。窗口缺省表示该档不生效。
- 无 ClinePass 订阅：该接口返回 **404**（magpie 实现注释）；此时只有按量余额：`GET /api/v1/users/me` 取 id → `GET /api/v1/users/{id}/balance`，`balance` 是**百万分之一美元**的整数（500000 = $0.50）。
- 官方 Enterprise API 参考（https://docs.cline.bot/enterprise-solutions/api-reference ）列了 `/users/me`、`/users/{id}/balance`、`/users/{id}/usages`，但**没有列** `/users/me/plan/usage-limits`。

### 最小请求的错误区分

未查到官方错误码表；第三方仅记录"无 ClinePass = 404"。限速/无额度的状态码与错误体**查不到**（推测与 OpenAI 兼容层一致，未验证）。

---

## E. OpenAI Codex（ChatGPT 账号 OAuth 登录）

### 结论：能在 Codex 之外查，但用的是未公开的内部接口

- 凭据：`$CODEX_HOME/auth.json`（未设则 `~/.codex/auth.json`；部分版本还会回落 macOS Keychain `Codex Auth`）。结构：`tokens.access_token` / `tokens.refresh_token` / `tokens.id_token` / `tokens.account_id`，顶层 `last_refresh`，或仅有 `OPENAI_API_KEY`（此时无订阅额度）。
- 查询接口：
  ```
  GET https://chatgpt.com/backend-api/wham/usage
  Authorization: Bearer <tokens.access_token>
  ChatGPT-Account-Id: <tokens.account_id>        # 可选
  ```
  来源（均非 OpenAI 官方文档，属逆向/实测）：https://gist.github.com/monperrus/21dc7d85ea518dc2a66606006d356b5b 、https://github.com/wikty/codex-accounts/blob/main/docs/internals/codex-app-internals.md 、https://github.com/burakgon/ai-usage-menubar/blob/main/docs/provider-contracts.md 。
- 返回样例（gist 原样）：
  ```json
  {
    "plan_type": "plus",
    "rate_limit": {
      "allowed": true,
      "limit_reached": false,
      "primary_window":   {"used_percent":55,"limit_window_seconds":18000,"reset_after_seconds":2547,"reset_at":1778670307},
      "secondary_window": {"used_percent":51,"limit_window_seconds":604800,"reset_after_seconds":489405,"reset_at":1779157165}
    },
    "credits": {"has_credits": false, "unlimited": false, "balance": "0"},
    "additional_rate_limits": null,
    "rate_limit_reset_credits": {"available_count": 3}
  }
  ```
  窗口类型**按 `limit_window_seconds` 判**：`18000` = 5 小时，`604800` = 7 天；不要假定 primary=5h（Team 计划可能只有 weekly 且放在 primary）。`used_percent` 是已用比例（可能 >100，需 clamp）。`reset_at` 是 epoch 秒。
- 备用字段：`x-codex-primary-used-percent` / `x-codex-secondary-used-percent` 响应头（provider-contracts.md 记为缺失时的兜底）。
- Codex 自身入口：TUI `/status` 显示 5h/周；`codex app-server` 的 JSON-RPC `account/rateLimits/read`（gist 注明）；CLI 每 ~60s 轮询该接口（https://github.com/openai/codex/issues/10869 ）。
- 刷新 token：`POST https://auth.openai.com/oauth/token`，`grant_type=refresh_token&client_id=app_EMoamEEZ73f0CkXaXp7hrann`。401/403 需重登或刷新一次。
- 注意：这是 **ChatGPT 后端内部接口**，无稳定性承诺、无官方文档；`used_percent` 到 0 的跳变/`reset_at` 漂移有实测记录（gist 的 caveat），不要把它当严格周历。

### 最小请求的错误区分

- 走推理接口时，Codex 错误码 `insufficient_quota` → 「Quota exceeded. Check your plan and billing details.」不可重试；`usage_not_included` → 提示升级 Plus（来自 opencode 1.19.3 内置 codex provider 的错误映射代码，属本地二进制观察）。
- 纯 HTTP 探测（wham/usage）：`plan_type=free` 或 `rate_limit.allowed=false` / `limit_reached=true` → 无额度/被限；正常 vs 低额度只能看 `used_percent`。

---

## F. opencode CLI 自身的用量/额度能力与凭据位置

- **`opencode stats`**：`show token usage and cost statistics`，参数 `--days/--models/--project/--tools`（本机 `opencode stats --help` 实测）。它统计的是**本地会话历史**（token 数与按模型价目折算的成本），**不是订阅额度**，也没有"还剩多少订阅额度/何时重置"。
- **`opencode providers list`**（别名 `opencode auth list`）：打印凭据文件路径与本机凭据条目（本机实测输出 `Credentials ~/.local/share/opencode/auth.json`，`0 credentials`），不显示额度。
- **`opencode serve` 的 HTTP 接口**：未找到任何额度/账单端点。本路对二进制的扫描已被叫停（协调者指示 A 项不再查），因此这条结论限于：CLI 帮助、`debug --help`、`providers list`、`stats --help` 中均无 quota/usage 命令，serve 侧**未见**（未穷尽）。
- **auth.json 位置**：`$XDG_DATA_HOME/opencode/auth.json`，否则 `~/.local/share/opencode/auth.json`（`opencode debug paths` 的 `data` 即 `~/.local/share/opencode`）。运行时可被 `OPENCODE_AUTH_CONTENT` 环境变量整体覆盖（本机 1.19.3 二进制内 Auth 服务）。
- **auth.json 结构**（只列字段名，不涉值）：顶层是以 provider id 为 key 的对象；每个值是三选一：
  - `{"type":"oauth","refresh","access","expires","accountId"?,"enterpriseUrl"?}`
  - `{"type":"api","key","metadata"?}`
  - `{"type":"wellknown","key","token"}`
  按此，`opencode-go` 走 API Key 订阅（文档流程是订阅后复制 API Key、`/connect` 粘贴），落盘应为 `type:"api"`；OAuth 类 provider（如 codex/copilot）才是 `type:"oauth"`。
  另：1.19.3 的 Console 账号（`opencode auth`/account 登录）不写 auth.json，而是 SQLite `~/.local/share/opencode/opencode.db` 的 `account` / `control_account` / `account_state` 表（列含 `email/url/access_token/refresh_token/token_expiry` 等；本机读取，未取任何值）。这是 `opencode console login` 那条线，和 provider 的 auth.json 是两套。

---

## 汇总：三档探测可行性

| provider | 官方额度接口 | 重置时间 | 正常/低/无 三档靠什么 |
|---|---|---|---|
| opencode-go | 无公开文档；转述有 zen/billing 接口（未验证） | 无公开字段 | 5h=20%、周=50%、月=100% 的折算美元用量；无额度靠请求被阻断（码未验证） |
| DeepSeek | `GET /user/balance`（Bearer） | 无（预付费） | `is_available` + `total_balance` 阈值；无额度请求返 402 |
| alibaba-token-plan-cn | `bl usage token-plan`（Console 登录）/ 团队 OpenAPI `GetSubscriptionStats`（AccessKey 签名） | CLI `*ResetTime`（ms）；团队 `SeatRefreshTime` | 已用百分比阈值；无额度 = 429 `insufficient_quota`，限速 = 429 `Requests rate limit exceeded` |
| cline-pass | `GET /api/v1/users/me/plan/usage-limits`（Bearer；未文档化） | `resetsAt`（RFC3339） | `percentUsed` 0–100；无订阅 = 404；无额度错误码未查到 |
| Codex（ChatGPT OAuth） | `GET chatgpt.com/backend-api/wham/usage`（Bearer；内部未文档化） | `reset_at` / `reset_after_seconds` | `used_percent` + `allowed`/`limit_reached`；窗口按 `limit_window_seconds` 判定 |
| opencode CLI 自身 | 无（`opencode stats` 仅本地历史成本） | 无 | 不适用于订阅额度 |

## 查不到 / 未验证清单

- opencode-go 的 zen/billing 具体 URL、鉴权头、返回字段（本路无 orca 仓，转述未核）。
- opencode-go 超限时的确切 HTTP 状态码与错误体、是否带重置头。
- opencode zen 免费模型的额度/限速规则。
- ClinePass 无额度/限速时的最小请求错误码与错误体（官方无错误码表）。
- 阿里云个人版 Token Plan 是否有 API Key 鉴权的官方额度接口（官方只给 Console 页面 + `bl` CLI；`GetSubscriptionStats` 是团队版 OpenAPI）。
- `opencode serve` 是否真无额度端点（扫描被叫停，未穷尽）。
