# README 双语化：英文主、中文副

> 状态：设计完成，待实现
> 来源：08-14 brainstorm——README 全中文，海外访客第一眼看不懂

---

## 1. 问题

`README.md` 392 行，全中文。项目对外发布（`https://handoff.gosuper.dev/install`），
GitHub 上默认打开的就是这份文件，非中文读者第一眼即劝退。

同仓的 `CONTRIBUTING.md` / `SECURITY.md` / `CHANGELOG.md` 也是中文，但它们只有在读者
**已经决定参与**之后才会被点开。README 是唯一的第一接触面，性价比最高，本轮只做它。

## 2. 目标与非目标

### 2.1 目标

- GitHub 默认展示英文 README，中文原文完整保留、可一键切换
- 两份内容对等：章节、顺序、示例、命令一一对应
- 建立一条最轻的防漂移约定，避免半年后两份说的不是一回事

### 2.2 非目标（明确不做）

- **CLI 界面英文化**。`handoff init`、`--help`、错误提示当前全是中文（例：
  `cmd/version.go:35` `Short: "打印本二进制的版本标识"`）。英文读者照 README 装完，
  第一条命令就撞中文界面——这是真实的体验断层，但涉及数十个文件且需引入 i18n 机制，
  属独立项目，另记 backlog，不在本轮。
- **CONTRIBUTING / SECURITY / CHANGELOG 双语化**。看 README 英文版的反馈再定。
- **CI 校验两份是否同步**。见 §3.5。

## 3. 设计

### 3.1 文件布局

| 文件 | 语言 | 来源 |
|------|------|------|
| `README.md` | 英文 | 由中文原文翻译而来 |
| `README.zh-CN.md` | 中文 | 现有 `README.md` 原文 |

实现顺序必须是：先 `git mv README.md README.zh-CN.md`（保住 392 行的修改历史），
再新建英文 `README.md`。**不要**直接覆写 `README.md` 后另存中文副本，那会让中文原文
在 git 历史里凭空断掉。

命名用 `.zh-CN` 而非 `_zh` / `.cn`，与 Vite、Vue 等项目一致，是生态里最通用的写法。

### 3.2 语言切换条

两份文件的一级标题正下方，位置一致，格式一致：

```markdown
# handoff

[English](README.md) | [简体中文](README.zh-CN.md)
```

置于 `# handoff` 与 License badge 之间。当前文件行首三行是标题、空行、badge，
切换条插在 badge 之前。

### 3.3 翻译原则

**意译，不直译。** 现有文风有鲜明个性——「断网不丢现场」「合盖断网」「你批一条它才动
一条」「笔记本上写计划，派发到常开机的工作站执行」。逐字翻译会碾成一堆无人阅读的被动
语态。英文版按英文技术文案习惯重写，保留同等的干脆与具体。

**逐字不动的内容**（翻译时视为不可变字面量）：

- 全部 shell / PowerShell 命令与其参数：`handoff dispatch --target mac-02 --new-worktree`
- 配置键与路径：`~/.handoff/config.yaml`、`repo_root`、`reserve_ratio`、
  `%LOCALAPPDATA%\Programs\handoff\handoff.exe`
- 任务状态与事件名：`waiting_review`、`running`、`permission_request`
- 产品名与专有名词：`handoff`、`agentd`、`opencode`、`Claude Code`、`grok`、`codex`、
  `launchd`、`systemd`、`Tailscale`、`WSL2`
- 版本号、URL、backlog 编号（如 `backlog B37`）

**ASCII 流程图**（`README.md:9-17` 的代码块）：框内中文换英文，**框线与箭头需按新文本
重新对齐**，不能只替换文字留下错位的竖线。

**章节对应**：英文版章节顺序与中文版完全一致，共 16 个二级标题：
安装 / 快速开始 / 连接远程执行机 / 远程执行机 / 命令速查 / 任务状态与事件 / 配置参考 /
各 executor 须知 / 升级 / 会话恢复 / Troubleshooting / 卸载 / 即将推出 / 文档 /
友情链接 / License。日后对照修改时可按序号逐节比对。

### 3.4 链接修正

- `CONTRIBUTING.md:4` 的 `[README](README.md)` → 改指 `README.zh-CN.md`。该文档是中文
  的，读者也是中文的，链到英文版是倒退。
- 英文 `README.md` 中指向 `CONTRIBUTING.md` / `SECURITY.md` / `CHANGELOG.md` 的链接
  保留，但在链接文字后标注 `(Chinese)`，避免英文读者点进去扑空。
- 中文 `README.zh-CN.md` 内部指向其他文档的相对链接保持原样，无需改动。

### 3.5 防漂移

在 `CONTRIBUTING.md` 增加一条约定（中文，与该文档一致）：**改动 README 的 PR 必须同时
更新 `README.md` 与 `README.zh-CN.md` 两份，先改哪一份不限。**

不上 CI 校验。可行的自动校验（比对二级标题数量 / 代码块数量）都很脆，正常改动会频繁误
报，最终结果是被 `--no-verify` 绕过或直接删掉；当前贡献者规模也撑不起这层机制。真出现
漂移再补，不预先支付。

## 4. 验收标准

- [ ] `git log --follow README.zh-CN.md` 能看到原 README 的完整历史
- [ ] 两份文件的二级标题数量与顺序一致（16 个，逐一对应）
- [ ] 两份文件顶部都有语言切换条，且互相链接可点通
- [ ] 英文版所有 shell / PowerShell 代码块与中文版逐字节相同（除注释文字）
- [ ] 英文版 ASCII 流程图框线对齐无错位
- [ ] `CONTRIBUTING.md` 的 README 链接指向 `README.zh-CN.md`，且含双份同步约定
- [ ] 全仓 `grep -rn "README.md"` 无指向已迁移路径的失效引用
- [ ] 英文版通篇无残留中文字符（`grep -P '[\x{4e00}-\x{9fff}]' README.md` 无输出）

## 5. 风险

**翻译质量是本轮唯一的实质风险。** 392 行技术文案的意译不是机械劳动，交给不擅长英文
技术写作的执行者会产出「语法正确但没人愿意读」的结果，而这种劣化在验收清单里查不出来
（上面 8 条全部能过）。建议由协调者本人翻译或亲自逐节通读，不做无人复核的派发。
