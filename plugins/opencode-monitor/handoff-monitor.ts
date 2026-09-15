// handoff-monitor.ts —— OpenCode 协调者侧的 monitor 工具（B373）。
//
// 职责：把一条 shell 命令挂到后台，stdout 每一行（短时间合并）叫醒同一会话。
// 边界：不改 opencode.json；busy 时排队，session.idle 再灌；dispose / 会话删除时杀进程。

import { appendFileSync, mkdirSync } from "node:fs"
import { homedir } from "node:os"
import { join } from "node:path"
import { spawn, type ChildProcess } from "node:child_process"
import type { Plugin } from "@opencode-ai/plugin"
import { tool } from "@opencode-ai/plugin"

const MAX_JOBS = 8
const COALESCE_MS = 250
const MAX_BATCH = 40
const DEFAULT_TIMEOUT_MS = 3 * 60 * 60 * 1000
const LOG_REL = join(".handoff", "opencode-monitor.jsonl")

type Job = {
  id: string
  sessionID: string
  command: string
  description: string
  proc: ChildProcess
  startedAt: number
  timeout: ReturnType<typeof setTimeout> | undefined
  silent: boolean
  stdoutRest: string
  stderrRest: string
}

type Wake = {
  id: string
  description: string
  line: string
  at: number
}

function log(entry: Record<string, unknown>) {
  const row = JSON.stringify({ ts: new Date().toISOString(), ...entry })
  try {
    const dir = join(homedir(), ".handoff")
    mkdirSync(dir, { recursive: true })
    appendFileSync(join(homedir(), LOG_REL), row + "\n")
  } catch {
    // 日志失败不得拖垮监视本身
  }
}

function newID() {
  return "mon_" + Math.random().toString(36).slice(2, 10)
}

function shell() {
  return process.platform === "win32"
    ? { bin: process.env.ComSpec || "cmd.exe", flag: "/c" }
    : { bin: "/bin/sh", flag: "-c" }
}

function killTree(proc: ChildProcess, signal: NodeJS.Signals) {
  if (!proc.pid) return
  if (process.platform === "win32") {
    spawn("taskkill", ["/pid", String(proc.pid), "/t", "/f"], { stdio: "ignore", windowsHide: true })
    return
  }
  try {
    process.kill(-proc.pid, signal)
  } catch {
    try {
      proc.kill(signal)
    } catch {
      // already gone
    }
  }
}

