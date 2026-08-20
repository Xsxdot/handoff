// discipline.go —— 执行纪律块的内置版本与能力分档。
//
// 职责：
//   - 内置 A/B 两版纪律块（go:embed），随二进制分发
//   - defaultTier：executor 名 → 内置档位的能力表
//   - Block：一次解析的产物（正文 + 人可读来源标注）
//
// 边界：
//   - 不理解纪律内容、不校验语义；不负责注入进 prompt（交各 adapter）
package discipline

import _ "embed"

//go:embed builtin/subagent.md
var builtinSubagent string

//go:embed builtin/single-context.md
var builtinSingleContext string

// builtinReview 是审阅角色的内置纪律块，只读，不写。
//
//go:embed builtin/review.md
var builtinReview string

// 内置档位名。
const (
	TierSubagent      = "subagent"       // 有 subagent 机制的执行器（opencode / claude）
	TierSingleContext = "single-context" // 无 subagent 机制的执行器（codex / grok）
)

// 纪律块角色名。名字是「这一轮执行者扮演什么角色」，与 Tier（执行器能力档位）
// 是两条正交的轴：implement 这一个角色内部还要按档位分，review 则与档位无关。
const (
	NameImplement = "implement" // 实现角色；内部按 defaultTier 落到 subagent / single-context
	NameReview    = "review"    // 审阅角色；只读，与执行器能力无关
)

// Block 是一次纪律解析的产物。
//
// Source 是人可读的来源标注（「内置:single-context」/「配置:my-rules.md」），
// 供派发时回显给协调者：配置化把纪律块从 plan 文件里拿走之后，写 plan 的人
// 再也看不见它，回显是唯一的补偿（B126-A）。
type Block struct {
	Text   string // 纪律块正文；空表示不注入
	Source string // 人可读来源标注；Text 为空时同为空
}

// defaultTier 是「无配置时按执行器有没有 subagent 机制选内置版本」的能力表。
// 加新 executor 时加一行。
var defaultTier = map[string]string{
	"opencode": TierSubagent,
	"claude":   TierSubagent,
	"codex":    TierSingleContext,
	"grok":     TierSingleContext,
}

// builtinFor 返回该 executor 的内置纪律块。
//
// 未登记的 executor 一律取单上下文版，这个保守方向是刻意的：单上下文版给
// 有 subagent 的执行器只是没用上能力（B93 实测仍 6/6 全绿），而 subagent 版
// 给没有 subagent 的执行器是灾难性的——它会把自己当协调者、每完成一个工作
// 单元就交还控制权，同一份 6-task plan 从「0 推动 26 分钟跑完」退化成
// 「9 次人工推动只到 3/6 且最后卡死」。
func builtinFor(executor string) Block {
	if defaultTier[executor] == TierSubagent {
		return Block{Text: builtinSubagent, Source: "内置:" + TierSubagent}
	}
	return Block{Text: builtinSingleContext, Source: "内置:" + TierSingleContext}
}

// builtinByName 返回该名字的内置纪律块；名字没有内置对应物时返回 ok=false。
//
// 参数：name 角色名；executor 仅在 name==NameImplement 时被使用（选档位）。
// 返回：Block 与命中标志。
//
// Source 里给 implement 带上档位（如「内置:implement(single-context)」）是刻意的：
// 只写角色名会把「派错档」这个历史上真出过事的信息藏起来——codex/grok 读到
// subagent 版会转而扮协调者，同一份 plan 从「0 推动跑完」退化成「9 次人工推动卡死」。
func builtinByName(name, executor string) (Block, bool) {
	switch name {
	case NameImplement:
		b := builtinFor(executor)
		tier := TierSingleContext
		if defaultTier[executor] == TierSubagent {
			tier = TierSubagent
		}
		return Block{Text: b.Text, Source: "内置:" + NameImplement + "(" + tier + ")"}, true
	case NameReview:
		return Block{Text: builtinReview, Source: "内置:" + NameReview}, true
	}
	return Block{}, false
}

// Builtin 是一份内置纪律块（Tier + 正文）。控制台把它作为只读条目展示，
// 并允许「以此为模板新建」——用户想微调内置纪律时不必去仓库里翻原文。
type Builtin struct {
	Tier    string
	Content string
}

// Builtins 返回全部内置纪律块，顺序固定为 subagent、single-context、review。
//
// 顺序固定是给界面用的：列表次序不该随 map 迭代而抖动。
func Builtins() []Builtin {
	return []Builtin{
		{Tier: TierSubagent, Content: builtinSubagent},
		{Tier: TierSingleContext, Content: builtinSingleContext},
		// review 追加在末尾而不是插在前面：控制台用 builtins[0] 当默认选中项，
		// 换位置会静默改掉用户打开设置页时看到的内容。
		{Tier: NameReview, Content: builtinReview},
	}
}

// DefaultTierFor 返回该 executor 在「未配置」这一档会用到的内置版本名。
//
// 未登记的 executor 一律 TierSingleContext，理由见 builtinFor 的注释。
// 界面即使在已配置的档位上也要显示它——那是「改回默认会变成什么」的预告。
func DefaultTierFor(executor string) string {
	if defaultTier[executor] == TierSubagent {
		return TierSubagent
	}
	return TierSingleContext
}
