// 升级面板：显示同步进度与 handoff upgrade --now 的流式输出。
//
// 职责：
//   - 监听 upgrade-line：把一行输出追加到输出区并滚到底
//   - 监听 upgrade-state：切换三态标题，失败时亮出「带 --force 重试」
//   - 点「带 --force 重试」时发 upgrade-force-retry 事件回 Go 侧
//
// 边界：
//   - 不解析输出内容。判断「是不是闸一导致的失败」交给用户自己看——输出是
//     给人看的中文表格，解析它是脆的，且失效方式是「按钮再也不出现」，
//     没有任何报错（见 spec §7.2）
//   - 不自己发起任何动作，只显示与回传点击
import { Events } from "@wailsio/runtime";

const title = document.getElementById("panel-title") as HTMLHeadingElement;
const output = document.getElementById("panel-output") as HTMLPreElement;
const actions = document.getElementById("panel-actions") as HTMLDivElement;
const force = document.getElementById("panel-force") as HTMLButtonElement;

Events.On("upgrade-line", (ev: { data: string }) => {
  output.textContent += ev.data + "\n";
  // 滚到底：长输出时用户关心的永远是最后一行
  output.scrollTop = output.scrollHeight;
});

Events.On("upgrade-state", (ev: { data: { state: string; detail: string } }) => {
  const { state, detail } = ev.data;
  switch (state) {
    case "running":
      title.textContent = detail || "正在升级";
      actions.hidden = true;
      break;
    case "ok":
      title.textContent = detail || "升级完成";
      actions.hidden = true;
      break;
    case "fail":
      title.textContent = detail || "升级失败";
      // 只要失败就亮按钮，不去判断失败原因——理由见文件头
      actions.hidden = false;
      force.disabled = false;
      break;
  }
});

force.addEventListener("click", () => {
  // 立刻禁用，避免连点发出两次强制升级
  force.disabled = true;
  Events.Emit("upgrade-force-retry", null);
});
