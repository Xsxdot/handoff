// newFile —— 「新建一个空白文件」的命名与创建（spec §4.2 / §5.2）。
//
// 职责：在一个基准目录根下挑一个不冲突的 untitled-N.md，把它建出来。
//
// 边界：
//   - 只建文件，不打开它——打开是调用方的事（中央区变成 file tab，
//     浮窗开一个 file tab，两处对同一个结果有不同的处置）
//   - 不吞错误：agentd 的中文原文原样抛出，由调用方展示
import { createWorkspaceEntry, fetchWorkspaceDir } from '../../api/client'
import type { BaseDir } from './useWorkbench'

// UNTITLED_RE 匹配 untitled-<正整数>.md，捕获编号。
const UNTITLED_RE = /^untitled-(\d+)\.md$/

// nextUntitledName 在已有条目名里挑第一个空出来的 untitled 编号。
//
// 参数：existing 该目录下的全部条目名（文件与目录都算——同名目录一样会撞）
// 返回：形如 'untitled-3.md' 的单层文件名
//
// 为什么捡中间空出来的编号而不是一直取 max+1：连着建了删、删了建几次之后，
// max+1 会爬到 untitled-47.md，而目录里其实只有一个文件。
//
// 为什么固定 .md：总得选一个，而 .md 在纯文本编辑器里无害且是记东西最常见的
// 格式。想要别的后缀可以在右栏或浮窗里改名，那是一步既有操作。
export function nextUntitledName(existing: string[]): string {
  const used = new Set<number>()
  for (const name of existing) {
    const match = UNTITLED_RE.exec(name)
    if (match !== null) used.add(Number(match[1]))
  }
  let n = 1
  while (used.has(n)) n++
  return `untitled-${n}.md`
}

// createUntitledFile 在基准目录根下建一个空白文件，返回它的相对路径。
//
// 参数：base 基准目录（工作树或草稿区都行——两者都在文件端点的白名单里）
// 返回：新文件的 rel（就是文件名，因为它建在根上）
//
// 抛出：agentd 的错误原样上抛（ApiError）。**特别是 409**：列举与创建之间
// 另一个客户端建了同名文件时会撞上。这里**不重试**——那是真实的并发冲突，
// 静默重试会掩盖「有别人在动这个目录」这个事实，用户再点一次就好。
//
// 为什么先列举再命名，而不是从 1 开始建、撞 409 就 +1：后者在已经有
// untitled-1..9 的目录里会打出 9 个 409，服务端日志里全是拒绝记录，
// 排障时看着像出了故障。
export async function createUntitledFile(base: BaseDir): Promise<string> {
  const listed = await fetchWorkspaceDir(base.path, undefined, base.machine || undefined)
  const name = nextUntitledName(listed.entries.map((entry) => entry.name))
  await createWorkspaceEntry(base.path, '', name, 'file', base.machine || undefined)
  return name
}
