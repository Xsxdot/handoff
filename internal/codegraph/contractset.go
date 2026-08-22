package codegraph

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// SetContract creates or patches the From→To contract. Nil slices and zero budget
// mean that the corresponding caller field was omitted. Validation always runs before writing.
// The whole target is rewritten with json.MarshalIndent: the first set may create
// a one-time formatting diff, and subsequent writes are stable by design.
func SetContract(repoRoot string, c Contract) (before, after *Contract, err error) {
	return setContract(repoRoot, c, c.Entries != nil, c.Interfaces != nil, c.LegacyBudget != 0)
}

// SetContractWithPresence is the CLI bridge for flags whose zero value is meaningful.
// The presence booleans are not part of Contract because they are invocation metadata,
// not target.json data.
func SetContractWithPresence(repoRoot string, c Contract, entriesSet, interfacesSet, budgetSet bool) (before, after *Contract, err error) {
	return setContract(repoRoot, c, entriesSet, interfacesSet, budgetSet)
}

func setContract(repoRoot string, c Contract, entriesSet, interfacesSet, budgetSet bool) (before, after *Contract, err error) {
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
		if entriesSet {
			updated.Entries = append([]string(nil), c.Entries...)
		}
		if interfacesSet {
			updated.Interfaces = append([]string(nil), c.Interfaces...)
		}
		if budgetSet {
			updated.LegacyBudget = c.LegacyBudget
		}
		t.Contracts[idx] = updated
	} else {
		created := Contract{From: c.From, To: c.To}
		if entriesSet {
			created.Entries = append([]string(nil), c.Entries...)
		}
		if interfacesSet {
			created.Interfaces = append([]string(nil), c.Interfaces...)
		}
		if budgetSet {
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
	return c
}
