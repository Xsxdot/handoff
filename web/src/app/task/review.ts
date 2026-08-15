// review.ts —— 审批台的应答编码与校验（硬契约，vitest 钉死）。
//
// 浏览器点「批准」和 CLI 敲 `reply --approve` 是同一件事，answer 的编码是
// 与 agentd 对齐的契约（manager.go 的 gateDecision：trim 后严格等于 "allow"
// 才回 "once"，其余一律 "reject"）：
//
//   - gate 批准 → answer **必须**正好是 "allow"
//   - gate 拒绝 → "deny: <理由>"（理由会回到模型手里；无理由时 UI 不允许提交，
//     否则 executor 会原地重试同样的操作、白烧一轮）
//   - ask 提问  → 自由文本原样透传
//
// 工单 request 的展示契约：权限/提问全文只在工单里（事件 payload 的是截断摘要），
// 解析必须读工单的 request 字段。完整展示、不截断。
import type { Ticket } from '../../api/types'

// TicketAction 是审核者对一张工单的裁决动作。
export type TicketAction = 'approve' | 'deny' | 'answer'

// validateReply 校验一次应答是否可以提交。
//
// 返回：
//   - null: 可以提交
//   - 非 null: 阻止提交的原因（给用户看的提示文案）
//
// 现在唯一的拦路点是「gate 拒绝必须填理由」——理由会回到模型手里，不填它就
// 原地重试同样的操作。
export function validateReply(kind: string, action: TicketAction, reason: string): string | null {
  if (kind === 'gate' && action === 'deny' && reason.trim() === '') {
    return '拒绝必须填写理由：理由会回到模型手里，不填它就原地重试同样的操作'
  }
  return null
}

// buildTicketAnswer 按契约把裁决动作编码成 POST /api/tasks/{id}/reply 的 answer。
//
// 参数：
//   - kind: 工单 kind（gate / ask）
//   - action: 裁决动作（approve / deny / answer）
//   - reason: deny 的理由（必须非空；调用方先用 validateReply 拦）
//   - freeText: ask 的自由文本
//
// 返回：
//   - gate + approve → "allow"
//   - gate + deny   → "deny: <reason>"（reason 已 trim）
//   - ask           → freeText 原样透传
export function buildTicketAnswer(
  kind: string,
  action: TicketAction,
  reason: string,
  freeText: string,
): string {
  if (kind === 'ask') {
    return freeText
  }
  if (action === 'approve') {
    return 'allow'
  }
  if (action === 'deny') {
    return `deny: ${reason.trim()}`
  }
  return freeText
}

// ParsedTicketRequest 是工单 request 的可读视图：kind 与要展示的全文文本。
export interface ParsedTicketRequest {
  kind: string
  text: string
}

// parseTicketRequest 从工单的 request 字段提取「要展示的全文」。
//
// request 在 agentd 侧是 json.RawMessage：{"kind":"gate","permission":"<全文>"}
// 或 {"kind":"ask","question":"<全文>"}。fetch 解码后 request 是对象；fixture
// 里也有 request 是别的形状的历史样本（{"cmd":…}）。
//
// 为什么是这里而不是读事件：permission_request 事件 payload 里的 permission 是
// 截断过的摘要（permEventTextLimit=200），工单里才是全文——展示必须读工单。
// 结构不符时回退 JSON 原文，绝不吞数据。
export function parseTicketRequest(ticket: Ticket): ParsedTicketRequest {
  const raw = ticket.request
  if (typeof raw === 'string') {
    // 极少数情况下 request 是 JSON 字符串（而非已解析对象），补一层 parse
    try {
      return parseTicketRequest({ ...ticket, request: JSON.parse(raw) as unknown })
    } catch {
      return { kind: ticket.kind, text: raw }
    }
  }
  if (raw !== null && typeof raw === 'object') {
    const obj = raw as Record<string, unknown>
    const kind = typeof obj.kind === 'string' && obj.kind !== '' ? obj.kind : ticket.kind
    if (typeof obj.permission === 'string' && obj.permission !== '') {
      return { kind, text: obj.permission }
    }
    if (typeof obj.question === 'string' && obj.question !== '') {
      return { kind, text: obj.question }
    }
    // 形状不符（如历史样本 {"cmd":…}）：把整个对象原样展示，不猜不丢
    return { kind, text: JSON.stringify(obj, null, 2) }
  }
  return { kind: ticket.kind, text: String(raw ?? '') }
}

// ticketKindLabel 是工单 kind 的中文展示名。
export function ticketKindLabel(kind: string): string {
  switch (kind) {
    case 'gate':
      return '权限请求'
    case 'ask':
      return '提问'
    default:
      return kind
  }
}
