/*
 * B453 账号与额度 → B464 账号 = 身份、登录 = 账号在某机某 HOME 的一份凭据 —— 原型共用 mock 数据 + 控制台底栏与「使用量」弹层（照 orca）。
 *
 * 职责：所有页面（工作台 / 看板 / 流程 / 设置 / 账号 / 移动端设置）读同一份账号、登录与载体 mock，
 *       保证「账号在账本里，各机控制台看到同一份」在原型里也成立；并提供底栏挂载与各页共用的渲染片段。
 * 边界：纯 mock、零依赖、file:// 直开；不发任何请求。视觉 token 在 styles.css 的 B453 段。
 * B464 模型：
 *   - 账号 = 厂商侧身份（opencode 账号 id + org、codex / claude 邮箱…），按身份去重；额度读数、预警线 / 低额度线、
 *     探测间隔挂账号。账号名自动起（邮箱优先，否则「类型-短 id」），可改名。
 *   - 登录 = 账号在某机某 HOME 的一份凭据（机器、HOME、凭据来源、短指纹、状态、到期/刷新时刻、代理）。
 *     一个账号可有多条登录（多机，甚至同机多 HOME）；同机多个载体可共用一条登录。代理挂在登录上（每机各填）。
 *   - 发现：各机 agentd 扫主 HOME 与 ~/.handoff/home/*，认出已登录身份上报，同一身份归到同一账号下。
 *   - 载体 = 机器 × 执行器 × 账号登录 × 模型 × 并发上限；载体的 HOME 与凭据由所选登录决定，载体自己不再填 HOME。
 * 色彩纪律（2026-10-22 用户定，照 orca）：整体灰阶；彩色只给越线的数字/文字本身——
 *       ≥ 预警线、低额度、登录已过期/已失效 → 琥珀字；无额度 → 红字；探测失败/未探测 → 弱前景字；正常不加标记。
 * 用法：<script src="../shared/accounts.js" data-root="../"></script>，
 *       页面 body 加 class="has-statusbar" 后调用 HandoffAccounts.mountStatusBar()。
 */
