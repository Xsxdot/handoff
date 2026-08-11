# Prototype Instructions

Run the local server yourself and open the preview in the browser available to this environment. Do not give the user server-start instructions when you can run it.

Before making substantial visual changes, use the Product Design plugin's `get-context` skill when the visual source is unclear or no longer matches the current goal. When the user gives durable prototype-specific design feedback, preferences, or decisions, record them in `AGENTS.md`.

When implementing from a selected generated mock, treat that image as the source of truth for layout, component anatomy, density, spacing, color, typography, visible content, and hierarchy.

Build app UI in `src/`. Keep `.openai/hosting.json`, `worker/index.js`, `scripts/prepare-sites-build.mjs`, and `tests/sites-worker.test.mjs` intact so the same local prototype can be handed to Sites. Before a Sites handoff, run `npm run build` and `npm run test:sites`; the build must leave `dist/client/index.html`, `dist/server/index.js`, and `dist/.openai/hosting.json`.

## Confirmed product and visual decisions

- Use an Orca-like three-column desktop workbench: project overview on the left, terminal/editor/browser tab groups in the center, and the selected directory's file tree on the right.
- Each project has at least one code location: local, one paired remote development machine, or both. A project can never bind more than one remote machine. The left hierarchy is project → code location(s) → main/worktree directory → handoff tasks. Project rows aggregate directory, running-task, and attention counts.
- Selecting a directory changes both the central workspace context and the right file tree. A disconnected remote machine stays visible but cannot be opened.
- handoff is the executor attachment layer. A task opens the executor's native Codex CLI/OpenCode-style TUI; do not represent handoff itself as a task-table TUI.
- Keep the quiet light Orca visual system: Geist UI type, compact monospace technical surfaces, neutral chrome, hairline dividers, and color reserved for state and git decorations.
- Global task board, machine/agent management, and settings replace the workbench content area while keeping the project overview visible. The file tree appears only when a concrete directory is selected in the workbench.
- The task board uses four lifecycle columns; approval, question, blocked, and failure are card-level intervention states rather than additional columns. Opening an actionable card returns to its existing task session in the workbench.
- Settings contain only basic desktop behavior and Env-file management. An Env file is a physical `.env` file under one machine's handoff directory; each machine has many files and each file has many variables.
- Executor availability, each executor's optional Env file, and the automatic approver are configured per machine on the development-machine page. An automatic approver is a lightweight executor invocation in handoff's graded approval flow; uncertainty escalates to the user.
- Adding a project first selects local and/or one remote development machine, with at least one selected, then configures a Git repository or existing directory for every selected location. Git clone path is optional and defaults to `~/.handoff/<project-name>`. Only local existing directories can be chosen through Finder; remote directories accept pasted paths only.
- Machine management handles already-paired agents and shows disconnected code hosts, their files, Env files, and task sites as unavailable, not read-only.
- The task board's project filter is a clickable dropdown that supports multi-select.
- This folder is a throwaway interactive prototype. Do not add production backend integration, persistence, or production instrumentation unless the user explicitly expands scope.
