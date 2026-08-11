# Design QA

## Target and implementation

- Source visual truth: `reference/final-workbench.png`
- Source pixels: 1486 × 1058, visually normalized to the 1440 × 1024 desktop viewport
- Implementation viewport: 1440 × 1024 CSS px at DPR 1
- Implementation evidence:
  - `implementation-complete-workbench.png`
  - `implementation-project-location-selection.png`
  - `implementation-project-local-finder.png`
  - `implementation-project-remote-path.png`
  - `implementation-board-project-multiselect.png`
  - `implementation-machine-runtime-config.png`
  - `implementation-settings-general.png`
  - `implementation-settings-env-files.png`
- States reviewed: directory-scoped workbench, local/remote project-location selection, local Finder directory selection, remote pasted-path input, project multi-select, connected/disconnected machine management, per-machine executor/approver configuration, basic settings, and machine Env-file management
- Comparison method: the source and the three latest project-wizard screenshots were opened together in the same visual comparison input at original detail; prior global-page comparisons remain unchanged.

## Full-view comparison

The workbench remains faithful to the selected Orca-style target: a persistent project overview on the left, directory-scoped terminal/editor/browser work in the center, a file tree only when a directory context exists, and a compact status bar. The new global pages replace the center and file regions while retaining the same shell, typography, divider rhythm, neutral palette, icon treatment, state colors, and compact information density.

The task board uses four lifecycle columns and keeps intervention states on cards. Machine management preserves the same project/machine vocabulary, keeps Env and automatic-approver configuration inside the selected machine, and makes disconnected locations explicitly unavailable. Settings use a narrow internal navigation and a two-pane Env-file editor rather than introducing a visually unrelated admin console.

## Required fidelity surfaces

- Fonts and typography: Geist and the existing monospace stack are preserved. Heading, label, metadata, truncation, and code weights stay consistent with the source shell.
- Spacing and layout rhythm: the 300 px left rail, 32 px status bar, compact rows, hairline dividers, and 7–9 px radii remain consistent. Global pages use the vacated center/right width without showing an unrelated file rail.
- Colors and visual tokens: neutral whites and grays dominate; green, amber, and red remain reserved for connected/running, intervention, and disconnected states.
- Image quality and asset fidelity: the product contains no new raster imagery. Existing Lucide icons are reused consistently; no placeholder art, handcrafted SVG, CSS illustration, or emoji was introduced.
- Copy and content: labels follow the confirmed domain language: project, project location, project root, local machine, at most one remote machine, main/worktree directory, Env file, executor, automatic approver, handoff task, and unavailable remote location.

## Focused comparison

A separate crop was not required because the critical dense surfaces—task cards, project creation, machine runtime controls, Env-file rows, automatic-approval chain, and workbench chrome—were legible in the 1440 × 1024 originals. They were inspected alongside the full source rather than inferred from code.

## Comparison history

### Iteration 1

- P2: the task-board header exposed a visually primary “新建 handoff” action that had no corresponding confirmed flow. Removed it so every visible core action is functional and the board stays focused on observation and intervention.
- P2: waiting tasks used a running-green dot. Added a neutral state token so queued work is not mistaken for active execution.

### Iteration 2

- Re-captured `implementation-task-board-final.png` at the same viewport.
- Verified the unimplemented CTA is gone, waiting states are neutral, column hierarchy remains intact, and the browser reports no console errors or warnings.
- No remaining actionable P0, P1, or P2 visual findings.

### Iteration 3

- Replaced the earlier multi-location project concept with one code host per project and a two-step host/source wizard. Git clone path defaults to `~/.handoff/<project-name>`; local existing directories expose a Finder picker.
- Changed the task-board project token into a compact dropdown multi-select with selected count and live task counts.
- Moved executor and approval configuration out of global settings. Each machine now owns executor enablement, an optional Env-file selector, and an automatic approver that escalates uncertainty to the user.
- Replaced scoped environment-variable precedence with machine-owned `.env` files and a file/variable editor. Disconnected machines expose no Env contents or editing controls.
- Compared all revised surfaces with the source at 1440 × 1024. Density, hierarchy, controls, dividers, and status colors remain aligned; no actionable P0, P1, or P2 visual findings remain.

### Iteration 4

- Corrected the over-constrained single-host interpretation. Project creation now supports exactly three valid combinations: local only, one remote only, or local plus one remote; selecting neither blocks continuation.
- Kept remote-machine selection singular through one selector, so a project can never configure multiple remote hosts.
- When both locations are selected, the source step exposes one tab per location and stores independent Git/directory configuration for each.
- Existing-directory selection now shows Finder only for the local location. The remote location has one path input and explicit pasted-path guidance, with no browse control.
- Compared the location selector, local Finder state, and remote path-only state with the source at 1440 × 1024. Modal dimensions, density, hierarchy, controls, dividers, and status colors remain aligned; no actionable P0, P1, or P2 visual findings remain.

## Interaction checks

- Filter the task board to intervention-only cards: passed
- Open running task T-004 from the board in its existing workbench TUI: passed
- Open approval task T-005 and approve once: passed
- Select disconnected `staging-03`, keep terminal unavailable, then retry connection: passed
- Pair a new machine, copy the one-time command, and add the machine card: passed
- Toggle per-machine executor availability: passed
- Verify local-only, one-remote-only, and local-plus-one-remote project combinations: passed
- Deselect both locations and verify continuation is disabled: passed
- Configure both selected locations independently and verify the project is added with two directory locations: passed
- Select an existing local directory through the Finder action: passed
- Select an existing remote directory and verify only a pasted-path input is available: passed
- Open the project filter, deselect multiple projects, and verify visible card/task counts: passed
- Select different machines and verify executor Env and automatic-approver configuration remain machine-specific: passed
- Select a disconnected machine and verify executor/approver controls are disabled: passed
- Open a machine Env file, add a new Env file and variable, then select a disconnected machine and verify contents are unavailable: passed
- Verify Settings contains only Basic and Env Files; executor and approval pages are absent: passed
- Existing workbench editor/browser/file/terminal interactions remain available: passed

## Runtime checks

- `npm run build`: passed
- `npm run test:sites`: passed, 4 tests
- `git diff --check`: passed
- Browser console errors/warnings: none
- Browser viewport: 1440 × 1024 at DPR 1

## Final result

passed
