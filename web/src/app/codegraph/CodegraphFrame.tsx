// CodegraphFrame —— /codegraph 的宿主薄壳。
//
// 职责：把当前工作台项目名编码进同源 charter viewer iframe 的 URL。
// 边界：不取 API、不读取 token/cookie、不复制项目选择器或 viewer 状态；
// charter viewer 自己负责同源请求与二期代码图渲染。
import type { ReactElement } from 'react'

export interface CodegraphFrameProps {
  project: string
}

// 参数：project 是当前 BaseDir.projectName；空串是合法输入，viewer 会跳过取数。
// 返回：指向宿主 /codegraph/app/ 的同源 iframe；project 只进入 query 且经过编码。
// 注意：不要把 machine、token、cookie、host 或第二端口写进 src。
export function CodegraphFrame({ project }: CodegraphFrameProps): ReactElement {
  const src = `/codegraph/app/?project=${encodeURIComponent(project)}`
  return (
    <iframe
      title="代码图"
      src={src}
      className="h-full w-full border-0"
    />
  )
}
