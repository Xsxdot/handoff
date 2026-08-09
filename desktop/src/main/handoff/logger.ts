/**
 * Handoff 桌面主进程结构化日志。
 *
 * 职责：
 *   - 提供 HandoffLogger：按行 JSONL 写到 userData/logs/handoff-desktop.log
 *   - 字段值先 redaction（token/secret 等敏感字段脱敏）
 *
 * 边界：
 *   - 新 Handoff 代码不得用 console.log 作为日志
 *   - token、env value、回答全文、文件内容一律不落盘
 */
import { appendFileSync, mkdirSync } from 'node:fs'
import { dirname } from 'node:path'
import { randomBytes } from 'node:crypto'

export type HandoffLoggerOptions = {
  /** 日志目录（userData/logs 的注入点；测试传临时目录） */
  dir: string
  /** 需要脱敏的字段名（值会被替换为 <redacted>） */
  redactFields: string[]
}

/** 生成随机会话标识（用于日志关联）。 */
function randomToken(): string {
  return randomBytes(16).toString('hex')
}

/**
 * HandoffLogger：按行 JSON 结构化日志。
 * 字段值先 redaction（redactFields 命中即替换为 <redacted>）。
 */
export class HandoffLogger {
  private readonly filePath: string
  private readonly redactFields: Set<string>

  constructor(options: HandoffLoggerOptions) {
    this.filePath = `${options.dir}/handoff-desktop.log`
    this.redactFields = new Set(options.redactFields)
    mkdirSync(dirname(this.filePath), { recursive: true })
  }

  /**
   * 写一条日志。
   * @param level 级别（info/warn/error/debug）
   * @param message 结构化消息
   * @param fields 键值字段（键命中 redactFields 时值脱敏）
   */
  log(level: string, message: string, fields: Record<string, unknown> = {}): void {
    const safe: Record<string, unknown> = { ts: new Date().toISOString(), level, message }
    for (const [k, v] of Object.entries(fields)) {
      safe[k] = this.redactFields.has(k) ? '<redacted>' : v
    }
    try {
      appendFileSync(this.filePath, `${JSON.stringify(safe)}\n`, 'utf8')
    } catch {
      // 日志写失败不影响主流程（避免日志自身成为故障点）
    }
  }

  info(message: string, fields?: Record<string, unknown>): void {
    this.log('info', message, fields)
  }
  warn(message: string, fields?: Record<string, unknown>): void {
    this.log('warn', message, fields)
  }
  error(message: string, fields?: Record<string, unknown>): void {
    this.log('error', message, fields)
  }
  debug(message: string, fields?: Record<string, unknown>): void {
    this.log('debug', message, fields)
  }
}

/** 默认 redaction 字段：token 类敏感字段一律脱敏。 */
export const defaultRedactFields = ['token', 'secret', 'authorization', 'answer', 'content']

/** 生成随机会话标识（用于日志关联）。 */
export function newSessionToken(): string {
  return randomToken()
}
