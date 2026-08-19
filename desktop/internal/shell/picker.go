// 本文件是目录选择器里「选完之后」的那半边：校验与归一化。
//
// 为什么单独一个文件：原生对话框本身要跑起 GUI 才能调，测不了；
// 而真正会出错的是选完之后——用户可能取消、可能选到文件、可能选到已被删掉的路径。
// 把这半边关在这里，就能用普通 go test 覆盖。
//
// 边界：**只判断本机路径**。给远程开发机添项目时选出来的本机路径没有意义，
// 这个不对称与 B108「Reveal in Finder 只做本机半边」是同一个，已被接受（spec §4.5）。
package shell

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// NormalizeProjectDir 校验并归一化用户选中的项目目录。
//
// 参数：
//   - raw: 原生对话框返回的路径。用户取消时可能是空串
//
// 返回：
//   - 绝对路径
//   - error：空输入、路径不存在、或选中的不是目录。报文要说清是哪一种
func NormalizeProjectDir(raw string) (string, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		// 用户取消选择也会走到这里，属正常操作，用 Debug 不用 Warn
		slog.Debug("目录选择返回空值（用户取消或对话框未选中）")
		return "", fmt.Errorf("没有选择任何目录")
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		slog.Error("目录路径归一化失败", "raw", p, "cause", err)
		return "", fmt.Errorf("解析路径 %s: %w", p, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		slog.Error("选中的路径不可用", "path", abs, "cause", err)
		return "", fmt.Errorf("路径 %s 不可用: %w", abs, err)
	}
	if !info.IsDir() {
		slog.Error("选中的是文件而不是目录", "path", abs)
		return "", fmt.Errorf("%s 是文件，不是目录——请选择项目所在的目录", abs)
	}
	slog.Info("已选定项目目录", "path", abs)
	return abs, nil
}
