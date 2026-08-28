// Command codegraph-flows adds bounded Go control-flow data to an existing
// baseline. It changes only flows and the scan metadata; nodes and edges are
// read-only inputs, and TypeScript is outside this command's boundary.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const flowGenerator = "codegraph-flows-b277-go"

func main() {
	repo := flag.String("repo", ".", "repository root")
	flag.Parse()
	if err := scanRepository(*repo, slog.Default()); err != nil {
		slog.Default().Error("Go flow 扫描失败", "repo", *repo, "cause", err)
		os.Exit(1)
	}
}

func scanRepository(repoRoot string, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	baselinePath := filepath.Join(repoRoot, "codegraph", "baseline.json")
	top, graph, err := readBaseline(baselinePath)
	if err != nil {
		return err
	}
	seeds := SeedGoSeams(graph)
	logger.Info("Go flow 扫描种子已确定", "count", len(seeds))

	flows := make(map[string]Flow)
	pending := append([]string(nil), seeds...)
	for round := 1; round <= 2 && len(pending) > 0; round++ {
		logger.Info("Go flow 扫描轮次开始", "round", round, "ids", len(pending))
		newFlows := Extract(graph, pending, repoRoot)
		for id, flow := range newFlows {
			flows[id] = flow
		}
		pending = implementationMethods(graph, newFlows, flows)
		logger.Info("Go flow 扫描轮次完成", "round", round, "written", len(newFlows), "next_implementations", len(pending))
	}

	commit, err := gitShortCommit(repoRoot)
	if err != nil {
		return err
	}
	if err := updateBaseline(top, flows, commit, time.Now().Format("2006-01-02")); err != nil {
		return err
	}
	if err := writeBaseline(baselinePath, top); err != nil {
		return err
	}
	logger.Info("Go flow baseline 写出成功", "path", baselinePath, "seeds", len(seeds), "flows", len(flows), "commit", commit)
	return nil
}

func readBaseline(path string) (map[string]json.RawMessage, *Graph, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read baseline %s: %w", path, err)
	}
	top := make(map[string]json.RawMessage)
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, nil, fmt.Errorf("decode baseline %s: %w", path, err)
	}
	var graph Graph
	if err := json.Unmarshal(data, &graph); err != nil {
		return nil, nil, fmt.Errorf("decode graph fields %s: %w", path, err)
	}
	if graph.Nodes == nil || graph.Containers == nil {
		return nil, nil, errors.New("baseline missing nodes or containers")
	}
	return top, &graph, nil
}

func updateBaseline(top map[string]json.RawMessage, flows map[string]Flow, commit, scannedAt string) error {
	meta := make(map[string]any)
	if raw, ok := top["meta"]; ok {
		if err := json.Unmarshal(raw, &meta); err != nil {
			return fmt.Errorf("decode baseline meta: %w", err)
		}
	}
	meta["scannedAt"] = scannedAt
	meta["generator"] = flowGenerator
	meta["commit"] = commit
	metaRaw, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("encode baseline meta: %w", err)
	}
	flowsRaw, err := json.Marshal(flows)
	if err != nil {
		return fmt.Errorf("encode baseline flows: %w", err)
	}
	top["meta"] = metaRaw
	top["flows"] = flowsRaw
	return nil
}

func writeBaseline(path string, top map[string]json.RawMessage) error {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(top); err != nil {
		return fmt.Errorf("encode baseline %s: %w", path, err)
	}
	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write baseline %s: %w", path, err)
	}
	return nil
}

func gitShortCommit(repoRoot string) (string, error) {
	cmd := exec.Command("git", "-C", repoRoot, "rev-parse", "--short", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse --short HEAD: %w", err)
	}
	commit := strings.TrimSpace(string(out))
	if commit == "" {
		return "", errors.New("git rev-parse --short HEAD returned empty commit")
	}
	return commit, nil
}

func implementationMethods(g *Graph, extracted map[string]Flow, existing map[string]Flow) []string {
	interfaces := make(map[string]bool)
	for _, flow := range extracted {
		for _, step := range flow.Steps {
			if step.Kind == "call" && step.Iface {
				interfaces[step.To] = true
			}
		}
	}
	if len(interfaces) == 0 {
		return nil
	}
	var ids []string
	for _, pair := range g.Implements {
		if len(pair) != 2 || !interfaces[pair[1]] {
			continue
		}
		impl, implOK := g.Nodes[pair[0]]
		iface, ifaceOK := g.Nodes[pair[1]]
		if !implOK || !ifaceOK {
			continue
		}
		if impl.Kind == "func" {
			if strings.HasSuffix(impl.File, ".go") {
				ids = appendImplementationID(ids, existing, pair[0])
			}
			continue
		}
		if impl.Kind != "model" || !strings.HasSuffix(impl.File, ".go") {
			continue
		}
		methodNames := make(map[string]bool, len(iface.Fields))
		for _, field := range iface.Fields {
			if len(field) > 0 {
				methodNames[shortSymbolName(field[0])] = true
			}
		}
		if len(methodNames) == 0 {
			continue
		}
		for id, candidate := range g.Nodes {
			if candidate.Kind != "func" || !strings.HasSuffix(candidate.File, ".go") {
				continue
			}
			candidateContainer := g.Containers[candidate.Container]
			if filepath.Dir(candidate.File) != filepath.Dir(impl.File) || containerTypeName(candidateContainer) != impl.Name || !methodNames[shortSymbolName(candidate.Name)] {
				continue
			}
			ids = appendImplementationID(ids, existing, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func containerTypeName(container Container) string {
	label := strings.TrimSpace(container.Label)
	if i := strings.LastIndexByte(label, '.'); i >= 0 {
		return label[i+1:]
	}
	return label
}

func appendImplementationID(ids []string, existing map[string]Flow, id string) []string {
	if _, alreadyExtracted := existing[id]; alreadyExtracted {
		return ids
	}
	for _, current := range ids {
		if current == id {
			return ids
		}
	}
	return append(ids, id)
}
