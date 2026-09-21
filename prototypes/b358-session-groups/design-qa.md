# Project tree option 1 design QA

## Source and implementation

- Source visual truth: `/Users/xushixin/.codex/generated_images/01a048bd-52f4-7542-b76b-695413314bee/exec-89032bfd-4aa0-495a-9fd3-070f3654db0c.png`
- Supplemental task icon reference: `/Users/xushixin/Downloads/AnyTimeDelete/截屏/截屏2026-08-28 23.24.26.png`
- Supplemental host hover reference: `/Users/xushixin/Downloads/AnyTimeDelete/截屏/截屏2026-08-28 23.27.15.png`
- Supplemental directory-item hover reference: `/Users/xushixin/Downloads/AnyTimeDelete/截屏/截屏2026-08-28 23.27.25.png`
- Implementation: `prototypes/base/pages/project-tree-option-1.html`
- Implementation screenshot: `/private/tmp/handoff-project-tree-option-1-latest.png`
- Latest project-header screenshot: `/private/tmp/handoff-project-tree-project-task-icon.png`
- Expanded-state screenshot: `/private/tmp/handoff-project-tree-option-1-latest-expanded.png`
- Host hover screenshot: `/private/tmp/handoff-project-tree-directory-host-hover.png`
- Directory child hover screenshot: `/private/tmp/handoff-project-tree-directory-child-hover.png`
- Combined comparison input: `/private/tmp/handoff-project-tree-comparison-latest.jpg`
- Default state: all three project sections expanded; first handoff task selected; first two tasks marked as opened; dispatch, terminal-tab, and file rows use distinct icons; search empty; right workspace intentionally blank.
- Expanded state: first `本机` directory row expanded with `master` and `eval/batch3` child rows; the host shows terminal-plus actions and the selected child shows its terminal action.

## Capture normalization

- CSS viewport: `1790 x 1520`
- Implementation browser capture: `1790 x 1520` pixels for the full-page screenshot; the right side is intentionally blank.
- Implementation sidebar layout: `456px` CSS width; the focused comparison normalizes both images to a `456 x 864` sidebar view.
- Source screenshot: `911 x 1727` pixels.
- Density: the comparison uses the source at half scale to compare the same compact layout rhythm; no claim is made that the source and browser screenshots share the same raster density. The right blank workspace was excluded from the focused sidebar comparison because it is an explicit out-of-scope area.

## Latest review findings

- [P2 resolved] Directory surface now uses a neutral `#f7f7f7` tint matching the supplied design instead of the earlier green tint.
- [P2 resolved] Added a visible gap between the `任务`/`目录` labels and the content surfaces below them.
- [P2 resolved] Reduced task and directory icon slots and removed the gray dot before `任务`.
- [P1 resolved] Dispatch task rows now use the supplied three-node reference icon (`dispatch-task.png`); terminal-tab and file rows retain their semantic terminal and file icons.
- [P2 resolved] Directory rows now toggle an expanded child treatment with a left chevron, compact child names, and smaller left-side treatment.
- [P2 resolved] Host rows reveal terminal-plus actions on hover, while directory child rows reveal only the terminal action on hover/selection.
- [P2 resolved] Added distinct `is-open` and `is-selected` task states: opened tasks use the hover tint, while the current focus task keeps the selected tint.
- [P2 resolved] Replaced the project-header `任务` label with the same dispatch-task icon while retaining the numeric count.

## Evidence review

- Full-view composition: the implementation preserves a left navigation rail with a blank right workspace, matching the requested prototype boundary.
- Focused region: the combined comparison checks the sidebar's task rows, directory labels, host surface, icon scale, and right-aligned machine metadata at the normalized `456 x 864` view.
- Hierarchy: the main vertical spine marks peer projects; each project has its own indented connector, task label, task rows, and subordinate directory group.
- Typography and spacing: neutral system sans, compact task/host rows, smaller icon slots, visible label-to-surface breathing room, stronger project names, compact dispatch icon/count project metadata, right-aligned task machine metadata, and restrained group separation match the source rhythm.
- Colors and tokens: white base, neutral gray separators, pale selected row, neutral light-gray directory host surface, green folder/status accents, and restrained icon gray match the source direction.
- Assets: search, folder, terminal, plus, file, directory, chevron, and host icons render from static assets; the dispatch icon is a transparent static extraction of the supplied reference crop and the remaining UI icons are static exports of the existing lucide-react library. No broken image assets remain.
- Copy: source task/project/machine labels are represented, including `handoff`, `benchmarking`, `charter`, `本机`, and `linux-01`.

## Interaction evidence

- Search filters the visible project groups and shows the empty state when nothing matches.
- Project headers collapse and expand their task/directory content.
- Directory rows expand and collapse their nested child treatment and rotate their chevron.
- Host hover exposes terminal and plus actions; selected/hovered child directory rows expose only terminal.
- Clicking a task moves the selected state so only one task is active.
- Opened tasks use the hover tint while the current task keeps the selected tint.
- `⌘K` / `Ctrl+K` focuses and selects the search input.
- Final browser console errors: none.

## Comparison history

1. Initial implementation preview did not render icons because the browser could not resolve the React dependency imported by the static HTML. Fixed by replacing the runtime import with static icon assets sourced from the existing icon library.
2. First interaction capture showed the browser's focus outline around the clicked task. Added keyboard-only focus styling and captured the default selected state without a focus ring.
3. User review identified excessive task/host row margins, an over-tinted directory section, and mismatched terminal/dispatch icon treatment. Tightened the row rhythm, moved the directory surface to a neutral panel, aligned machine metadata to the row edge, and initially explored a unified task icon.
4. Follow-up review clarified that only dispatch tasks use the supplied branch icon; terminal tabs and files keep semantic icons. Added the transparent supplied icon asset, restored task-type mapping, reduced icon sizes, and added terminal-plus/terminal directory actions.
5. Final comparison used the corrected hierarchy, matching selected/open states, compact expanded directory treatment, static assets, and blank right workspace. No actionable P0/P1/P2 visual findings remain.
6. Latest user refinement replaced each project-header `任务` label with the supplied dispatch-task icon; the refreshed focused comparison confirms all three project headers are consistent. No actionable P0/P1/P2 visual findings remain.

## Follow-up polish

- [P3] If this panel later becomes a production-width desktop sidebar, tune the final host shell's raster density and exact line weight against its production screenshot. The current prototype matches the supplied design crop and keeps the right side blank as requested.

## Implementation checklist

- [x] Main project spine and project nodes are visible.
- [x] Project > task > directory nesting is visually explicit.
- [x] Selected task, machine status, and directory affordances are represented.
- [x] Search, collapse/expand, task selection, and keyboard focus behavior are verified.
- [x] Static icon assets load without console errors.

final result: passed
