// taskName.ts —— 任务显示名的统一口径（纯函数，无 React 依赖）。
//
// 职责：把一个任务投影成左栏行、tab 标题、面包屑共用的展示名。
// 命名口径与 ProjectTree 的任务行同源同值：name 优先，空则 plan_summary，
// 再空则「（无名称）」。
//
// 边界：不认识 React、不做数据获取——任务名由持有任务流的层解析后注入
// （见 tabs.ts#tabTitle 的 resolver 参数）；本文件只做纯投影。
export function taskDisplayName(t: { name: string; plan_summary: string }): string {
  return t.name || t.plan_summary || '（无名称）'
}
