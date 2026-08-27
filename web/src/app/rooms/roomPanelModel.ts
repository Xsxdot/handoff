// roomPanelModel —— RoomPanel 的纯展示投影。
//
// 职责：处理消息预览、注意力筛选、稳定排序和 attach 工作目录映射。
// 边界：不发请求、不操作 Workbench；所有外部副作用留在 RoomPanel 接缝。
import type { RoomHistoryItem, RoomMessage, RoomSummary } from '../../api/rooms'
import type { BaseDir } from '../workbench/useWorkbench'

export type RoomPanelView = 'list' | 'room' | 'detail'

/** 返回历史事件中的消息正文；坏载荷降级为空串。 */
export function messageBody(event: RoomHistoryItem | undefined): string {
  const payload = (event?.payload ?? {}) as Partial<RoomMessage>
  return typeof payload.body === 'string' ? payload.body : ''
}

/** 取列表预览的最后一条正文；没有正文时显示明确的空预览。 */
export function roomPreview(events: RoomHistoryItem[]): string {
  return messageBody(events[events.length - 1]) || '暂无预览'
}

/** 判断卡房间是否在收件箱中需要协调者关注。 */
export function roomNeedsReply(room: RoomSummary, needRoomIDs: ReadonlySet<string>): boolean {
  return room.kind === 'card' && needRoomIDs.has(room.id)
}

/** 按项目和需要你筛选房间；global 不受项目筛选影响。 */
export function visibleRooms(
  rooms: RoomSummary[],
  project: string,
  needsOnly: boolean,
  needRoomIDs: ReadonlySet<string>,
): RoomSummary[] {
  return rooms.filter((room) => {
    const projectMatch = project === '' || room.kind === 'global' || room.project === project
    return projectMatch && (!needsOnly || roomNeedsReply(room, needRoomIDs))
  })
}

/** 将需要你置顶，同时保持服务端给出的其余活动顺序。 */
export function orderRooms(rooms: RoomSummary[], needRoomIDs: ReadonlySet<string>): RoomSummary[] {
  return [...rooms].sort(
    (left, right) => Number(roomNeedsReply(right, needRoomIDs)) - Number(roomNeedsReply(left, needRoomIDs)),
  )
}

/** 为房间生成稳定的圆形头像文字。 */
export function roomInitials(room: RoomSummary): string {
  if (room.kind === 'global') return '全'
  if (room.kind === 'project') return room.project?.slice(0, 2) || '项'
  return room.id.slice(0, 3)
}

/** 把服务端 attach 投影转成既有 Workbench 的基准目录；无投影返回 null。 */
export function attachBase(room: RoomSummary): BaseDir | null {
  if (!room.attach) return null
  const { target, work_dir: path } = room.attach
  const label = path.split('/').filter(Boolean).pop() || path
  return {
    key: 'room-attach:' + (target ?? '') + ':' + path,
    kind: 'workspace',
    path,
    label,
    projectName: room.project ?? '',
    machine: target ?? '',
  }
}
