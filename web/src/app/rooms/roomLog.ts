// roomLog —— 房间面板的结构化日志边界。
//
// 职责：统一记录房间面板入口、请求和错误分支，便于排查轮询与写操作。
// 边界：字段只接受房间 id、视图、请求和错误等上下文；绝不写入消息正文。

export type RoomLogLevel = 'debug' | 'warn' | 'error'

/**
 * 记录房间面板事件。
 * 参数 fields 只能放诊断上下文，不应包含消息正文或用户输入。
 */
export function logRoom(
  level: RoomLogLevel,
  event: string,
  fields: Record<string, unknown> = {},
): void {
  const payload = { subsystem: 'rooms', event, ...fields }
  if (level === 'error') console.error(payload)
  else if (level === 'warn') console.warn(payload)
  else console.debug(payload)
}
