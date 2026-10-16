// desktopSeatCopy —— B434 卡页桌面端坐席入口的固定文案（契约 §3.12）。
// 文案即契约：改字面量 = 契约变更（desktopSeatCopy.test.ts 逐字锁定）。
// 前四条逐字来自 spec「实现决定」首条；拉起器两条来自用户 P4 裁决（按推荐）。

/** 空座主入口，与「▶ 叫机器人」并列。 */
export const DESKTOP_SEAT_BUTTON = '用 Claude Code 坐下'
/** 深链发不出时的兜底按钮。 */
export const DESKTOP_COPY_BRIEF_BUTTON = '复制简报'
/** 兜底按钮旁的说明。 */
export const DESKTOP_COPY_BRIEF_HINT = '到 Claude 桌面端新建会话后粘贴'
/** 首次拉起提示。 */
export const DESKTOP_TRUST_HINT = '如弹出信任文件夹，点信任，再按发送'
/** 空座第三个按钮：仅在材料端点返回 launcher 时显示。 */
export const DESKTOP_LAUNCHER_BUTTON = '让拉起器代开'
/** 拉起器代开请求成功后的提示。 */
export const DESKTOP_LAUNCHER_SENT = '已请拉起器代开，几分钟内这里会显示已绑定'
