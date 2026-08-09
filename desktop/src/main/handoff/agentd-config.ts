/**
 * Handoff agentd-config：从本机 ~/.handoff/config.yaml 读取控制面连接信息。
 *
 * 职责：
 *   - 读取本机 agentd 的 listen 地址与 token
 *   - 只暴露 listen/token/unavailable 三个字段
 *
 * 边界：
 *   - 不解析/暴露 targets.*.token（远端 secret 永不进入 renderer）
 *   - 文件缺失返回 unavailable=true（桌面提示用户启动 agentd）
 *   - 测试用注入路径，不直接污染真实 ~/.handoff
 */
import { existsSync, readFileSync } from 'node:fs'

export type AgentdConfig = {
  /** agentd 监听地址（如 127.0.0.1:7777）；文件缺失时为空 */
  listen: string
  /** 本机 agentd 访问 token；文件缺失时为空 */
  token: string
  /** 配置不可读（文件缺失/解析失败）时为 true */
  unavailable: boolean
}

/**
 * 读取 agentd 配置。
 * @param path 配置文件路径（测试注入；生产为 DefaultAgentdConfigPath）
 */
export function readAgentdConfig(path: string): AgentdConfig {
  if (!existsSync(path)) {
    return { listen: '', token: '', unavailable: true }
  }
  const raw = readFileSync(path, 'utf8')
  const listen = /^listen:\s*"?([^"\s]+)"?/m.exec(raw)?.[1] ?? ''
  const token = /^token:\s*"?([^"\s]+)"?/m.exec(raw)?.[1] ?? ''
  if (!listen || !token) {
    return { listen, token, unavailable: true }
  }
  return { listen, token, unavailable: false }
}

/** 默认 agentd 配置路径（~/.handoff/config.yaml）。 */
export const DefaultAgentdConfigPath = `${process.env.HOME ?? ''}/.handoff/config.yaml`
