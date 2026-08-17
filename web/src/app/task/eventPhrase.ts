// eventPhrase.ts —— 帧 event 类型 → 人话短语（纯函数）。
//
// 职责：会话流内联事件行（EventChip）的文案与色调。
// 边界：
//   - 输入是帧的 event 类型名（W4a 刻意冗余在帧里），不查 events 表
//   - 白名单外的类型**原样透出**，不吞——契约会演进，前端比后端旧是常态
//   - 文案沿自原 EventMark 的 EVENT_LABEL（B100 的 failed/turn_failed 区分保留）

// EventPhrase 是一条事件的展示：text 文案 + tone 色调（warn 用琥珀）。
export interface EventPhrase {
  text: string
  tone: 'info' | 'warn'
}

// PHRASES 是已知事件类型的映射。可裁决类（permission_request/question）把人
// 指向工单面板；completed/failed 等没有可裁决物，不指（指了会让人扑空——
// 原 EventMark 的 ADJUDICABLE 纪律）。
const PHRASES: Record<string, EventPhrase> = {
  permission_request: { text: '权限工单：等待裁决——入口在左栏底部的工单面板', tone: 'warn' },
  question: { text: '提问工单：等待回答——入口在左栏底部的工单面板', tone: 'warn' },
  completed: { text: '一轮结束，进入待审', tone: 'info' },
  failed: { text: '任务失败', tone: 'warn' },
  turn_failed: { text: '回合失败', tone: 'warn' },
  delivery_failed: { text: '裁决已落库但没送到 executor', tone: 'warn' },
  stalled: { text: '看门狗：长时间无产出', tone: 'warn' },
}

// eventPhrase 返回事件的人话展示；未知类型原样透出（info 色调）。
export function eventPhrase(event: string): EventPhrase {
  return PHRASES[event] ?? { text: event, tone: 'info' }
}
