// Banners.tsx —— 断线 / 会话失效两类全局横幅。
//
// 断线语义（产品契约）：断线时**保留**最后拿到的列表/详情数据继续显示，所有会
// 改状态的按钮置为不可用，明确标注「已断开」。**不称为「只读」**——只读暗示
// 数据是新的，而它不是。恢复连接后横幅消失、按钮恢复。
//
// 会话失效：agentd 吊销会话时 /ws/events 以 close code 1008 关闭，这是不可恢复
// 的终止——落到「请重新打开控制台」而不是无脑重连。
import { AlertTriangle, RotateCw, WifiOff } from 'lucide-react'
import { Button } from '@/components/ui/button'

// DisconnectedBanner 标注「已断开」：保留陈旧数据显示，提示正在恢复。
//
// 参数：
//   - message: 断开原因原文（agentd 错误信息信息量大，必须透传）
export function DisconnectedBanner({ message }: { message: string }) {
  return (
    <div role="alert" className="flex items-start gap-2 rounded-lg border border-amber-500/40 bg-amber-500/10 p-3 text-sm">
      <WifiOff className="mt-0.5 size-4 shrink-0 text-amber-600" />
      <div className="flex flex-col gap-0.5">
        <p className="font-medium text-amber-700">已断开</p>
        <p className="break-words text-foreground/80">
          {message}（保留最后一次拿到的数据继续显示；会改状态的操作已禁用，恢复连接后自动解除）
        </p>
      </div>
    </div>
  )
}

// SessionExpiredBanner 表示会话已失效：唯一可行动的动作是重新兑换 cookie。
//
// 注意：到达这里后各数据源（轮询 / WS / render 流）都会持续失败，本横幅是
// 终止态，不做重连尝试。
export function SessionExpiredBanner() {
  return (
    <div role="alert" className="flex items-start gap-2 rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm">
      <AlertTriangle className="mt-0.5 size-4 shrink-0 text-destructive" />
      <div className="flex flex-col gap-1">
        <p className="font-medium text-destructive">会话已失效，请重新打开控制台</p>
        <p className="text-foreground/80">
          当前会话已被吊销或过期。请执行 handoff console --print-url，把打印出的地址端口从
          7777 换成 5173 后打开，完成 cookie 兑换后再回来。
        </p>
      </div>
    </div>
  )
}

// LoadFailed 首次加载失败（还没有任何可显示的数据）时的终止态，附重试。
export function LoadFailed({ message, onRetry }: { message: string; onRetry: () => void }) {
  return (
    <div role="alert" className="flex flex-col gap-3 rounded-lg border border-destructive/40 bg-destructive/10 p-4">
      <div className="flex items-start gap-2 text-sm">
        <AlertTriangle className="mt-0.5 size-4 shrink-0 text-destructive" />
        <p className="break-words text-foreground/90">{message}</p>
      </div>
      <div>
        <Button variant="outline" size="sm" onClick={onRetry}>
          <RotateCw className="size-4" />
          重试
        </Button>
      </div>
    </div>
  )
}
