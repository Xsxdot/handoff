// taskenv.go —— codex 任务的启动物料：包内文件名常量。
//
// 职责：
//   - 统一约定任务目录内的文件名（serve.log / render.log）
//
// 边界：
//   - 不起进程（proc.go 的 prochost 负责）、不碰协议（appserver.go）
//   - **刻意不生成任何 codex 配置文件**：本设计的安全档位全部协议级下发
//     （spec §2「配置下发：全部协议级，不碰任何 config 文件」），写配置文件会
//     让「代码钉死安全边界」这条保证多出一个可被绕过的入口
//   - env 注入不再经启动脚本：改由 proc.go 的 Spec.Env 直传（B19 覆盖语义不变，
//     droppedEnvKeys 的丢弃逻辑随脚本一并移入 StartServe 的 env 处理）。普通派发
//     丢弃用户 CODEX_HOME；小队载体有非空 HOME 时，显式保留它以使用载体凭据。
package codex

const (
	serveLogName  = "serve.log"
	renderLogName = "render.log"
)

// droppedEnvKeys 是普通派发中 env 文件里出现即**丢弃**的变量。
//
// 为什么是丢弃而不是像 grok 那样靠 env 顺序覆盖：codex adapter 自身在普通派发
// 中从不设置 CODEX_HOME（本设计刻意复用用户级 ~/.codex，spec §1.3），没有「后
// 写的那行」可以压过它。一旦生效，executor 会换到一个空 home 跑——凭据、插件、
// sessions 全部落空，任务以「未登录」形态失败且原因极难追。只有 manager 已注入
// 非空载体 HOME 时才走显式例外。
var droppedEnvKeys = map[string]bool{
	"CODEX_HOME": true,
}
