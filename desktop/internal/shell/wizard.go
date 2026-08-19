// 本文件是 shell 包对首次配置的薄封装：BuildForm 造表、ApplyAnswers 校验写回。
//
// 边界：
//   - **不 import Wails**。表单构造与校验都在根模块 internal/initflow 里，
//     这里只是让桌面侧用法可以被不 import Wails 的普通 go test 覆盖
//     （薄壳纪律：shell 包不碰 Wails，装配与 Wails API 只在 desktop/main.go）。
//   - **不落盘**：BuildForm/ApplyAnswers 只改内存里的 cfg，Save 由调用方决定；
//     校验失败时调用方绝不可落盘。
//   - 不记录答案内容（可能含 token 或私有路径）。
package shell

import (
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/initflow"
	"github.com/Xsxdot/handoff/internal/toolchain"
)

// BuildForm 造出首次配置的字段表。
//
// 它只是 initflow.Form 的一层薄封装，存在的理由是让桌面侧的用法可以被
// 不 import Wails 的普通 go test 覆盖（薄壳纪律：shell 包不碰 Wails）。
func BuildForm(cfg *config.Config, rs []toolchain.Result, goos string) []initflow.Field {
	return initflow.Form(cfg, rs, goos, false) // 首次配置恒 cfgExisted=false
}

// ApplyAnswers 校验前端回传的答案并写回 cfg。
//
// 承重：**返回错误时调用方绝不可落盘**。半截答案落盘会造出一份让
// shell.Resolve 判为「已配置」的文件，向导从此再也不会出现（W5b-2 缺陷 A）。
func ApplyAnswers(cfg *config.Config, fields []initflow.Field, answers map[string]string) error {
	return initflow.Apply(cfg, fields, answers)
}
