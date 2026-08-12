// TextBlock —— 时间线上的模型正文段落。
//
// 职责：渲染一段合并后的 text 增量，代码围栏交给 CodeText
// 边界：永远展开、不可折叠。回合审阅需要的因果链里，正文是最不该被藏起来的一环
import { CodeText } from './codeText'

// TextBlock 渲染一段正文。
//
// 参数：text 已按 part 合并好的完整段落
export function TextBlock({ text }: { text: string }) {
  return (
    <div className="text-sm leading-relaxed">
      <CodeText text={text} />
    </div>
  )
}
