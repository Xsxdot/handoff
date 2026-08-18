// Package discipline resolves the execution-discipline block injected into a task prompt.
//
// Responsibilities:
//   - embed the two built-in discipline blocks distributed with the binary
//   - map executors to the built-in capability tier
//   - return a resolved block with a human-readable source marker
//
// It does not interpret discipline content or inject it into prompts; adapters own that.
package discipline

import _ "embed"

//go:embed builtin/subagent.md
var builtinSubagent string

//go:embed builtin/single-context.md
var builtinSingleContext string

// Built-in tier names.
const (
	TierSubagent      = "subagent"
	TierSingleContext = "single-context"
)

// Block is the result of one discipline resolution.
//
// Source is a human-readable marker such as "内置:single-context" or
// "配置:my-rules.md". It is empty when injection is explicitly disabled.
type Block struct {
	Text   string
	Source string
}

// defaultTier maps executor names to the built-in tier used when no override is configured.
// Add a row when a new executor is introduced.
var defaultTier = map[string]string{
	"opencode": TierSubagent,
	"claude":   TierSubagent,
	"codex":    TierSingleContext,
	"grok":     TierSingleContext,
}

// builtinFor returns the built-in discipline block for an executor.
//
// Unknown executors deliberately fall back to the single-context block. Giving a
// subagent-only instruction to an executor without that mechanism is unsafe, while
// a subagent executor can still follow the conservative single-context process.
func builtinFor(executor string) Block {
	if defaultTier[executor] == TierSubagent {
		return Block{Text: builtinSubagent, Source: "内置:" + TierSubagent}
	}
	return Block{Text: builtinSingleContext, Source: "内置:" + TierSingleContext}
}
