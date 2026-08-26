// 会话列表页（spec §7 注意力面第一件：扁平活动排序列表）。
//
// 职责：
//   - 轮询 GET /api/rooms 全量，渲染 RoomSummary 扁平列表
//   - 项目筛选（客户端过滤，CardsPage 同构；端点仍支持 ?project=，见 rooms.ts fetchRooms）
//   - live / read_only / kind 徽标；bound_session 原样展示（澄清一：不透明载体标识，
//     不做任何格式假设）
//
// 边界：
//   - 排序是服务端契约（ListRooms LastActivity 降序），本组件不二次排序
//   - 错误态与空列表是两个东西：usePoll 断线时展示错误横幅，不渲染成空列表（已知陷阱四）
//   - 布局/滚动/长列表性能进真机清单，jsdom 断言只锁数据流与交互语义
import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { fetchRooms } from '../../api/rooms'
import type { RoomSummary } from '../../api/rooms'
import { usePoll } from '../data/usePoll'
import { formatRelative } from '../lib/format'
import { COLLAB_POLL_MS } from './constants'

// KIND_LABEL 房间类型 → 中文标签；未知 kind 原样显示（词表演进时降级不炸）。
const KIND_LABEL: Record<string, string> = {
  card: '卡会话',
  project: '项目群',
  global: '全员群',
}

function RoomRow({ room }: { room: RoomSummary }) {
  const navigate = useNavigate()
  return (
    <button
      type="button"
      onClick={() => navigate(`/rooms/${encodeURIComponent(room.id)}`)}
      className="w-full rounded-md border px-3 py-2 text-left hover:bg-accent/60"
    >
      <div className="flex flex-wrap items-center gap-2 text-sm">
        <span className="font-medium">{room.title}</span>
        <span className="rounded-full border px-1.5 py-0.5 text-[10px] text-muted-foreground">
          {KIND_LABEL[room.kind] ?? room.kind}
        </span>
        {room.live && <span className="text-[10px] text-green-600">在线</span>}
        {room.read_only && <span className="text-[10px] text-amber-700">只读</span>}
      </div>
      {/* bound_session 是绑定者会话标识（driver_session 投影），不透明展示，不做格式假设（澄清一） */}
      {room.bound_session !== undefined && room.bound_session !== '' && (
        <div className="mt-0.5 font-mono text-[11px] text-muted-foreground">{room.bound_session}</div>
      )}
      <div className="mt-0.5 text-[11px] text-muted-foreground">{formatRelative(room.last_activity)}</div>
    </button>
  )
}

export function RoomsListPage() {
  const [project, setProject] = useState('')
  const poll = usePoll(fetchRooms, COLLAB_POLL_MS)
  // rooms 用 useMemo 包住 ?? []：让引用稳定，避免每次渲染都新建数组、
  // 下游 useMemo 依赖它在每帧失效重算（CardsPage 同构，react-hooks 警告同源）。
  const rooms = useMemo(() => poll.data ?? [], [poll.data])
  const projectOptions = useMemo(
    () => [...new Set(rooms.map((r) => r.project).filter((p): p is string => !!p))].sort(),
    [rooms],
  )
  // 项目筛选：卡房间按 project 匹配，项目群按 project:<name> 匹配（global 恒在）。
  const filtered = useMemo(
    () =>
      project === ''
        ? rooms
        : rooms.filter((r) => r.project === project || r.id === `project:${project}`),
    [rooms, project],
  )

  return (
    <main className="flex h-full min-h-0 w-full flex-col bg-background">
      <header className="flex flex-wrap items-center gap-2 border-b px-4 py-2.5">
        <span className="text-sm font-semibold">会话</span>
        <select
          aria-label="项目"
          value={project}
          onChange={(event) => setProject(event.target.value)}
          className="rounded-md border bg-background px-2 py-1 text-xs"
        >
          <option value="">全部项目</option>
          {projectOptions.map((item) => (
            <option key={item} value={item}>{item}</option>
          ))}
        </select>
      </header>
      {poll.disconnected && (
        <p role="alert" className="border-b bg-amber-50 px-4 py-1.5 text-xs text-amber-800">
          会话列表加载失败：{poll.errorText}
        </p>
      )}
      <div data-testid="room-list" className="min-h-0 flex-1 space-y-1.5 overflow-y-auto p-3">
        {/* 错误态与空列表是两个东西（已知陷阱四）：断线且从未取到数据时横幅已说明
            失败，这里既不渲染（空）也不渲染「正在读取…」——（空）会误导成「确实没有
            会话」，正在读取会误导成「还在转」。断线但已有旧数据时继续渲染旧列表。 */}
        {poll.data === null ? (
          poll.disconnected ? null : <p className="text-sm text-muted-foreground">正在读取…</p>
        ) : filtered.length === 0 ? (
          <p className="text-sm text-muted-foreground">（空）</p>
        ) : (
          filtered.map((room) => <RoomRow key={room.id} room={room} />)
        )}
      </div>
    </main>
  )
}