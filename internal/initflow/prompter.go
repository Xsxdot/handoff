// 本文件是 init 问答的通道：接口 + 按行读答案的脚本化实现。
//
// 职责：
//   - 定义 Prompter（Select / Input / Confirm）
//   - 提供 ScriptedPrompter：从 Reader 按行读，空行 / EOF 取默认
//     （测试与 CI 用；真终端走 cmd/init_huh.go）
//
// 边界：
//   - **不写配置**：只返回用户（或脚本）的答案，不碰 config.yaml
//   - **不探测工具链**：选项列表由调用方传入，这里不调 toolchain.Detect
//   - 不负责问题集合；问什么仍由本包的 AskAll 决定
package initflow

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
)

// ErrCanceled 表示用户中途取消（Ctrl-C / huh.ErrUserAborted）。
// RunE 见到它必须立刻返回、不得 Save：半截答案写出一份只配了一半的配置，
// 比取消本身更糟。
var ErrCanceled = errors.New("已取消")

// Option 是 Select 的一项。Value 写入配置，Label 给人看。
type Option struct {
	Value string
	Label string
}

// Prompter 是 init 问答的唯一入口。生产 TTY 走 huh（cmd/init_huh.go）；
// 测试经 runInitWith 把该缝换成脚本化实现，读 cmd.In。
type Prompter interface {
	Select(title string, options []Option, def string) (string, error)
	Input(title, def string) (string, error)
	Confirm(title string, def bool) (bool, error)
}

// ScriptedPrompter 从 Reader 按行读答案，行为对齐旧 ask / askString / askBool。
type ScriptedPrompter struct {
	in  *bufio.Reader
	out io.Writer
}

// NewScriptedPrompter 构造按行读的 Prompter。
//
// 参数：
//   - in: 答案来源（测试喂 strings.Reader）
//   - out: 提示打到哪；nil 当 io.Discard
//
// 注意：同一份输入只能包一层。AskAll 与 MaybeInstallService 必须共用实例，
// 否则各自 bufio 会把后续答案提前吃掉。
func NewScriptedPrompter(in io.Reader, out io.Writer) *ScriptedPrompter {
	if out == nil {
		out = io.Discard
	}
	return &ScriptedPrompter{in: bufio.NewReader(in), out: out}
}

// Select 选一项，返回命中的 Value。
//
// 匹配顺序：空行 / EOF 取 def；先按 Value 精确匹配，再按 1-based 下标。
// 同时认 Value 和下标，是为了过渡期保住现有 answers := "1\n" 脚本；
// Task 4 改成 Value 后下标仍可作为逃生。
func (p *ScriptedPrompter) Select(title string, options []Option, def string) (string, error) {
	v, err := p.readLine(title, def)
	if err != nil {
		slog.Error("prompter Select 读入失败", "title", title, "cause", err)
		return "", err
	}
	if v == "" {
		slog.Debug("prompter Select 空行/EOF，取默认", "title", title, "default", def)
		return def, nil
	}
	for _, o := range options {
		if v == o.Value {
			return o.Value, nil
		}
	}
	if n, aerr := strconv.Atoi(v); aerr == nil && n >= 1 && n <= len(options) {
		return options[n-1].Value, nil
	}
	slog.Debug("prompter Select 未匹配，取默认", "title", title, "input", v, "default", def)
	return def, nil
}

// Input 读一行字符串；空行 / EOF 返回 def。
func (p *ScriptedPrompter) Input(title, def string) (string, error) {
	v, err := p.readLine(title, def)
	if err != nil {
		slog.Error("prompter Input 读入失败", "title", title, "cause", err)
		return "", err
	}
	if v == "" {
		slog.Debug("prompter Input 空行/EOF，取默认", "title", title, "default", def)
		return def, nil
	}
	return v, nil
}

// Confirm 读 y/n。空行 / EOF 取 def；认 y/yes 为真、n/no 为假；其余当假。
func (p *ScriptedPrompter) Confirm(title string, def bool) (bool, error) {
	d := "n"
	if def {
		d = "y"
	}
	v, err := p.readLine(title+" (y/n)", d)
	if err != nil {
		slog.Error("prompter Confirm 读入失败", "title", title, "cause", err)
		return false, err
	}
	if v == "" {
		slog.Debug("prompter Confirm 空行/EOF，取默认", "title", title, "default", def)
		return def, nil
	}
	switch strings.ToLower(v) {
	case "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	default:
		return false, nil
	}
}

// readLine 打印「title [def]: 」并读一行。
//
// stdin 提前结束（脚本答案用完 / Ctrl-D）当作空行，不报错——与旧 ask 一致，
// 这样测试喂完答案后剩余问题全部取默认。
func (p *ScriptedPrompter) readLine(title, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(p.out, "%s [%s]: ", title, def)
	} else {
		fmt.Fprintf(p.out, "%s []: ", title)
	}
	line, err := p.in.ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(p.out)
		return "", nil
	}
	return strings.TrimSpace(line), nil
}
