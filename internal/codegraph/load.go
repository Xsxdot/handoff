// 本文件实现数据契约文件的加载：baseline、单个 diff、视图列表。
//
// 职责：读文件 + json.Unmarshal + 带路径上下文的错误
// 边界：不校验引用完整性（validate.go 的事）、不合并（merge.go 的事）
package codegraph

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LoadGraph 读取 repoRoot/codegraph/baseline.json。
// 文件不存在或 JSON 非法时返回带路径的错误——调用方（CLI/agentd）原文透出。
func LoadGraph(repoRoot string) (*Graph, error) {
	p := filepath.Join(repoRoot, "codegraph", "baseline.json")
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("读取基线 %s: %w", p, err)
	}
	var g Graph
	if err := json.Unmarshal(raw, &g); err != nil {
		return nil, fmt.Errorf("解析基线 %s: %w", p, err)
	}
	return &g, nil
}

// LoadDiff 读取 repoRoot/codegraph/diffs/<view>.json。view 是文件名（不含 .json）。
func LoadDiff(repoRoot, view string) (*Diff, error) {
	p := filepath.Join(repoRoot, "codegraph", "diffs", view+".json")
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("读取视图 %s: %w", p, err)
	}
	var d Diff
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("解析视图 %s: %w", p, err)
	}
	return &d, nil
}

// ListViews 列出 diffs 目录下的视图名（文件名去 .json，字典序）。
// 目录不存在返回空列表——大多数仓库只有基线，这不是错误。
func ListViews(repoRoot string) ([]string, error) {
	dir := filepath.Join(repoRoot, "codegraph", "diffs")
	ents, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("列视图目录 %s: %w", dir, err)
	}
	var out []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			out = append(out, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	sort.Strings(out)
	return out, nil
}
