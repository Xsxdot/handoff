package codegraph

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// SetContract creates or patches the From→To contract. Nil slices and zero budget
// mean that the corresponding caller field was omitted; the explicit Set markers
// let the CLI distinguish --budget 0. Validation always runs before writing.
// The whole target is rewritten with json.MarshalIndent: the first set may create
// a one-time formatting diff, and subsequent writes are stable by design.
func SetContract(repoRoot string, c Contract) (before, after *Contract, err error) {
	t, err := LoadTarget(repoRoot)
	if err != nil {
		return nil, nil, err
	}
	idx := -1
	for i := range t.Contracts {
		if t.Contracts[i].From == c.From && t.Contracts[i].To == c.To {
			idx = i
			break
		}
	}
	if idx >= 0 {
		old := cleanContract(t.Contracts[idx])
		before = &old
		updated := old
		if c.EntriesSet || c.Entries != nil {
			updated.Entries = append([]string(nil), c.Entries...)
		}
		if c.InterfacesSet || c.Interfaces != nil {
			updated.Interfaces = append([]string(nil), c.Interfaces...)
		}
		if c.LegacyBudgetSet || c.LegacyBudget != 0 {
			updated.LegacyBudget = c.LegacyBudget
		}
		t.Contracts[idx] = updated
	} else {
		created := Contract{From: c.From, To: c.To}
		if c.EntriesSet || c.Entries != nil {
			created.Entries = append([]string(nil), c.Entries...)
		}
		if c.InterfacesSet || c.Interfaces != nil {
			created.Interfaces = append([]string(nil), c.Interfaces...)
		}
		if c.LegacyBudgetSet || c.LegacyBudget != 0 {
			created.LegacyBudget = c.LegacyBudget
		}
		t.Contracts = append(t.Contracts, created)
		idx = len(t.Contracts) - 1
	}

	if issues := ValidateTarget(t); len(issues) > 0 {
		return before, nil, fmt.Errorf("目标图校验失败: %v", issues)
	}
	raw, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return before, nil, fmt.Errorf("编码目标图: %w", err)
	}
	path := filepath.Join(repoRoot, "codegraph", "target.json")
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		return before, nil, fmt.Errorf("写回目标图 %s: %w", path, err)
	}
	updated := cleanContract(t.Contracts[idx])
	return before, &updated, nil
}

func cleanContract(c Contract) Contract {
	c.EntriesSet = false
	c.InterfacesSet = false
	c.LegacyBudgetSet = false
	return c
}
