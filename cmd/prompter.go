// 本文件是 init 问答的通道：接口 + 按行读答案的脚本化实现。
//
// 职责：
//   - 定义 prompter（Select / Input / Confirm），挡住后续 huh 实现
//   - 提供 scriptedPrompter：从 Reader 按行读，空行 / EOF 取默认
//
// 边界：
//   - **不写配置**：只返回用户（或脚本）的答案，不碰 config.yaml
//   - **不探测工具链**：选项列表由调用方传入，这里不调 toolchain.Detect
//   - 不负责问题集合；问什么仍由 init.go 的 askAll 决定
package cmd

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
)

// promptOption 是 Select 的一项。Value 写入配置，Label 给人看。
type promptOption struct {
	Value string
	Label string
}

// prompter 是 init 问答的唯一入口。生产（Task 5）走 huh；测试与当前 TTY
// 走脚本化实现读 cmd.In。
type prompter interface {
	Select(title string, options []promptOption, def string) (string, error)
	Input(title, def string) (string, error)
	Confirm(title string, def bool) (bool, error)
}

// scriptedPrompter 从 Reader 按行读答案，行为对齐旧 ask / askString / askBool。
type scriptedPrompter struct {
	in  *bufio.Reader
	out io.Writer
}

// newScriptedPrompter 构造按行读的 prompter。
//
// 参数：
//   - in: 答案来源（测试喂 strings.Reader，TTY 喂 cmd.InOrStdin）
//   - out: 提示打到哪；nil 当 io.Discard
//
// 注意：同一份输入只能包一层。askAll 与 maybeInstallService 必须共用实例，
// 否则各自 bufio 会把后续答案提前吃掉。
func newScriptedPrompter(in io.Reader, out io.Writer) *scriptedPrompter {
	if out == nil {
		out = io.Discard
	}
	return &scriptedPrompter{in: bufio.NewReader(in), out: out}
}

// Select 选一项，返回命中的 Value。
//
// 匹配顺序：空行 / EOF 取 def；先按 Value 精确匹配，再按 1-based 下标。
// 同时认 Value 和下标，是为了过渡期保住现有 answers := "1\n" 脚本；
// Task 4 改成 Value 后下标仍可作为逃生。
func (p *scriptedPrompter) Select(title string, options []promptOption, def string) (string, error) {
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
func (p *scriptedPrompter) Input(title, def string) (string, error) {
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
func (p *scriptedPrompter) Confirm(title string, def bool) (bool, error) {
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
func (p *scriptedPrompter) readLine(title, def string) (string, error) {
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
