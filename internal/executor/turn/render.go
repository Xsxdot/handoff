// render.go —— 回合文本增量落盘到 render.log。
//
// 职责：把模型文本增量追加到任务目录的 render.log，供 handoff attach 实况读取
// 边界：只做追加写；文件不存在时创建；不轮转、不清理（任务归档时随目录一起走）

package turn

import (
	"fmt"
	"os"
)

// AppendRender 把 delta 追加到 renderLogPath（不存在则创建，权限 0644）。
//
// 注意：调用方通常在高频文本增量路径上调用本函数，失败应只 Warn 不中断回合
// ——可见性是增强能力，不值得为它挂掉任务。
func AppendRender(renderLogPath, delta string) error {
	f, err := os.OpenFile(renderLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("打开 %s: %w", renderLogPath, err)
	}
	defer f.Close()
	if _, err := f.WriteString(delta); err != nil {
		return fmt.Errorf("写 %s: %w", renderLogPath, err)
	}
	return nil
}
