// taskName.ts —— 任务显示名的统一口径（纯函数，无 React 依赖）。
//
// 职责：把一个任务投影成左栏行、tab 标题、面包屑共用的展示名。
// 命名口径与 ProjectTree 的任务行、search 的任务搜索同源同值：
//   1. name —— 但**名只是 plan_summary 前缀时除外**。后端派发时没给显式名
//      也没给 plan 文件名的话，会拿 prompt 前 20 字当名（manager.go#deriveName），
//      charter 派发的提示词开头千篇一律，这种名分不清谁是谁；它恰好等于
//      plan_summary 的开头，可判定。此时改用 branch（cards/Bxxx 卡分支）。
//   2. branch（无名但分支可辨的派发，如 bench- 系列的 plan 名派生名不受影响）
//   3. plan_summary
//   4. 「（无名称）」
//
// 边界：不认识 React、不做数据获取——任务名由持有任务流的层解析后注入
// （见 tabs.ts#tabTitle 的 resolver 参数）；本文件只做纯投影。
export function taskDisplayName(t: {
  name: string
  branch: string
  plan_summary: string
}): string {
  if (t.name !== '' && !t.plan_summary.startsWith(t.name)) return t.name
  return t.branch || t.plan_summary || '（无名称）'
}
