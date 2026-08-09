# Orca Upstream Snapshot

`desktop/` 是本仓库对官方 Orca Desktop 的固定上游源码快照。Handoff 桌面工作台以
Electron 壳和可提取的渲染能力复用该快照；对 Orca 的修改遵循设计规格
（`docs/superpowers/specs/2026-08-09-handoff-desktop-vertical-slice-design.md`）
的「独立 Handoff Workbench 边界」约束，不接入 Orca SSH / 旧 Project/Worktree 持久化。

## 来源契约

- 官方 URL: `https://github.com/stablyai/orca`
- License: **MIT**（见 `desktop/LICENSE`）
- Annotated tag: `v1.4.177-rc.0`
- Tag object: `ff48a6d33b7bde5d37ccc367dc5aa1103d2a8ee4`
- Peeled source commit: `9e948fbdf462ede3c0160c719474100fc5cbefb7`
- 导入日期: `2026-08-09`

## 同步方法

每次同步使用相同命令，保证可复现：

```bash
ORCA_IMPORT_DIR="$(mktemp -d /tmp/handoff-orca-import.XXXXXX)"
git clone --depth 1 --branch v1.4.177-rc.0 https://github.com/stablyai/orca "$ORCA_IMPORT_DIR/orca"
test "$(git -C "$ORCA_IMPORT_DIR/orca" rev-parse HEAD)" = 9e948fbdf462ede3c0160c719474100fc5cbefb7
mkdir -p desktop
rsync -a --exclude .git "$ORCA_IMPORT_DIR/orca/" desktop/
```

要点：

- 固定 annotated tag，并用 `rev-parse HEAD` 断言 peeled commit 一致，杜绝「看起来
  是同一 tag、内容却漂移」的隐患。
- 用 `rsync -a --exclude .git` 平铺复制，**不保留嵌套 `.git`**——`desktop/` 必须是
  无嵌套仓库的源码树，其版本历史完全由本仓库 git 管理。
- 保留快照自身的 `.gitignore` 与 `LICENSE`。

## 边界说明

- **本地下载目录不是来源**：`/Users/xushixin/Downloads/AnyTimeDelete/orca-main` 等
  本地目录仅用于设计期阅读，不可作为基线或复制来源；一切以本文件声明的官方
  tag/commit 为准。
- 导入后对 `desktop/` 的任何修改都记录在本仓库 commit 中，不再回推上游。
- 上游 `desktop/package.json`/lockfile 由 Task 8–10 使用，作为桌面工程基线。
