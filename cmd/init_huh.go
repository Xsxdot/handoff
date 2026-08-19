// 本文件是 init 在真终端上的 huh 问答实现。
//
// 职责：
//   - 用 huh 的 Select / Input / Confirm 实现 initflow.Prompter
//   - 把用户取消（Ctrl-C、huh.ErrUserAborted、context 取消）译成 initflow.ErrCanceled
//
// 边界：
//   - **只服务 TTY**：测试不得走这里。CI 没有真终端，huh 会挂死；
//     测试经 newInteractivePrompter 缝换成脚本化实现
//   - **取消 / 失败绝不写配置**：本文件只返回错误。写盘是 RunE 的事，
//     见到错就不 Save，避免留下一份只配了一半的 config.yaml
//   - 不负责问题集合；问什么仍由 initflow.AskAll 决定
package cmd

import (
	"context"
	"errors"
	"log/slog"

	"charm.land/huh/v2"

	"github.com/Xsxdot/handoff/internal/initflow"
)

// huhPrompter 用 huh 控件问完一题。空结构体：状态都在控件里。
type huhPrompter struct{}

var _ initflow.Prompter = huhPrompter{}

// newHuhPrompter 构造真终端 huh 实现。
//
// 注意：
//   - 只给 TTY 用；测试必须走 newInteractivePrompter 缝换成脚本化
//   - 取消 / 意外失败只返回错误，不写盘（写盘是 RunE 的事）
func newHuhPrompter() initflow.Prompter {
	slog.Debug("init 使用 huh 问答")
	return huhPrompter{}
}

// Select 用 huh 选一项，返回命中的 Value。
//
// 参数：
//   - title: 题干，与 initflow.AskAll 现有中文标题一致
//   - options: Value 写入配置，Label 给人看
//   - def: 预选项（光标停在对应项）
//
// 返回：选中的 Value；取消时返回 initflow.ErrCanceled。
func (huhPrompter) Select(title string, options []initflow.Option, def string) (string, error) {
	value := def
	err := newHuhSelect(title, options, &value).Run()
	if err != nil {
		return "", mapHuhErr(title, err)
	}
	slog.Debug("huh Select 完成", "title", title, "value", value)
	return value, nil
}

// newHuhSelect 构造会把全部选项画出来的 Select。
//
// 参数：
//   - title: 题干
//   - options: Value 写入配置，Label 给人看
//   - value: 预选值，Run 后写成用户的选择
//
// 注意：
//   - huh v1 把 viewport.YOffset 直接设成 selected，三项里预选第二项时
//     「执行机」被卷出视口。v2 改成 ensureCursorVisible，只做让光标露出来
//     的最小滚动；Height 再锁成「标题 + 全部选项」，避免 Form 按终端高度
//     再裁一刀。
func newHuhSelect(title string, options []initflow.Option, value *string) *huh.Select[string] {
	def := ""
	if value != nil {
		def = *value
	}
	opts := make([]huh.Option[string], 0, len(options))
	for _, o := range options {
		opt := huh.NewOption(o.Label, o.Value)
		if o.Value == def {
			opt = opt.Selected(true)
		}
		opts = append(opts, opt)
	}
	slog.Debug("huh Select 构造", "title", title, "options", len(options), "default", def)
	return huh.NewSelect[string]().
		Title(title).
		Options(opts...).
		Value(value).
		Height(len(options) + 2)
}

// Input 用 huh 读一行字符串，预填 def。
//
// 参数：
//   - title: 题干，与 initflow.AskAll 现有中文标题一致
//   - def: 输入框预填值，回车即保留
//
// 返回：用户输入；取消时返回 initflow.ErrCanceled。
func (huhPrompter) Input(title, def string) (string, error) {
	value := def
	err := huh.NewInput().
		Title(title).
		Value(&value).
		Run()
	if err != nil {
		return "", mapHuhErr(title, err)
	}
	slog.Debug("huh Input 完成", "title", title)
	return value, nil
}

// Confirm 用 huh 问是/否，预选 def。
//
// 参数：
//   - title: 题干，与 initflow.AskAll 现有中文标题一致
//   - def: 预选（true=是）
//
// 返回：用户选择；取消时返回 initflow.ErrCanceled。
func (huhPrompter) Confirm(title string, def bool) (bool, error) {
	value := def
	err := huh.NewConfirm().
		Title(title).
		Affirmative("是").
		Negative("否").
		Value(&value).
		Run()
	if err != nil {
		return false, mapHuhErr(title, err)
	}
	slog.Debug("huh Confirm 完成", "title", title, "value", value)
	return value, nil
}

// mapHuhErr 把 huh / 终端错误译成 init 能识别的取消或原样失败。
//
// Ctrl-C 在 huh 里是 ErrUserAborted；带超时的 context 取消是 context.Canceled。
// 这两种都是用户主动离开，打 Warn。其它失败（终端不够用、huh 内部错）打 Error。
// 两种都不写盘——由调用方见到非 nil 就不 Save。
func mapHuhErr(title string, err error) error {
	if errors.Is(err, huh.ErrUserAborted) || errors.Is(err, context.Canceled) {
		slog.Warn("init 向导已取消", "title", title, "cause", err)
		return initflow.ErrCanceled
	}
	slog.Error("init huh 问答失败", "title", title, "cause", err)
	return err
}