const plugin: Plugin = async (ctx) => {
  const jobs = new Map<string, Job>()
  const queues = new Map<string, Wake[]>()
  const busy = new Set<string>()
  const flushing = new Set<string>()
  const dead = new Set<string>()
  const timers = new Map<string, ReturnType<typeof setTimeout>>()

  log({ event: "plugin_loaded" })
  const app = ctx.client.app as { log?: (a: unknown) => Promise<unknown> }
  void app?.log?.({
    body: {
      service: "handoff-monitor",
      level: "info",
      message: "plugin loaded",
    },
  })

  const enqueue = (sessionID: string, wake: Wake) => {
    if (dead.has(sessionID)) return
    const q = queues.get(sessionID) ?? []
    q.push(wake)
    queues.set(sessionID, q)
    const prev = timers.get(sessionID)
    if (prev) clearTimeout(prev)
    timers.set(
      sessionID,
      setTimeout(() => {
        timers.delete(sessionID)
        void flush(sessionID)
      }, COALESCE_MS),
    )
  }

  const flush = async (sessionID: string) => {
    if (dead.has(sessionID) || busy.has(sessionID) || flushing.has(sessionID)) return
    const q = queues.get(sessionID)
    if (!q || q.length === 0) return
    flushing.add(sessionID)
    const batch = q.splice(0, MAX_BATCH)
    const text = [
      "<monitor-notification>",
      ...batch.map((w) => `[${w.id} ${w.description}] ${w.line}`),
      "</monitor-notification>",
      "These are background monitor events. Treat each JSON line as a handoff wait event if it looks like one. Do not poll. Do not restart the monitor unless it exited.",
    ].join("\n")
    let dispatched = false
    try {
      const session = ctx.client.session as {
        promptAsync?: (args: unknown) => Promise<unknown>
        prompt: (args: unknown) => Promise<unknown>
      }
      const args = {
        path: { id: sessionID },
        body: { parts: [{ type: "text", text, synthetic: true }] },
      }
      // promptAsync 派发后立刻返回，必须自己记 busy，否则下一行会叠一次 prompt。
      // 同步 prompt 会等到回合结束，回来时会话已 idle，不能记 busy。
      if (typeof session.promptAsync === "function") {
        await session.promptAsync(args)
        busy.add(sessionID)
      } else {
        await session.prompt(args)
      }
      dispatched = true
      log({ event: "woke", sessionID, n: batch.length, ids: [...new Set(batch.map((w) => w.id))] })
      void app?.log?.({
        body: {
          service: "handoff-monitor",
          level: "info",
          message: "woke session",
          extra: { sessionID, n: batch.length },
        },
      })
    } catch (err) {
      if (dead.has(sessionID)) {
        log({ event: "wake_dropped", sessionID, reason: "session_gone", n: batch.length })
      } else {
        queues.set(sessionID, [...batch, ...(queues.get(sessionID) ?? [])])
        const msg = err instanceof Error ? err.message : String(err)
        const isBusy = /busy|SessionBusy/i.test(msg)
        if (isBusy) busy.add(sessionID)
        log({ event: isBusy ? "busy_defer" : "wake_error", sessionID, err: msg, n: batch.length })
      }
    } finally {
      flushing.delete(sessionID)
      // 失败不要立刻重入：busy 等 idle，其它错误等下一行 coalesce，避免 livelock。
      if (
        dispatched &&
        !dead.has(sessionID) &&
        !busy.has(sessionID) &&
        (queues.get(sessionID) ?? []).length > 0
      ) {
        void flush(sessionID)
      }
    }
  }

  const stopJob = (id: string, reason: string) => {
    const job = jobs.get(id)
    if (!job) return false
    if (job.timeout) clearTimeout(job.timeout)
    // 会话没了或插件卸载时再灌 prompt 只会失败；自然退出/kill/超时仍要叫醒。
    job.silent = reason === "session_deleted" || reason === "dispose"
    try {
      killTree(job.proc, "SIGTERM")
    } catch (err) {
      log({ event: "kill_error", id, err: err instanceof Error ? err.message : String(err) })
    }
    const killer = setTimeout(() => killTree(job.proc, "SIGKILL"), 2000)
    job.proc.once("exit", () => clearTimeout(killer))
    jobs.delete(id)
    log({ event: "stopped", id, reason, sessionID: job.sessionID })
    return true
  }

  const emitLines = (job: Job, chunk: Buffer, stream: "stdout" | "stderr") => {
    const text = (stream === "stdout" ? job.stdoutRest : job.stderrRest) + chunk.toString("utf8")
    const parts = text.split(/\r?\n/)
    const rest = parts.pop() ?? ""
    if (stream === "stdout") job.stdoutRest = rest
    else job.stderrRest = rest
    for (const line of parts) {
      const trimmed = line.trim()
      if (!trimmed) continue
      if (stream === "stderr") {
        // wait --follow 的 INFO 在 stderr；按行叫醒只能看 stdout，否则重连日志会把会话吵醒。
        log({ event: "stderr", id: job.id, line: trimmed.slice(0, 500) })
        continue
      }
      enqueue(job.sessionID, { id: job.id, description: job.description, line: trimmed, at: Date.now() })
    }
  }

  const flushRest = (job: Job) => {
    const leftover = job.stdoutRest.trim()
    job.stdoutRest = ""
    if (leftover) {
      enqueue(job.sessionID, { id: job.id, description: job.description, line: leftover, at: Date.now() })
    }
    job.stderrRest = ""
  }

  const startJob = (input: {
    sessionID: string
    command: string
    description: string
    timeoutMs: number
    cwd: string
  }) => {
    if (jobs.size >= MAX_JOBS) {
      throw new Error(`too many monitors (${MAX_JOBS}); stop one with monitor_kill`)
    }
    const id = newID()
    const sh = shell()
    // Unix 下 detached 让 sh 成为进程组首领，才能 SIGTERM 整棵树（handoff wait 是 sh -c 的孩子）。
    const proc = spawn(sh.bin, [sh.flag, input.command], {
      cwd: input.cwd,
      env: process.env,
      stdio: ["ignore", "pipe", "pipe"],
      detached: process.platform !== "win32",
      windowsHide: true,
    })
    const job: Job = {
      id,
      sessionID: input.sessionID,
      command: input.command,
      description: input.description,
      proc,
      startedAt: Date.now(),
      timeout: undefined,
      silent: false,
      stdoutRest: "",
      stderrRest: "",
    }
    jobs.set(id, job)
    log({ event: "started", id, sessionID: input.sessionID, command: input.command, timeoutMs: input.timeoutMs })

    proc.stdout?.on("data", (buf: Buffer) => emitLines(job, buf, "stdout"))
    proc.stderr?.on("data", (buf: Buffer) => emitLines(job, buf, "stderr"))
    proc.on("error", (err) => {
      log({ event: "spawn_error", id, err: err.message })
      enqueue(input.sessionID, {
        id,
        description: input.description,
        line: JSON.stringify({ handoff_monitor: "error", id, error: err.message }),
        at: Date.now(),
      })
    })
    proc.on("exit", (code, signal) => {
      if (job.timeout) clearTimeout(job.timeout)
      jobs.delete(id)
      log({ event: "exited", id, code, signal, sessionID: input.sessionID, silent: job.silent })
      if (job.silent) return
      flushRest(job)
      enqueue(input.sessionID, {
        id,
        description: input.description,
        line: JSON.stringify({ handoff_monitor: "exited", id, code, signal }),
        at: Date.now(),
      })
      const t = timers.get(input.sessionID)
      if (t) {
        clearTimeout(t)
        timers.delete(input.sessionID)
      }
      void flush(input.sessionID)
    })
    if (input.timeoutMs > 0) {
      job.timeout = setTimeout(() => stopJob(id, "timeout"), input.timeoutMs)
    }
    return id
  }

  const sessionIDOf = (ev: {
    properties?: { sessionID?: string; info?: { id?: string } }
  }) => ev.properties?.sessionID || ev.properties?.info?.id

  return {
    tool: {
      monitor: tool({
        description:
          "Run a shell command as a long-lived background watcher. Each stdout line wakes this session (nearby lines are batched). Use for handoff wait --follow / card wait --follow. Command stdout must stay silent until an actionable event. Do not poll. Stop with monitor_kill.",
        args: {
          command: tool.schema.string().describe("Shell command to run (stdout lines become wake events)"),
          description: tool.schema.string().describe("Short label prefixed onto each wake"),
          timeout_ms: tool.schema
            .number()
            .optional()
            .describe(`Kill after this many milliseconds (default ${DEFAULT_TIMEOUT_MS})`),
        },
        async execute(args, toolCtx) {
          await toolCtx.ask({
            permission: "bash",
            patterns: [args.command],
            always: [args.command],
            metadata: { command: args.command, description: args.description },
          })
          const id = startJob({
            sessionID: toolCtx.sessionID,
            command: args.command,
            description: args.description,
            timeoutMs: args.timeout_ms ?? DEFAULT_TIMEOUT_MS,
            cwd: toolCtx.directory,
          })
          return {
            title: args.description,
            output: `Started monitor ${id}. You will be notified on stdout lines. Do not poll. Stop with monitor_kill id=${id}.`,
            metadata: { id },
          }
        },
      }),
      monitor_kill: tool({
        description: "Stop a monitor started by the monitor tool.",
        args: {
          id: tool.schema.string().describe("Monitor id from a prior monitor call"),
        },
        async execute(args) {
          if (!stopJob(args.id, "kill")) {
            return `Monitor ${args.id} is not running.`
          }
          return `Stopped monitor ${args.id}.`
        },
      }),
      monitor_list: tool({
        description: "List running monitors started in this OpenCode process.",
        args: {},
        async execute() {
          if (jobs.size === 0) return "No running monitors."
          return [...jobs.values()]
            .map((j) => `${j.id}\t${j.description}\t${j.command}`)
            .join("\n")
        },
      }),
    },
    event: async ({ event }) => {
      const ev = event as {
        type?: string
        properties?: { sessionID?: string; info?: { id?: string }; status?: { type?: string } }
      }
      const sessionID = sessionIDOf(ev)
      if (!sessionID) return
      if (ev.type === "session.status") {
        if (ev.properties?.status?.type === "busy") busy.add(sessionID)
        if (ev.properties?.status?.type === "idle") {
          busy.delete(sessionID)
          void flush(sessionID)
        }
      }
      if (ev.type === "session.idle") {
        busy.delete(sessionID)
        void flush(sessionID)
      }
      if (ev.type === "session.deleted") {
        dead.add(sessionID)
        for (const [id, job] of jobs) {
          if (job.sessionID === sessionID) stopJob(id, "session_deleted")
        }
        queues.delete(sessionID)
        busy.delete(sessionID)
      }
    },
    dispose: async () => {
      for (const job of jobs.values()) dead.add(job.sessionID)
      for (const id of [...jobs.keys()]) stopJob(id, "dispose")
      queues.clear()
      log({ event: "disposed" })
    },
  }
}

export const HandoffMonitor = plugin
export default plugin
