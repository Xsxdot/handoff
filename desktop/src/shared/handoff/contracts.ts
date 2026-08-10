/**
 * Handoff 桌面控制面协议契约（与 internal/desktopapi 的 Go wire DTO 一一对应）。
 *
 * 职责：
 *   - 定义 BootstrapResponse / ControlEventEnvelope / Problem / Operation 等
 *     Zod schema 与推导类型
 *   - 供 Main 网络响应解析、preload IPC 类型与 renderer CatalogStore 共用
 *
 * 边界：
 *   - 与 internal/desktopapi/testdata/*.json golden 保持同步（Go/TS 契约漂移测试）
 *   - 不承载业务逻辑；字段用 snake_case 与 Go 侧一致
 */
import { z } from 'zod'

// ---- 基础枚举（与 controlplane 一致） ----

export const machineKindSchema = z.enum(['local', 'remote'])
export const machineStatusSchema = z.enum([
  'connecting',
  'reconciling',
  'connected',
  'unavailable',
  'incompatible'
])
export const locationRoleSchema = z.enum(['local', 'remote'])
export const locationSourceSchema = z.enum(['existing_path', 'git_clone'])
export const workspaceKindSchema = z.enum(['main', 'worktree', 'detached'])
export const availabilitySchema = z.enum(['available', 'unavailable'])
export const operationStateSchema = z.enum([
  'pending',
  'running',
  'partial',
  'succeeded',
  'failed'
])
export const taskSummaryStateSchema = z.enum([
  'pending',
  'running',
  'waiting_answer',
  'waiting_review',
  'completed',
  'failed',
  'stalled'
])

// ---- 资源行 ----

export const machineSchema = z.object({
  id: z.string(),
  display_name: z.string(),
  kind: machineKindSchema,
  endpoint: z.string(),
  protocol_version: z.number().int(),
  capabilities: z.record(z.string(), z.number().int()),
  status: machineStatusSchema,
  last_seen_at: z.string().datetime().nullable().optional()
})
export type Machine = z.infer<typeof machineSchema>

export const projectSchema = z.object({
  id: z.string(),
  name: z.string(),
  git_identity: z.string().optional(),
  created_at: z.string().datetime(),
  updated_at: z.string().datetime()
})
export type Project = z.infer<typeof projectSchema>

export const projectLocationSchema = z.object({
  id: z.string(),
  project_id: z.string(),
  machine_id: z.string(),
  role: locationRoleSchema,
  main_workspace_id: z.string(),
  source: locationSourceSchema,
  git_url: z.string().optional(),
  created_at: z.string().datetime(),
  updated_at: z.string().datetime()
})
export type ProjectLocation = z.infer<typeof projectLocationSchema>

export const workspaceSchema = z.object({
  id: z.string(),
  machine_id: z.string(),
  location_id: z.string().nullable().optional(),
  kind: workspaceKindSchema,
  path: z.string(),
  canonical_path: z.string(),
  repo_identity: z.string().optional(),
  git_common_dir: z.string().optional(),
  branch: z.string().optional(),
  head_oid: z.string().optional(),
  availability: availabilitySchema,
  last_scanned_at: z.string().datetime()
})
export type Workspace = z.infer<typeof workspaceSchema>

export const gitRefSchema = z.object({
  location_id: z.string(),
  name: z.string(),
  head_oid: z.string(),
  checked_out_workspace_ids: z.array(z.string())
})
export type GitRef = z.infer<typeof gitRefSchema>

export const taskSummarySchema = z.object({
  task_id: z.string(),
  machine_id: z.string(),
  workspace_id: z.string(),
  name: z.string(),
  executor: z.string(),
  state: taskSummaryStateSchema,
  attention: z.number().int(),
  updated_at: z.string().datetime()
})
export type TaskSummary = z.infer<typeof taskSummarySchema>

export const operationResultSchema = z.object({
  workspace_id: z.string(),
  location_id: z.string(),
  path: z.string()
})
export type OperationResult = z.infer<typeof operationResultSchema>

export const operationErrorSchema = z.object({
  code: z.string(),
  message: z.string()
})
export type OperationError = z.infer<typeof operationErrorSchema>

export const operationTargetSchema = z.object({
  target_id: z.string(),
  machine_id: z.string(),
  state: operationStateSchema,
  result: operationResultSchema.optional(),
  error: operationErrorSchema.optional()
})
export type OperationTarget = z.infer<typeof operationTargetSchema>

export const operationSchema = z.object({
  operation_id: z.string(),
  kind: z.string(),
  state: operationStateSchema,
  project_id: z.string().optional(),
  targets: z.array(operationTargetSchema),
  progress: z.string().optional(),
  created_at: z.string().datetime(),
  updated_at: z.string().datetime()
})
export type Operation = z.infer<typeof operationSchema>

// ---- 顶层响应 ----

