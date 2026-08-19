// 更新数据流的 React hooks。
//
// 职责：用统一的 usePoll 暴露薄壳状态、最新版本与下载进度三条数据流。
// 边界：不做版本判断、不决定展示文案；提示框与设置页消费这里的原始状态。
import { fetchDesktopState, fetchDownloadState, fetchLatest } from '../../api/client'
import type { DesktopState, DownloadState, LatestResp } from '../../api/types'
import { usePoll, type PollState } from './usePoll'

const UPDATE_INTERVAL = 10000
const DOWNLOAD_INTERVAL = 1000

// useDesktopState 每 10s 读取薄壳状态；没有薄壳时 data 为 null。
export function useDesktopState(): PollState<DesktopState | null> {
  return usePoll(fetchDesktopState, UPDATE_INTERVAL)
}

// useLatest 每 10s 读取最新 release 缓存。
export function useLatest(): PollState<LatestResp> {
  return usePoll(fetchLatest, UPDATE_INTERVAL)
}

// useDownload 在下载相关 UI 活跃时每 1s 读取进度；不活跃时完全停表。
export function useDownload(active: boolean): PollState<DownloadState> {
  return usePoll(fetchDownloadState, DOWNLOAD_INTERVAL, { enabled: active })
}
