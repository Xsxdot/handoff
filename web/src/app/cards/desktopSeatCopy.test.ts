// desktopSeatCopy.test.ts —— B434 卡页桌面端坐席文案金样本（契约 §4 条 194–199）。
import { describe, expect, it } from 'vitest'
import {
  DESKTOP_COPY_BRIEF_BUTTON,
  DESKTOP_COPY_BRIEF_HINT,
  DESKTOP_LAUNCHER_BUTTON,
  DESKTOP_LAUNCHER_SENT,
  DESKTOP_SEAT_BUTTON,
  DESKTOP_TRUST_HINT,
} from './desktopSeatCopy'

describe('desktopSeatCopy 文案冻结', () => {
  it('spec 实现决定的四条文案逐字', () => {
    expect(DESKTOP_SEAT_BUTTON).toBe('用 Claude Code 坐下')
    expect(DESKTOP_COPY_BRIEF_BUTTON).toBe('复制简报')
    expect(DESKTOP_COPY_BRIEF_HINT).toBe('到 Claude 桌面端新建会话后粘贴')
    expect(DESKTOP_TRUST_HINT).toBe('如弹出信任文件夹，点信任，再按发送')
  })

  it('P4 拉起器代开两条文案逐字', () => {
    expect(DESKTOP_LAUNCHER_BUTTON).toBe('让拉起器代开')
    expect(DESKTOP_LAUNCHER_SENT).toBe('已请拉起器代开，几分钟内这里会显示已绑定')
  })
})