export const bootstrapResponseSchema = z.object({
  machines: z.array(machineSchema),
  projects: z.array(projectSchema),
  locations: z.array(projectLocationSchema),
  workspaces: z.array(workspaceSchema),
  git_refs: z.array(gitRefSchema),
  active_task_summaries: z.array(taskSummarySchema),
  operations: z.array(operationSchema),
  control_revision: z.number().int()
})
export type BootstrapResponse = z.infer<typeof bootstrapResponseSchema>

export const controlEventEnvelopeSchema = z.object({
  revision: z.number().int(),
  kind: z.string(),
  resource_id: z.string(),
  payload: z.unknown(),
  created_at: z.string().datetime()
})
export type ControlEventEnvelope = z.infer<typeof controlEventEnvelopeSchema>

export const problemSchema = z.object({
  code: z.string(),
  message: z.string(),
  retryable: z.boolean(),
  machine_id: z.string().optional(),
  workspace_id: z.string().optional(),
  task_id: z.string().optional(),
  operation_id: z.string().optional(),
  details: z.string().optional()
})
export type Problem = z.infer<typeof problemSchema>

// ---- Workspace resources ----

export const fileKindSchema = z.enum(['file', 'directory', 'symlink'])
export const fileEntrySchema = z.object({
  workspace_id: z.string(),
  path: z.string(),
  name: z.string(),
  kind: fileKindSchema,
  size: z.number().int(),
  modified_at: z.string().datetime(),
  version: z.string().optional()
})
export type FileEntry = z.infer<typeof fileEntrySchema>

export const fileDocumentSchema = z.object({
  workspace_id: z.string(),
  path: z.string(),
  version: z.string(),
  content_base64: z.string(),
  size: z.number().int(),
  modified_at: z.string().datetime()
})
export type FileDocument = z.infer<typeof fileDocumentSchema>

export const fileSearchMatchSchema = z.object({
  path: z.string(),
  line: z.number().int(),
  column: z.number().int(),
  preview: z.string()
})
export const fileSearchResultSchema = z.object({
  workspace_id: z.string(),
  matches: z.array(fileSearchMatchSchema),
  truncated: z.boolean(),
  scanned_files: z.number().int(),
  scanned_bytes: z.number().int()
})
export type FileSearchResult = z.infer<typeof fileSearchResultSchema>

export const gitStatusEntrySchema = z.object({
  path: z.string(),
  original_path: z.string().optional(),
  index_status: z.string(),
  worktree_status: z.string()
})
export const gitStatusSnapshotSchema = z.object({
  workspace_id: z.string(),
  branch: z.string().optional(),
  head_oid: z.string().optional(),
  upstream: z.string().optional(),
  ahead: z.number().int(),
  behind: z.number().int(),
  entries: z.array(gitStatusEntrySchema)
})
export type GitStatusSnapshot = z.infer<typeof gitStatusSnapshotSchema>

export const ptyStateSchema = z.enum(['starting', 'active', 'ended'])
export const ptySessionSchema = z.object({
  terminal_session_id: z.string(),
  incarnation: z.string(),
  workspace_id: z.string(),
  state: ptyStateSchema,
  shell: z.string(),
  through_seq: z.number().int(),
  exit_code: z.number().int().nullable().optional()
})
export type PtySession = z.infer<typeof ptySessionSchema>

export const ptyFrameKindSchema = z.enum([
  'subscribed',
  'snapshot',
  'data',
  'status',
  'exit',
  'problem'
])
export const ptyServerFrameSchema = z.object({
  version: z.number().int(),
  kind: ptyFrameKindSchema,
  terminal_session_id: z.string(),
  incarnation: z.string(),
  seq: z.number().int(),
  through_seq: z.number().int(),
  data_base64: z.string().optional(),
  state: ptyStateSchema.optional(),
  exit_code: z.number().int().optional(),
  problem: problemSchema.optional()
})
export type PtyServerFrame = z.infer<typeof ptyServerFrameSchema>

export const previewStateSchema = z.enum(['pending', 'active', 'closed', 'expired'])
export const previewSessionSchema = z.object({
  preview_session_id: z.string(),
  workspace_id: z.string(),
  machine_id: z.string(),
  state: previewStateSchema,
  url: z.string().url(),
  port: z.number().int(),
  expires_at: z.string().datetime()
})
export type PreviewSession = z.infer<typeof previewSessionSchema>

// ---- 项目创建请求 ----

export const createProjectLocationReqSchema = z.object({
  machine_id: z.string(),
  role: locationRoleSchema,
  source: locationSourceSchema,
  path: z.string().optional(),
  git_url: z.string().optional(),
  clone_path: z.string().optional()
})
export type CreateProjectLocationReq = z.infer<typeof createProjectLocationReqSchema>

export const createProjectRequestSchema = z.object({
  operation_id: z.string(),
  name: z.string(),
  locations: z.array(createProjectLocationReqSchema)
})
export type CreateProjectRequest = z.infer<typeof createProjectRequestSchema>

// CreateProjectCommand 与 Go controlplane.CreateProjectCommand 对应（Main → agentd 传递）。
export const createProjectCommandSchema = createProjectRequestSchema
export type CreateProjectCommand = z.infer<typeof createProjectCommandSchema>
