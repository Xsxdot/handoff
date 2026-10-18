// deliveryCopy —— B435 房间页投递标注的文案常量与 no_recipient → 标注映射（W plan Q8/Q9）。
//
// 职责：spec §9 两条用户可见文案的唯一定义处 + 契约 §3.5 消费方映射（六值 → 标/不标）+
// 读时语义悬停说明（D-E，Q8 裁定稿）。组件与测试一律 import 这里的常量，不另写一份。
// 边界：只做映射，不判定收件人——判定权威在服务端 collab.Service.Recipients（读时求值），
// UI 不按 mentions / 席位自算（spec §8 禁止各方自带匹配逻辑）。
import type { NoRecipientReason } from '../../api/rooms'
import { logRoom } from './roomLog'

/** spec §9：寻址解析不出地址 / 根卡空座时消息下的标注。 */
export const NO_RECIPIENT_COPY = '无收件人（@ 指定）'
/** spec §9：多根卡（或零根卡）会话里人类无 @ 发言的标注（消息级，N3）。 */
export const NEEDS_MENTION_COPY = '需 @'
/** D-E 读时语义提示（Q8 裁定稿）：标注随当前席位/驻场者重算，补座后可能消失。 */
export const DELIVERY_READ_TIME_HINT = '按当前席位与驻场者判定；补座或坐下后可能不再显示'

// 已告警过的词表外值：同一漂移值只告警一次，轮询重渲染不刷屏。
const warnedUnknown = new Set<string>()

/**
 * noRecipientLabel 把 RoomHistoryItem.no_recipient 映射成标注文案（契约 §3.5）。
 *
 * 参数 reason：wire 原值；缺键传 undefined。
 * 返回：要显示的文案；null = 不标（structural / not_session_room / non_human_unaddressed、
 * 缺键、词表外值）。
 * 注意：switch 穷尽六值且以 never 收尾——联合类型新增取值而这里未登记时编译失败；
 * 运行期遇词表外值（wire 漂移）不标，并 console.warn 一次带键名与原值，不静默吞。
 */
export function noRecipientLabel(reason: NoRecipientReason | undefined): string | null {
  if (reason === undefined) return null
  switch (reason) {
    case 'addressed_unresolved':
    case 'root_no_seat':
      return NO_RECIPIENT_COPY
    case 'needs_mention':
      return NEEDS_MENTION_COPY
    case 'structural':
    case 'not_session_room':
    case 'non_human_unaddressed':
      return null
  }
  const unexpected: never = reason
  const value = String(unexpected)
  if (!warnedUnknown.has(value)) {
    warnedUnknown.add(value)
    logRoom('warn', 'delivery_no_recipient_unknown', { key: 'no_recipient', value })
  }
  return null
}
