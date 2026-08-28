// paneDrop.ts —— 工作台拖放边界的纯投影与序列化解析。
//
// 职责：把 DOM 坐标投影为五向 DropZone，并校验来自左树/组栏的 JSON MIME 数据。
// 边界：不访问 DOM、不调用 API、不改变 Workbench；布局迁移全部交给 tabs.ts。
import type { BaseDir } from './tabs'

export const DRAG_TASK_MIME = 'text/handoff-task'
export const DRAG_BASE_MIME = 'text/handoff-base'
export const DRAG_DIR_MIME = 'text/handoff-dir'
export const DRAG_TAB_MIME = 'text/handoff-tab'
export const DRAG_GROUP_MIME = 'text/handoff-group'

export type DropZone = 'left' | 'right' | 'top' | 'bottom' | 'center'

const EDGE_RATIO = 0.28

/** 按 28% 四向阈值投影坐标；不可加对应布局项或尺寸无效时返回 center。 */
export function dropZoneAt(
  offsetX: number,
  offsetY: number,
  width: number,
  height: number,
  canAddColumn: boolean,
  canAddPane: boolean,
): DropZone {
  if (width <= 0 || height <= 0) return 'center'
  const xEdge = width * EDGE_RATIO
  const yEdge = height * EDGE_RATIO
  if (canAddColumn && offsetX < xEdge) return 'left'
  if (canAddColumn && offsetX > width - xEdge) return 'right'
  if (canAddPane && offsetY < yEdge) return 'top'
  if (canAddPane && offsetY > height - yEdge) return 'bottom'
  return 'center'
}

function objectValue(raw: string): Record<string, unknown> | null {
  if (raw === '' || raw === 'null') return null
  try {
    const value: unknown = JSON.parse(raw)
    return typeof value === 'object' && value !== null && !Array.isArray(value)
      ? value as Record<string, unknown>
      : null
  } catch {
    return null
  }
}

function stringField(value: unknown): value is string {
  return typeof value === 'string'
}

/** 解析完整 BaseDir；任何缺字段/错类型返回 null，不抛异常。 */
export function readDragBase(raw: string): BaseDir | null {
  const value = objectValue(raw)
  if (!value || !stringField(value.key) || !stringField(value.kind) ||
      !stringField(value.path) || !stringField(value.label) ||
      !stringField(value.projectName) || !stringField(value.machine) ||
      !['workspace', 'home', 'scratch'].includes(value.kind)) return null
  return {
    key: value.key,
    kind: value.kind as BaseDir['kind'],
    path: value.path,
    label: value.label,
    projectName: value.projectName,
    machine: value.machine,
  }
}

/** 解析已有 Tab 拖源。 */
export function readDragTab(raw: string): { groupId: string; tabId: string } | null {
  const value = objectValue(raw)
  if (!value || !stringField(value.groupId) || !stringField(value.tabId) || value.groupId === '' || value.tabId === '') return null
  return { groupId: value.groupId, tabId: value.tabId }
}

/** 解析已有 group 拖源。 */
export function readDragGroup(raw: string): { groupId: string } | null {
  const value = objectValue(raw)
  if (!value || !stringField(value.groupId) || value.groupId === '') return null
  return { groupId: value.groupId }
}