(function () {
  const ROOT = (document.currentScript && document.currentScript.dataset.root) || '';
  const NOW = Date.now();
  const M = 60 * 1000, H = 60 * M, D = 24 * H;

  // 账号类型：额度都是「窗口百分比」——每个窗口给已用百分比 + resetAt（claude 不支持额度探测）。
  // 窗口词表对齐 orca（docs/superpowers/specs/b453-facts-orca.md §1）：session 5h / weekly 7d / monthly，
  // 界面上叫「会话 5h / 每周 / 每月」；另有 planType（套餐小标）与 extraUsage（窗口外的额外额度）。
  // cli = 对应的执行器；quota = 是否支持额度探测。
  const KINDS = {
    'opencode-go': { name: 'OpenCode Go', label: 'opencode-go 订阅', cli: 'opencode', quota: true,
      blurb: 'opencode-go 订阅；按 opencode 账号 id + org 认身份，额度由持有正常登录的机器定时探测。' },
    'codex':       { name: 'Codex', label: 'codex（ChatGPT 订阅）', cli: 'codex', quota: true,
      blurb: 'ChatGPT 订阅下的 codex 登录；按邮箱认身份，按会话 5h 与每周窗口计额度。' },
    // grok：weekly 7d = creditUsagePercent，monthly = used / monthlyLimit；planType 取 subscriptionTier。
    // token 由 grok CLI 自己刷新，探测只读不刷。
    'grok':        { name: 'Grok', label: 'grok（SuperGrok 订阅）', cli: 'grok', quota: true,
      blurb: 'SuperGrok 订阅；token 由该机 grok CLI 自己刷新，探测只读不刷。' },
    // agy（Antigravity CLI）：额度按模型组拆（Gemini / Claude·GPT），每组 5h 与每周各一个窗口；
    // 凭据是 agy CLI 自己的登录态；版本过旧时探测会消耗额度，直接跳过。
    'agy':         { name: 'Antigravity', label: 'Antigravity（agy）', cli: 'agy', quota: true,
      blurb: 'Antigravity CLI 订阅；按模型组（Gemini / Claude·GPT）各计 5h 与每周额度，凭据是 agy CLI 自己的登录态。' },
    // B464 新增：claude 只管登录，不支持额度探测（没有窗口条，派发不看额度）
    'claude':      { name: 'Claude', label: 'Claude（Claude Code 订阅）', cli: 'claude', quota: false,
      blurb: 'Claude Code 订阅登录；按邮箱认身份。不支持额度探测，引用它的载体照常派发。' },
    // B464 第二轮（用户裁决 A）：opencode auth.json 里的第三方 provider API key 也认成账号——身份 = key 短指纹，
    // 名字缺省「<provider>-key-<4位>」；不支持额度探测、不进底栏与使用量弹层；同一 HOME 可同时有 opencode-go 登录与 key。
    'apikey':      { name: '第三方 API key', label: '第三方 provider API key（opencode）', cli: 'opencode', quota: false,
      blurb: 'opencode auth.json 里的第三方 provider API key（deepseek、alibaba…）；按 key 短指纹认身份，不支持额度探测，引用它的载体照常派发。' },
  };
  // 执行器 ↔ 账号类型（载体弹窗只列兼容登录）；login = 在该机执行的登录命令
  const AGENTS = {
    // opencode 兼容两类账号：opencode-go 订阅与第三方 API key（kind = 新增登录时缺省识别的类型）
    opencode: { kind: 'opencode-go', kinds: ['opencode-go', 'apikey'], login: 'opencode auth login', short: 'oc' },
    codex:    { kind: 'codex',       login: 'codex login',         short: 'cx' },
    grok:     { kind: 'grok',        login: 'grok login',          short: 'gk' },
    agy:      { kind: 'agy',         login: 'agy login',           short: 'agy' },
    claude:   { kind: 'claude',      login: 'claude',              short: 'cl', note: '进入后输入 /login' },
  };
  // 单色字形：描边/填充都走 currentColor，跟随所在文字的灰阶
  const ICONS = {
    'opencode-go': '<svg viewBox="0 0 16 16"><rect x="3" y="2" width="10" height="12" rx="1.2" fill="none" stroke="currentColor" stroke-width="1.4"/><rect x="5.6" y="8.2" width="4.8" height="3.6" fill="currentColor"/></svg>',
    'codex':       '<svg viewBox="0 0 16 16"><path d="M8 1.8 13.4 4.9v6.2L8 14.2 2.6 11.1V4.9Z" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linejoin="round"/><path d="M6.4 6.3 4.9 8l1.5 1.7M9.6 6.3 11.1 8 9.6 9.7" fill="none" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"/></svg>',
    'grok':        '<svg viewBox="0 0 16 16"><rect x="2.2" y="2.2" width="11.6" height="11.6" rx="2.4" fill="none" stroke="currentColor" stroke-width="1.4"/><path d="M5.2 10.8 10.8 5.2" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>',
    'agy':         '<svg viewBox="0 0 16 16"><path d="M2.8 13.2 8 2.8l5.2 10.4" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"/><path d="M5.4 9.6h5.2" stroke="currentColor" stroke-width="1.4" stroke-linecap="round"/></svg>',
    'claude':      '<svg viewBox="0 0 16 16"><path d="M8 2.2v11.6M2.2 8h11.6M3.9 3.9l8.2 8.2M12.1 3.9 3.9 12.1" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round"/></svg>',
    'apikey':      '<svg viewBox="0 0 16 16"><circle cx="5.4" cy="10.6" r="2.8" fill="none" stroke="currentColor" stroke-width="1.4"/><path d="M7.4 8.6 13 3M11 5l1.8 1.8M9.6 6.4l1.4 1.4" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round"/></svg>',
  };
  const REFRESH_SVG = '<svg viewBox="0 0 16 16" aria-hidden="true"><path d="M13 8a5 5 0 1 1-1.5-3.6M13 2.5v3h-3" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"/></svg>';
  const STATUS = {
    out:  { label: '无额度',   cls: 'out',  rank: 0 },
    low:  { label: '低额度',   cls: 'low',  rank: 1 },
    fail: { label: '探测失败', cls: 'fail', rank: 2 },
    none: { label: '未探测',   cls: 'none', rank: 3 },
    ok:   { label: '正常',     cls: 'ok',   rank: 4 },
    na:   { label: '不支持额度探测', cls: 'na', rank: 5 },
  };
  // 登录状态：正常 / 已过期（凭据到期；CLI 运行时可能自行刷新，不暂停派发）/ 已失效（上次发现后在该 HOME 找不到凭据，暂停派发）
  const LOGIN_STATE = {
    ok:      { label: '正常' },
    expired: { label: '已过期', hint: 'CLI 运行时可能自行刷新；如失败请重新登录' },
    gone:    { label: '已失效', hint: '上次发现后找不到凭据，重新登录后自动恢复' },
  };
  // 载体选「无账号 · 主 HOME」（免费模型等不需要凭据）时 login 取这个值
  const NO_LOGIN = 'none';
  const MACHINES = ['mac-mini', 'linux-01', 'macbook-pro'];
  // 本机 = 控制台所在机器（登录命令不带 ssh 行）；其余机器给 ssh 目标
  const LOCAL = 'mac-mini';
  const SSH = { 'linux-01': 'root@linux-01', 'macbook-pro': 'xsx@macbook-pro' };
  // 刷新语义（对齐 orca §3）：默认每 15 分钟；最后一次成功读数超过 30 分钟记「已过期」；
  // 失败后指数退避 30s×2ⁿ，封顶 15 分钟。
  const DEFAULT_INTERVAL = 15, STALE_MS = 30 * M, DEFAULT_WARN = 60, DEFAULT_LOW = 80;
  const W = {
    session: (used, resetIn) => ({ key: 'session', label: '会话 5h', used, resetAt: NOW + resetIn }),
    weekly:  (used, resetIn) => ({ key: 'weekly',  label: '每周',    used, resetAt: NOW + resetIn }),
    monthly: (used, resetIn) => ({ key: 'monthly', label: '每月',    used, resetAt: NOW + resetIn }),
    // 带模型组的窗口（agy）：标签带组名，如「Gemini 5h」「Claude/GPT 周」
    gsession: (group, used, resetIn) => ({ key: 'session', group, label: `${group} 5h`, used, resetAt: NOW + resetIn }),
    gweekly:  (group, used, resetIn) => ({ key: 'weekly',  group, label: `${group} 周`,  used, resetAt: NOW + resetIn }),
  };
  // 登录构造：home '~' = 该机主 HOME；cred = { at: 'expires' | 'refreshed', ts }（凭据到期或最近刷新时刻）
  const L = (id, machine, home, source, fp, state, cred, proxy) => ({ id, machine, home, source, fp, state, cred, proxy: proxy || '' });
  const exp = (inMs) => ({ at: 'expires', ts: NOW + inMs });
  const ref = (agoMs) => ({ at: 'refreshed', ts: NOW - agoMs });

  // ---- 账号（发现得来，按身份去重；autoName = 自动起的名字，name 可改）。密钥不进账本，只存短指纹 ----
  // warn = 预警线（只变色），threshold = 低额度线（暂停派发），都是已用百分比，按账号可改。
  // probedAgo = 上次探测；goodAgo = 上次成功读数；nextIn = 距下次探测；failStreak = 连续失败次数。
  // brief = 探测失败原因的短语（底栏/弹层只放得下短语，完整原因在 error）。
  // 探测由持有「正常」登录的机器执行（probeVia）；没有正常登录时探测失败。
  const accounts = [
    // 改过名（自动名 opencode-go-3f9a）；两台机器各一条登录，两条都被载体共用
    { id: 'a-og1', name: 'og-main', autoName: 'opencode-go-3f9a', identity: 'acct_3f9a…c21e · org xsx-team', kind: 'opencode-go', planType: 'Go',
      warn: 60, threshold: 80, interval: 15, status: 'ok', probedAgo: 2 * M, goodAgo: 2 * M, nextIn: 13 * M, failStreak: 0, error: '', brief: '',
      quota: { windows: [W.session(34, 2 * H + 41 * M), W.weekly(58, 4 * D + 2 * H), W.monthly(22, 17 * D)], extraUsage: '$4.20' },
      logins: [
        L('l-og1-mini', 'mac-mini', '~', 'opencode.db 登录', 'sha256:3f9a…c21e', 'ok', ref(3 * M)),
        L('l-og1-cline', 'linux-01', '~/.handoff/home/cline', 'opencode.db 登录', 'sha256:3f9a…c21e', 'ok', ref(11 * M), 'socks5://127.0.0.1:1080'),
      ] },
    // 同机多 HOME：linux-01 上两个 HOME 都登着同一个身份
    { id: 'a-og2', name: 'og-team', autoName: 'opencode-go-77d0', identity: 'acct_77d0…e913 · org dp-lab', kind: 'opencode-go', planType: 'Go',
      warn: 60, threshold: 80, interval: 15, status: 'out', probedAgo: 6 * M, goodAgo: 6 * M, nextIn: 9 * M, failStreak: 0, error: '', brief: '',
      quota: { windows: [W.session(100, 3 * H + 12 * M), W.weekly(71, 1 * D + 9 * H), W.monthly(40, 9 * D)] },
      logins: [
        L('l-og2-dp', 'linux-01', '~/.handoff/home/dp', 'auth.json › opencode-go', 'sha256:77d0…e913', 'ok', ref(40 * M)),
        L('l-og2-mimo', 'linux-01', '~/.handoff/home/mimo', 'auth.json › opencode-go', 'sha256:77d0…e913', 'ok', ref(2 * H)),
      ] },
    // 未改名（自动名即账号名）；探测失败：该登录上的代理不通，沿用 47 分钟前的读数
    { id: 'a-og3', name: 'opencode-go-5e21', autoName: 'opencode-go-5e21', identity: 'acct_5e21…0b7d · org personal', kind: 'opencode-go', planType: 'Go',
      warn: 60, threshold: 80, interval: 15, status: 'fail', probedAgo: 2 * M, goodAgo: 47 * M, nextIn: 4 * M, failStreak: 3,
      error: '代理 socks5://127.0.0.1:7897 拒绝连接（mac-mini · ~/.handoff/home/mini-ds 这条登录上设置的代理）', brief: '代理拒绝连接',
      quota: { windows: [W.session(51, 1 * H + 5 * M), W.weekly(63, 5 * D + 3 * H), W.monthly(30, 17 * D)] },
      logins: [
        L('l-og3-ds', 'mac-mini', '~/.handoff/home/mini-ds', 'auth.json › opencode-go', 'sha256:5e21…0b7d', 'ok', ref(5 * H), 'socks5://127.0.0.1:7897'),
      ] },
    // 唯一一条登录已过期：探测拿不到新读数，显示旧读数；载体暂停（登录不可用）
    { id: 'a-cx1', name: 'ops@example.com', autoName: 'ops@example.com', identity: 'ops@example.com', kind: 'codex', planType: 'Plus',
      warn: 60, threshold: 80, interval: 15, status: 'fail', probedAgo: 1 * M, goodAgo: 3 * H, nextIn: 2 * M, failStreak: 2,
      error: '唯一的登录已过期（macbook-pro · ~/.handoff/home/mbp-codex），在该机重新登录后恢复探测', brief: '登录已过期',
      quota: { windows: [W.session(82, 1 * H + 48 * M), W.weekly(41, 3 * D + 5 * H)] },
      logins: [
        L('l-cx1-mbp', 'macbook-pro', '~/.handoff/home/mbp-codex', 'auth.json › tokens', 'sha256:e7a0…33d5', 'expired', exp(-5 * H)),
      ] },
    { id: 'a-gk1', name: 'grok-main', autoName: 'grok-6d4b', identity: 'uid_6d4b…a0f1', kind: 'grok', planType: 'SuperGrok',
      warn: 60, threshold: 80, interval: 15, status: 'ok', probedAgo: 5 * M, goodAgo: 5 * M, nextIn: 10 * M, failStreak: 0, error: '', brief: '',
      quota: { windows: [W.weekly(27, 3 * D + 14 * H), W.monthly(46, 12 * D)] },
      logins: [
        L('l-gk1-l01', 'linux-01', '~/.handoff/home/grok', 'auth.json › https://auth.x.ai', 'sha256:6d4b…a0f1', 'ok', exp(6 * D + 3 * H)),
      ] },
    // 唯一一条登录已失效：上次发现后该 HOME 里找不到凭据（目录被清或换了身份）
    { id: 'a-gk2', name: 'grok-b38e', autoName: 'grok-b38e', identity: 'uid_b38e…51c7', kind: 'grok', planType: '',
      warn: 60, threshold: 80, interval: 15, status: 'fail', probedAgo: 3 * M, goodAgo: null, nextIn: 2 * M, failStreak: 2,
      error: '没有可用登录：macbook-pro · ~/.handoff/home/mbp-grok 上次发现后找不到凭据', brief: '登录已失效',
      quota: null,
      logins: [
        L('l-gk2-mbp', 'macbook-pro', '~/.handoff/home/mbp-grok', 'auth.json › https://auth.x.ai', 'sha256:b38e…51c7', 'gone', ref(2 * D)),
      ] },
    { id: 'a-agy1', name: 'agy-main', autoName: 'agy-a91c', identity: 'xsx.dev@gmail.com', kind: 'agy', planType: 'Pro',
      warn: 60, threshold: 80, interval: 15, status: 'ok', probedAgo: 4 * M, goodAgo: 4 * M, nextIn: 11 * M, failStreak: 0, error: '', brief: '',
      quota: { windows: [W.gsession('Gemini', 22, 3 * H + 20 * M), W.gweekly('Gemini', 35, 5 * D + 6 * H),
                         W.gsession('Claude/GPT', 61, 2 * H + 5 * M), W.gweekly('Claude/GPT', 48, 5 * D + 6 * H)] },
      logins: [
        L('l-agy1-l01', 'linux-01', '~/.handoff/home/agy', 'agy CLI 登录态', 'sha256:a91c…6e04', 'ok', ref(1 * D)),
      ] },
    // 探测失败且无旧读数：agy 版本过旧时探测本身会消耗额度，agentd 直接跳过，不退避重试
    { id: 'a-agy2', name: 'agy-0d7f', autoName: 'agy-0d7f', identity: 'uid_0d7f…b2a8', kind: 'agy', planType: '',
      warn: 60, threshold: 80, interval: 15, status: 'fail', probedAgo: 4 * M, goodAgo: null, nextIn: 11 * M, failStreak: 0,
      error: 'agy 版本过旧（< 1.1.11），探测会消耗额度，已跳过', brief: 'agy 版本过旧，已跳过',
      quota: null,
      logins: [
        L('l-agy2-mbp', 'macbook-pro', '~', 'agy CLI 登录态', 'sha256:0d7f…b2a8', 'ok', ref(3 * D)),
      ] },
    // B464 新增：claude 账号——两台 Mac 的主 HOME 各一条登录；不支持额度探测
    { id: 'a-cl1', name: 'xsx@example.com', autoName: 'xsx@example.com', identity: 'xsx@example.com', kind: 'claude', planType: 'Max',
      warn: null, threshold: null, interval: null, status: 'na', probedAgo: null, goodAgo: null, nextIn: null, failStreak: 0, error: '', brief: '',
      quota: null,
      logins: [
        L('l-cl1-mini', 'mac-mini', '~', '钥匙串 › Claude Code-credentials', 'sha256:c4e2…19ab', 'ok', exp(7 * H + 20 * M)),
        L('l-cl1-mbp', 'macbook-pro', '~', '钥匙串 › Claude Code-credentials', 'sha256:c4e2…19ab', 'ok', exp(2 * D + 4 * H)),
      ] },
    // B464 第二轮：第三方 provider API key（身份 = key 短指纹；cred.at = 'static' 表示 key 本身不过期）。
    // linux-01 的 ~/.handoff/home/dp 同时有 og-team 的 opencode-go 登录与这把 deepseek key
    { id: 'a-ds1', name: 'deepseek-key-9c41', autoName: 'deepseek-key-9c41', identity: 'deepseek key sha256:9c41…d07e', kind: 'apikey', provider: 'deepseek', planType: '',
      warn: null, threshold: null, interval: null, status: 'na', probedAgo: null, goodAgo: null, nextIn: null, failStreak: 0, error: '', brief: '',
      quota: null,
      logins: [
        L('l-ds1-mini', 'mac-mini', '~', 'auth.json › deepseek', 'sha256:9c41…d07e', 'ok', { at: 'static' }),
        L('l-ds1-dp', 'linux-01', '~/.handoff/home/dp', 'auth.json › deepseek', 'sha256:9c41…d07e', 'ok', { at: 'static' }),
      ] },
    { id: 'a-al1', name: 'alibaba-key-52e0', autoName: 'alibaba-key-52e0', identity: 'alibaba key sha256:52e0…8a3f', kind: 'apikey', provider: 'alibaba', planType: '',
      warn: null, threshold: null, interval: null, status: 'na', probedAgo: null, goodAgo: null, nextIn: null, failStreak: 0, error: '', brief: '',
      quota: null,
      logins: [
        L('l-al1-ali', 'mac-mini', '~/.handoff/home/mini-ali', 'auth.json › alibaba', 'sha256:52e0…8a3f', 'ok', { at: 'static' }),
      ] },
  ];

  // ---- 载体（设置·自动化）= 机器 × 执行器 × 账号登录 × 模型 × 并发上限。login = 所用登录 id；HOME / 凭据 / 代理由登录决定 ----
  const carriers = [
    { name: 'mimo', machine: 'linux-01', cli: 'opencode', login: 'l-og2-mimo', model: 'opencode-go/deepseek-v4.1-flash', max_concurrency: 2, status: 'online', last_error: '', version: 7, running: 1 },
    { name: 'dp', machine: 'linux-01', cli: 'opencode', login: 'l-og2-dp', model: 'opencode-go/kimi-k2.6', max_concurrency: 2, status: 'online', last_error: '', version: 4, running: 1 },
    { name: 'cline', machine: 'linux-01', cli: 'opencode', login: 'l-og1-cline', model: '', max_concurrency: 1, status: 'online', last_error: '', version: 3, running: 0 },
    { name: 'cline-flash', machine: 'linux-01', cli: 'opencode', login: 'l-og1-cline', model: 'opencode-go/deepseek-v4.1-flash', max_concurrency: 2, status: 'online', last_error: '', version: 1, running: 0 },
    { name: 'agy', machine: 'linux-01', cli: 'agy', login: 'l-agy1-l01', model: '', max_concurrency: 1, status: 'online', last_error: '', version: 1, running: 0 },
    { name: 'grok', machine: 'linux-01', cli: 'grok', login: 'l-gk1-l01', model: 'grok-4.3', max_concurrency: 1, status: 'online', last_error: '', version: 1, running: 0 },
    { name: 'mini', machine: 'mac-mini', cli: 'opencode', login: 'l-og1-mini', model: 'opencode-go/deepseek-v4.1-flash', max_concurrency: 1, status: 'online', last_error: '', version: 5, running: 1 },
    { name: 'mini-pro', machine: 'mac-mini', cli: 'opencode', login: 'l-og1-mini', model: 'opencode-go/kimi-k2.6', max_concurrency: 1, status: 'online', last_error: '', version: 2, running: 0 },
    { name: 'mini-ds', machine: 'mac-mini', cli: 'opencode', login: 'l-og3-ds', model: 'opencode-go/deepseek-v4.1-flash', max_concurrency: 2, status: 'online', last_error: '', version: 2, running: 0 },
    // B464 第二轮恢复：选第三方 API key 账号
    { name: 'mini-ali', machine: 'mac-mini', cli: 'opencode', login: 'l-al1-ali', model: 'alibaba/qwen3.6-coder-plus', max_concurrency: 1, status: 'online', last_error: '', version: 1, running: 0 },
    // B464 第二轮恢复：免费模型，不需要凭据 → 「无账号 · 主 HOME」
    { name: 'mbp', machine: 'macbook-pro', cli: 'opencode', login: NO_LOGIN, model: 'opencode/deepseek-v4.1-flash-free', max_concurrency: 1, status: 'online', last_error: '', version: 6, running: 0 },
    // 所用登录已过期：照常派发，只标红并给复制登录命令
    { name: 'mbp-codex', machine: 'macbook-pro', cli: 'codex', login: 'l-cx1-mbp', model: 'gpt-5.6-luna', max_concurrency: 2, status: 'online', last_error: '', version: 2, running: 0 },
    // 所用登录已失效：暂停派发，重新登录后自动恢复
    { name: 'mbp-grok', machine: 'macbook-pro', cli: 'grok', login: 'l-gk2-mbp', model: 'grok-4.3', max_concurrency: 1, status: 'online', last_error: '', version: 1, running: 0 },
    { name: 'mbp-claude', machine: 'macbook-pro', cli: 'claude', login: 'l-cl1-mbp', model: 'claude-sonnet-4.5', max_concurrency: 1, status: 'online', last_error: '', version: 1, running: 0 },
  ];

  // 发现：各机 agentd 扫主 HOME 与 ~/.handoff/home/*；上次发现时刻（mock：2 分钟前）
  const discovery = { lastAgo: 2 * M, at: NOW - 2 * M };

  // ---------------- 账号 / 登录 / 载体 关系 ----------------
  function loginById(id) {
    for (const a of accounts) { const l = a.logins.find((x) => x.id === id); if (l) return { acct: a, login: l }; }
    return null;
  }
  function accountOfCarrier(c) { const r = c.login && loginById(c.login); return r ? r.acct : null; }
  function carriersOfLogin(id) { return carriers.filter((c) => c.login === id); }
  function homeText(home) { return home === '~' ? '~（主 HOME）' : home; }
  // 账号的登录所在机器（去重，按登录顺序）
  function machinesOf(a) { return [...new Set(a.logins.map((l) => l.machine))]; }
  function machinesText(a) { return machinesOf(a).join(' · ') || '无登录'; }
  // 探测由第一条「正常」登录所在机的 agentd 执行
  function probeVia(a) { const l = a.logins.find((x) => x.state === 'ok'); return l ? l.machine : ''; }
  // 某机某执行器可选的登录（载体弹窗账号下拉用）：账号类型匹配 + 登录在该机（已过期/已失效仍列出、标注）
  function kindsOf(cli) { const ag = AGENTS[cli]; return ag ? (ag.kinds || [ag.kind]) : []; }
  function loginsFor(machine, cli) {
    const ks = kindsOf(cli);
    const out = [];
    accounts.filter((a) => ks.includes(a.kind)).forEach((a) => a.logins
      .filter((l) => l.machine === machine)
      .forEach((l) => out.push({ acct: a, login: l })));
    return out;
  }
  // 登录命令：本机只给一行；远端先 ssh 再执行。主 HOME 不加 HOME= 前缀
  function loginCommand(machine, cli, home) {
    const ag = AGENTS[cli];
    const run = `${home && home !== '~' ? `HOME=${home} ` : ''}${ag.login}`;
    return { ssh: machine === LOCAL ? '' : `ssh ${SSH[machine] || machine}`, run, note: ag.note || '' };
  }
  function loginCommandText(machine, cli, home) {
    const c = loginCommand(machine, cli, home);
    return (c.ssh ? `${c.ssh} 后执行：` : '') + c.run + (c.note ? `（${c.note}）` : '');
  }
  function cliOfKind(kind) { return KINDS[kind].cli; }
  // 登录的凭据时刻：到期 / 已到期 / 最近刷新；API key 不过期
  function credTimeText(l) {
    if (!l.cred || l.cred.at === 'static') return 'key 不过期';
    if (l.cred.at === 'refreshed') return `刷新 ${ago(Date.now() - l.cred.ts)}`;
    return l.cred.ts > Date.now() ? `到期 ${clock(l.cred.ts)}` : `已于 ${clock(l.cred.ts)} 到期`;
  }

  // ---------------- 格式化 ----------------
  function countdown(ms) {
    if (ms <= 0) return '即将';
    const d = Math.floor(ms / D), h = Math.floor((ms % D) / H), m = Math.floor((ms % H) / M);
    if (d) return `${d}d ${h}h`;
    if (h) return `${h}h${String(m).padStart(2, '0')}m`;
    return `${Math.max(m, 1)}m`;
  }
  function clock(ts) {
    const t = new Date(ts);
    const p = (n) => String(n).padStart(2, '0');
    return `${p(t.getMonth() + 1)}-${p(t.getDate())} ${p(t.getHours())}:${p(t.getMinutes())}`;
  }
  function ago(ms) {
    if (ms == null) return '从未';
    if (ms < M) return '刚刚';
    if (ms < H) return `${Math.floor(ms / M)} 分钟前`;
    if (ms < D) return `${Math.floor(ms / H)} 小时前`;
    return `${Math.floor(ms / D)} 天前`;
  }
  function find(name) { return accounts.find((a) => a.name === name); }
  function findById(id) { return accounts.find((a) => a.id === id); }
  // 使用某账号的载体 = 用它任一条登录的载体
  function carriersOf(name) {
    const a = find(name);
    if (!a) return [];
    const ids = a.logins.map((l) => l.id);
    return carriers.filter((c) => ids.includes(c.login));
  }
  // 支持额度探测的账号（底栏与使用量弹层只列这些；claude 不进底栏）
  // B467：已忽略的账号（账本里仍在、发现照常扫到它的登录，但不探测、不进底栏/弹层/载体下拉；可恢复）
  const ignored = [
    { id: 'a-og9', name: 'opencode-54ce', autoName: 'opencode-54ce', identity: 'acct_54ce…9a10 · org xsx-old', kind: 'opencode-go', planType: 'Go',
      warn: 60, threshold: 80, interval: 15, status: 'none', probedAgo: null, goodAgo: null, nextIn: 0, failStreak: 0, error: '', brief: '', quota: null,
      ignoredAt: NOW - 3 * H, ignoredBy: 'mac-mini',
      logins: [L('l-og9-x', 'mac-pro', '~', 'opencode.db 登录', 'sha256:54ce…9a10', 'ok', ref(9 * D))] },
  ];
  function ignore(a) { const i = accounts.indexOf(a); if (i < 0) return; accounts.splice(i, 1); a.ignoredAt = Date.now(); ignored.unshift(a); notify(); }
  function restore(a) { const i = ignored.indexOf(a); if (i < 0) return; ignored.splice(i, 1); delete a.ignoredAt; accounts.push(a); notify(); }
  function quotaAccounts() { return accounts.filter((a) => KINDS[a.kind].quota); }
  function byProblem(list) { return list.slice().sort((x, y) => STATUS[x.status].rank - STATUS[y.status].rank); }

  // 窗口分档：< 预警线 正常；≥ 预警线 只变色；≥ 低额度线 低额度（暂停派发）；100% 无额度
  function winTone(a, w) {
    if (w.used >= 100) return 'out';
    if (a.threshold != null && w.used >= a.threshold) return 'low';
    if (a.warn != null && w.used >= a.warn) return 'warn';
    return 'ok';
  }
  // 分档 → 文字色类：越线（预警/低额度）琥珀，无额度红，其余不上色
  function toneCls(t) { return t === 'out' ? 'q-out' : (t === 'low' || t === 'warn') ? 'q-warn' : ''; }
  const isStale = (a) => a.goodAgo != null && a.goodAgo > STALE_MS;
  // 最近一个与当前状态相关的重置时刻（无额度/低额度才有意义）
  function resetOf(a) {
    if (!a.quota) return null;
    const hot = a.quota.windows.filter((w) => ['low', 'out'].includes(winTone(a, w)));
    return Math.min(...(hot.length ? hot : a.quota.windows).map((w) => w.resetAt));
  }
  function hotWindow(a) { return a.quota && a.quota.windows.find((w) => ['low', 'out'].includes(winTone(a, w))); }
  // 最紧窗口：已用最高的那个（并列取先重置的）——底栏与精简列表只报它
  function tightest(a) {
    if (!a.quota) return null;
    return a.quota.windows.slice().sort((x, y) => (y.used - x.used) || (x.resetAt - y.resetAt))[0];
  }
  // 下次探测：正常按间隔；失败按 30s×2ⁿ 退避（封顶 15 分钟）
  function nextProbeText(a) {
    const t = `下次 ${countdown(a.nextIn)} 后`;
    return a.failStreak ? `${t}（连续失败 ${a.failStreak} 次，退避中）` : t;
  }
  // 载体行的派发提示：低额度/无额度 → 暂停派发；探测失败/未探测 → 照常派发（额度未知）
  // B464：先看所用登录——只有「已失效」（发现扫不到凭据）暂停派发；「已过期」照常派发，只标红提示；再看账号额度
  function dispatchHint(c) {
    if (c.login === NO_LOGIN) return null; // 无账号 · 主 HOME：不看额度、不看登录
    const r0 = c.login && loginById(c.login);
    if (!r0) return { cls: 'muted', paused: true, text: '未选登录：不进派发' };
    const a = r0.acct, lg = r0.login;
    if (lg.state === 'gone') {
      return { cls: 'out', paused: true, login: true, text: `暂停派发：所用登录已失效（${lg.machine} ${homeText(lg.home)}），重新登录后自动恢复` };
    }
    const quota = quotaHint(a);
    if (quota && quota.paused) return quota;
    if (lg.state === 'expired') return { cls: 'out', login: true, text: '登录已过期，CLI 运行时可能自行刷新；如失败请重新登录' };
    return quota;
  }
  function quotaHint(a) {
    if (a.status === 'na') return { cls: 'muted', text: '不支持额度探测，照常派发' };
    const r = resetOf(a);
    if (a.status === 'out') {
      return { cls: 'out', paused: true, text: r ? `暂停派发，预计 ${countdown(r - Date.now())} 后恢复` : `暂停派发；${nextProbeText(a)}再探测` };
    }
    if (a.status === 'low') {
      const w = hotWindow(a);
      return { cls: 'low', paused: true, text: `暂停派发（${w.label} 已用 ${w.used}%），预计 ${countdown(r - Date.now())} 后恢复` };
    }
    if (a.status === 'fail') return { cls: 'muted', text: '额度未知，照常派发' };
    if (a.status === 'none') return { cls: 'muted', text: '额度未知，照常派发' };
    return null;
  }

  // ---------------- 渲染片段（各页共用） ----------------
  function icon(kind) { return `<span class="k-ic" aria-hidden="true">${ICONS[kind] || ''}</span>`; }
  // 账号名旁的小标：第三方 API key 显示 provider，其余显示套餐（planType）
  function plan(a) {
    if (a.provider) return `<span class="q-tag" title="provider">${a.provider}</span>`;
    return a.planType ? `<span class="q-tag" title="套餐（planType）">${a.planType}</span>` : '';
  }
  // 倒计时 span：data-reset 存绝对时刻，data-tpl 是文案模板（{} 处填倒计时），全局每 30s 刷一次
  function resetSpan(ts, tpl) {
    tpl = tpl || '{}';
    return `<span class="q-reset" data-reset="${ts}" data-tpl="${tpl}">${tpl.replace('{}', countdown(ts - Date.now()))}</span>`;
  }
  function pct(a, w) { return `<span class="q-pct ${toneCls(winTone(a, w))}">${w.used}%</span>`; }
  // 状态短语：正常返回空串（正常不加任何标记）
  function statusText(a) {
    if (a.status === 'out') return '<span class="q-out">无额度</span>';
    if (a.status === 'low') { const w = hotWindow(a); return `<span class="q-warn">低额度${w ? ` · ${shortLabel(w)} ${w.used}%` : ''}</span>`; }
    if (a.status === 'fail') return `<span class="q-mute">△ ${a.brief || '探测失败'}</span>`;
    if (a.status === 'none') return '<span class="q-mute">··· 未探测</span>';
    if (a.status === 'na') return '<span class="q-mute">不支持额度探测</span>';
    return '';
  }
  // 窗口短名：低额度短语里用（会话 5h → 会话）
  function shortLabel(w) { return w.group ? w.label : w.label.replace(/\s*5h$/, ''); }
  // 每周窗口：底栏与弹层列表的统一口径（2026-10-22 用户第二轮定：列表只报每周，会话/每月看 hover 面板）
  function weekly(a) { return a.quota && (a.quota.windows.find((w) => w.key === 'weekly') || tightest(a)); }
  // 一句话读数（每周口径）。问题状态：短语在前、倒计时在后（无额度 3h11m / 低额度 · 会话 82% 1h47m）；
  // 正常：order='lead' 百分比在前（底栏：og-main 58% 4d 1h），order='tail' 倒计时在前（弹层列表，照 orca：4d 1h 58%）
  function summary(a, order) {
    if (a.status === 'out') return `${statusText(a)} ${resetOf(a) ? resetSpan(resetOf(a)) : ''}`;
    if (a.status === 'low') { const w = hotWindow(a); return `${statusText(a)} ${w ? resetSpan(w.resetAt) : ''}`; }
    if (a.status === 'fail' || a.status === 'none' || a.status === 'na') return statusText(a);
    const w = weekly(a);
    if (!w) return '';
    return order === 'tail' ? `${resetSpan(w.resetAt)} ${pct(a, w)}` : `${pct(a, w)} ${resetSpan(w.resetAt)}`;
  }
  // ---- 详细模式（照 orca）：首行注记 + 次行全部窗口横排 ----
  function tinyLabel(w) { return { session: '5h', weekly: '周', monthly: '月' }[w.key] || w.label; }
  function earliestReset(a) { return a.quota ? Math.min(...a.quota.windows.map((w) => w.resetAt)) : null; }
  // 首行右侧注记：正常 = 最早重置的窗口「x 后重置」；问题账号 = 状态短语
  function detailNote(a) {
    if (a.status === 'out') { const r = resetOf(a); return `<span class="q-out">无额度</span>${r ? ` · ${resetSpan(r, '{} 后恢复')}` : ''}`; }
    if (a.status !== 'ok') return statusText(a);
    const r = earliestReset(a);
    return r ? resetSpan(r, '{} 后重置') : '';
  }
  // 次行：5h ▬ 34%  周 ▬ 58%  月 ▬ 22%；没有的窗口不占位；探测失败的旧读数整体降为弱色；无读数返回空串（不出次行）
  function winsInline(a) {
    if (!a.quota) return '';
    const qi = (w) => `<span class="qi" title="${w.label}"><span class="qi-l">${tinyLabel(w)}</span>${bar(a, w, 'qi-b')}${pct(a, w)}</span>`;
    const dim = a.status === 'fail' ? ' q-cached' : '';
    const groups = [...new Set(a.quota.windows.map((w) => w.group).filter(Boolean))];
    // 按模型组拆的账号（agy）：一组一行，组名作行首弱色小字，别挤在一行
    if (groups.length) {
      return `<span class="q-inline grouped${dim}">${groups.map((g) =>
        `<span class="qg"><span class="qg-n">${g}</span>${a.quota.windows.filter((w) => w.group === g).map(qi).join('')}</span>`).join('')}</span>`;
    }
    return `<span class="q-inline${dim}">${a.quota.windows.map(qi).join('')}</span>`;
  }
  // 最短的两个窗口（按时长 5h < 周 < 月，同档保持原序）——底栏详细段用
  const SPAN = { session: 0, weekly: 1, monthly: 2 };
  function shortestTwo(a) { return a.quota.windows.slice().sort((x, y) => SPAN[x.key] - SPAN[y.key]).slice(0, 2); }
  // 底栏读数（2026-10-22 用户第五轮定：底栏不写「低额度」「无额度」，只放数值与细条，越线靠颜色）。
  // 详细 = 短细条（最紧窗口）+ 最短两个窗口的「已用% 倒计时」；精简 = 每周「已用% 倒计时」（无额度/低额度报触发窗口）。
  // 探测失败有旧读数：照画数值但整体降为弱色，后跟「△」；无读数：「△ 短因」/「··· 未探测」。
  function barReading(a, mode) {
    if (!a.quota) return statusText(a);
    const cached = a.status === 'fail';
    // 精简报每周；但无额度/低额度若由别的窗口触发（如会话 5h），改报触发窗口——否则越线信号在底栏上消失
    const hot = (a.status === 'out' || a.status === 'low') && hotWindow(a);
    const ws = mode === 'detail' ? shortestTwo(a) : [hot || weekly(a)];
    const t = tightest(a);
    const mini = mode === 'detail' && t ? `<span class="sb-mb">${bar(a, t)}</span>` : '';
    const nums = ws.map((w) => `<span class="sb-w" title="${w.label}">${pct(a, w)} ${resetSpan(w.resetAt)}</span>`).join('<span class="sb-dot">·</span>');
    return `<span class="sb-read${cached ? ' q-cached' : ''}">${mini}${nums}</span>${cached ? `<span class="q-mute" title="${a.error}">△</span>` : ''}`;
  }
  function bar(a, w, cls) {
    return `<span class="qbar ${toneCls(winTone(a, w))} ${cls || ''}"><i style="width:${Math.min(w.used, 100)}%"></i></span>`;
  }
  // 紧凑窗口行（移动端展开、账号页卡片第三行共用）：窗口名 细条 已用% 倒计时
  function winsCompact(a) {
    if (!a.quota) return `<div class="q-wins-empty">${a.status === 'none' ? '尚未探测' : '额度未知'}</div>`;
    const dim = a.status === 'fail' ? ' q-cached' : '';
    return `<div class="q-wins${dim}">${a.quota.windows.map((w) => `
      <div class="q-win"><span class="q-wl">${w.label}</span>${bar(a, w)}${pct(a, w)}${resetSpan(w.resetAt)}</div>`).join('')}</div>`;
  }
  // 二级面板 / 账号页详情共用：一个窗口一节（窗口名、细条、左已用右重置）
  function winsFull(a) {
    if (!a.quota) return '';
    return a.quota.windows.map((w) => `
      <div class="sp-win${a.status === 'fail' ? ' q-cached' : ''}">
        <div class="sp-wl">${w.label}</div>
        ${bar(a, w)}
        <div class="sp-wr"><span>已用 ${pct(a, w)}</span>${resetSpan(w.resetAt, '{} 后重置')}</div>
      </div>`).join('');
  }
  function updatedText(a) {
    if (a.status === 'na') return '不支持额度探测';
    if (a.status === 'none') return '尚未探测';
    if (a.status === 'fail') return a.goodAgo != null ? `${ago(a.probedAgo)}探测失败 · 读数来自 ${ago(a.goodAgo)}` : `${ago(a.probedAgo)}探测失败 · 还没有成功读数`;
    return `${ago(a.probedAgo)}更新`;
  }
  // 探测失败 / 未探测的说明行（弱前景色）
  function noteHTML(a) {
    if (a.status === 'none') return '<div class="sp-note">尚未探测。发现后由持有正常登录的机器探测一次。</div>';
    if (a.status === 'fail') {
      return `<div class="sp-note">△ ${a.error}${a.goodAgo != null ? `<br>显示的是 ${ago(a.goodAgo)}的读数` : ''}</div>`;
    }
    return '';
  }
  function extraHTML(a) {
    return a.quota && a.quota.extraUsage ? `<div class="sp-line"><span>额外额度</span><span class="mono">${a.quota.extraUsage}</span></div>` : '';
  }

  // ---------------- 立即探测（mock，各页共用；完成后通知所有订阅者重绘） ----------------
  const listeners = [];
  function subscribe(fn) { listeners.push(fn); }
  function notify() { listeners.forEach((fn) => fn()); }
  function probe(a, done) {
    if (a.status === 'na') { if (done) done(a); return; } // claude：不支持额度探测
    setTimeout(() => {
      a.probedAgo = 0;
      if (!probeVia(a)) {
        // 没有正常登录：探测不了，按失败退避
        a.status = 'fail'; a.failStreak += 1; a.nextIn = Math.min(30e3 * 2 ** a.failStreak, 15 * M);
      } else if (a.status === 'none') {
        a.status = 'ok'; a.planType = a.planType || 'Plus'; a.goodAgo = 0; a.nextIn = a.interval * M;
        a.quota = { windows: [
          { key: 'session', label: '会话 5h', used: 6, resetAt: Date.now() + 4 * H },
          { key: 'weekly', label: '每周', used: 2, resetAt: Date.now() + 6 * D },
        ] };
      } else if (a.status === 'fail') {
        a.failStreak += 1; a.nextIn = Math.min(30e3 * 2 ** a.failStreak, 15 * M);
      } else {
        a.goodAgo = 0; a.nextIn = a.interval * M;
      }
      if (done) done(a);
      notify();
    }, 900);
  }

  // ---------------- 「使用量」二级面板 ----------------
  function sidePanelHTML(a) {
    const k = KINDS[a.kind];
    const refs = carriersOf(a.name);
    return `
      <button type="button" class="sp-back">‹ 使用量</button>
      <div class="sp-head"><span class="sp-name mono">${a.name}</span><span class="q-tag">${k.name}</span>${plan(a)}</div>
      <div class="sp-sub">${updatedText(a)}</div>
      ${winsFull(a)}
      ${extraHTML(a)}
      ${noteHTML(a)}
      <div class="sp-line"><span>登录</span><span>${a.logins.length} 条 · ${machinesText(a)}</span></div>
      <div class="sp-sec"><div class="sp-sec-t">使用此账号的载体</div><div class="mono">${refs.map((c) => c.name).join('、') || '<span class="dim">无</span>'}</div></div>
      <button type="button" class="sp-act" data-probe="${a.name}">立即探测</button>
      <button type="button" class="sp-act" data-manage="${a.name}">管理账号…<span>›</span></button>`;
  }

  // ---------------- 底栏 + 使用量弹层 ----------------
  function mountStatusBar(opts) {
    opts = opts || {};
    const sb = document.createElement('footer');
    sb.className = 'statusbar';
    sb.title = '使用量';
    sb.innerHTML = `
      <span class="sb-lead">已用</span>
      <div class="sb-segs"></div>
      <span class="sb-more" hidden></span>
      <button type="button" class="sb-refresh" title="刷新所有账号">${REFRESH_SVG}</button>`;
    document.body.appendChild(sb);

    const pop = document.createElement('div');
    pop.className = 'up-pop';
    pop.hidden = true;
    pop.innerHTML = `
      <div class="up-main">
        <div class="up-head"><span class="up-title">使用量</span><button type="button" class="up-all" title="立即探测所有账号">所有账号 <span class="up-ic">${REFRESH_SVG}</span></button></div>
        <div class="up-seg" role="tablist"><button type="button" data-mode="detail">详细</button><button type="button" data-mode="compact">精简</button></div>
        <div class="up-list"></div>
        <div class="up-menu">
          <a href="${ROOT}pages/accounts.html">管理账号…<span>›</span></a>
        </div>
      </div>
      <div class="up-side" hidden></div>`;
    document.body.appendChild(pop);

    const segsEl = sb.querySelector('.sb-segs');
    const moreEl = sb.querySelector('.sb-more');
    const listEl = pop.querySelector('.up-list');
    const sideEl = pop.querySelector('.up-side');
    let current = '';
    // 详细 / 精简：记住选择（per-viewer 便利项，存储不可用时用缺省「详细」）；底栏随模式变
    let mode = 'detail';
    try { mode = localStorage.getItem('b453.usage.mode') === 'compact' ? 'compact' : 'detail'; } catch (e) { /* 忽略 */ }

    function go(name) {
      if (opts.onPick) { opts.onPick(name); close(); return; }
      location.href = `${ROOT}pages/accounts.html?account=${encodeURIComponent(name)}`;
    }
    function open(name) {
      pop.hidden = false;
      renderList();
      if (name) select(name); else { sideEl.hidden = true; current = ''; }
    }
    function close() { pop.hidden = true; sideEl.hidden = true; current = ''; }
    function select(name) {
      current = name;
      listEl.querySelectorAll('.up-row').forEach((r) => r.classList.toggle('on', r.dataset.acct === name));
      renderSide();
    }
    function renderSide() {
      const a = current && find(current);
      if (!a) { sideEl.hidden = true; return; }
      sideEl.hidden = false;
      sideEl.innerHTML = sidePanelHTML(a);
      alignSide();
      sideEl.querySelector('.sp-back').addEventListener('click', () => { current = ''; sideEl.hidden = true; renderList(); });
      sideEl.querySelector('[data-manage]').addEventListener('click', () => go(a.name));
      sideEl.querySelector('[data-probe]').addEventListener('click', (e) => {
        e.currentTarget.disabled = true; e.currentTarget.textContent = '探测中…';
        probe(a);
      });
    }
    // 二级面板顶部对齐被选中的行，放不下时整体上移，不出视口、不压列表（面板在列表右侧）
    function alignSide() {
      sideEl.style.top = '';
      if (!window.matchMedia('(min-width: 681px)').matches) return; // 窄屏：面板盖在列表上，带返回
      const row = listEl.querySelector(`.up-row[data-acct="${current}"]`);
      if (!row) return;
      const popTop = pop.getBoundingClientRect().top;
      const floor = window.innerHeight - sb.offsetHeight - 6;
      const top = Math.max(8, Math.min(row.getBoundingClientRect().top, floor - sideEl.offsetHeight));
      sideEl.style.top = (top - popTop) + 'px';
      sideEl.style.bottom = 'auto';
    }
    // 列表：按类型分组——组标题（图标 + 组名）是第一层，账号行（无图标、文字与组名左对齐）是第二层；组内问题账号排前。
    // 精简：一行，右侧每周「倒计时 已用%」或问题短语；详细：两行（首行注记 + 全部窗口横排）
    function rowHTML(a) {
      const on = a.name === current ? ' on' : '';
      if (mode === 'compact') return `
        <button type="button" class="up-row${on}" data-acct="${a.name}">
          <span class="up-r1"><span class="up-name">${a.name}</span><span class="up-mach">${machinesText(a)}</span><span class="up-val">${summary(a, 'tail')}</span><span class="up-chev">›</span></span>
        </button>`;
      const r2 = winsInline(a);
      return `
        <button type="button" class="up-row d${on}" data-acct="${a.name}">
          <span class="up-r1"><span class="up-name">${a.name}</span><span class="up-mach">${machinesText(a)}</span><span class="up-note">${detailNote(a)}</span><span class="up-chev">›</span></span>
          ${r2 ? `<span class="up-r2">${r2}</span>` : ''}
        </button>`;
    }
    function renderList() {
      pop.querySelectorAll('.up-seg button').forEach((b) => b.classList.toggle('on', b.dataset.mode === mode));
      listEl.className = 'up-list ' + mode;
      listEl.innerHTML = Object.entries(KINDS).map(([key, k]) => {
        const list = byProblem(quotaAccounts().filter((a) => a.kind === key));
        if (!list.length) return '';
        return `<div class="up-group">${icon(key)}${k.name}</div>` + list.map(rowHTML).join('');
      }).join('');
      listEl.querySelectorAll('.up-row').forEach((r) => {
        r.addEventListener('mouseenter', () => { if (window.matchMedia('(hover: hover)').matches) select(r.dataset.acct); });
        r.addEventListener('focus', () => select(r.dataset.acct));
        r.addEventListener('click', () => select(r.dataset.acct));
      });
    }
    function probeAll(btn) {
      btn.classList.add('spinning');
      const qs = quotaAccounts();
      let left = qs.length;
      qs.forEach((a) => probe(a, () => { if (--left === 0) btn.classList.remove('spinning'); }));
    }
    pop.querySelector('.up-all').addEventListener('click', (e) => probeAll(e.currentTarget));
    pop.querySelectorAll('.up-seg button').forEach((b) => b.addEventListener('click', () => {
      mode = b.dataset.mode;
      try { localStorage.setItem('b453.usage.mode', mode); } catch (e) { /* 忽略 */ }
      renderBar(); renderList(); renderSide();
    }));

    // 底栏一行：每个账号一段「图标 名称 读数」，问题账号排前；读数随模式（详细：细条 + 两个最短窗口；精简：每周），不写状态字样
    function renderBar() {
      segsEl.innerHTML = byProblem(quotaAccounts()).map((a) => `
        <button type="button" class="sb-seg" data-acct="${a.name}" title="${a.name}">${icon(a.kind)}<span class="sb-val">${barReading(a, mode)}</span></button>`).join('');
      fit();
    }
    // 溢出：问题优先排序后从尾部（正常的）开始收进「+N」，收起来的永远是最不需要看的
    function fit() {
      const segs = [...segsEl.children];
      segs.forEach((c) => { c.hidden = false; });
      moreEl.hidden = true;
      const avail = segsEl.clientWidth;
      const gap = 12, reserve = 34;
      let used = 0, cut = -1;
      segs.forEach((c, i) => {
        used += c.offsetWidth + (i ? gap : 0);
        const last = i === segs.length - 1;
        if (cut < 0 && used > avail - (last ? 0 : reserve)) cut = i;
      });
      if (cut >= 0) {
        segs.slice(cut).forEach((c) => { c.hidden = true; });
        moreEl.hidden = false;
        moreEl.textContent = `+${segs.length - cut}`;
      }
    }

    sb.addEventListener('click', (e) => {
      if (e.target.closest('.sb-refresh')) { probeAll(e.target.closest('.sb-refresh')); return; }
      const seg = e.target.closest('.sb-seg');
      if (!pop.hidden && (!seg || seg.dataset.acct === current)) { close(); return; }
      open(seg ? seg.dataset.acct : '');
    });
    document.addEventListener('click', (e) => {
      if (!pop.hidden && !pop.contains(e.target) && !sb.contains(e.target)) close();
    });
    document.addEventListener('keydown', (e) => { if (e.key === 'Escape' && !pop.hidden) close(); });
    window.addEventListener('resize', fit);

    function render() {
      renderBar();
      if (!pop.hidden) { renderList(); renderSide(); }
    }
    subscribe(render);
    renderBar();
    return { render, open };
  }

  // 每 30s 刷一次页面上所有倒计时（data-reset 存绝对时刻，data-tpl 是文案模板）
  setInterval(() => {
    document.querySelectorAll('[data-reset]').forEach((el) => {
      const tpl = el.dataset.tpl || '{}';
      el.textContent = tpl.replace('{}', countdown(Number(el.dataset.reset) - Date.now()));
    });
  }, 30 * 1000);

  window.HandoffAccounts = {
    ROOT, NOW, KINDS, AGENTS, STATUS, LOGIN_STATE, MACHINES, LOCAL, accounts, carriers, discovery,
    DEFAULT_INTERVAL, DEFAULT_WARN, DEFAULT_LOW, STALE_MS,
    countdown, clock, ago, find, findById, carriersOf, quotaAccounts, byProblem, resetOf, dispatchHint,
    NO_LOGIN, ignored, ignore, restore, loginById, accountOfCarrier, carriersOfLogin, homeText, machinesOf, machinesText, probeVia, kindsOf, loginsFor, loginCommand, loginCommandText, cliOfKind, credTimeText,
    winTone, toneCls, isStale, nextProbeText, hotWindow, tightest,
    icon, plan, resetSpan, pct, statusText, summary, bar, winsCompact, winsFull, weekly, shortLabel, tinyLabel, earliestReset, detailNote, winsInline, barReading,
    updatedText, noteHTML, extraHTML, sidePanelHTML,
    probe, subscribe, notify, mountStatusBar,
  };
})();
