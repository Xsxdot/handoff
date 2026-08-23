// 本文件负责节点产出路径模板渲染与 git diff 路径投影。
// 边界：只处理传入字符串，不访问网络、文件系统，也不猜测产出物。
package ledgerstep

import (
	"bufio"
	"strconv"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/ledger"
)

// RenderOutputPath 将工作流声明中的四个占位符渲染成一次派发确定的路径。
// 参数：template 为声明模板；card/node 提供卡号与节点名；now 提供派发日期。
// 返回：只做字符串替换；未知占位符原样保留，便于配置错误在 diff 校验时显式暴露。
func RenderOutputPath(template string, card ledger.Card, node ledger.NodeDef, now time.Time) string {
	return strings.NewReplacer(
		"{{CARD}}", card.ID,
		"{{CARD_LOWER}}", strings.ToLower(card.ID),
		"{{NODE}}", node.Name,
		"{{DATE}}", now.Format("2006-01-02"),
	).Replace(template)
}

// ChangedPaths 提取 git diff 中首次出现的仓内相对路径，并保持其出现顺序。
// 只接受 diff --git、rename from/to 和非 /dev/null 的 ---/+++ 记录，避免把
// 提交标题、作者、索引或 hunk 内容误当成产出路径。
func ChangedPaths(diff string) []string {
	paths := make([]string, 0)
	seen := make(map[string]struct{})
	add := func(raw string) {
		path := diffPath(raw)
		if path == "" {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}

	scanner := bufio.NewScanner(strings.NewReader(diff))
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "diff --git "):
			fields := diffHeaderFields(strings.TrimPrefix(line, "diff --git "))
			if len(fields) >= 2 {
				add(fields[0])
				add(fields[1])
			}
		case strings.HasPrefix(line, "rename from "):
			add(strings.TrimPrefix(line, "rename from "))
		case strings.HasPrefix(line, "rename to "):
			add(strings.TrimPrefix(line, "rename to "))
		case strings.HasPrefix(line, "--- "):
			add(strings.TrimPrefix(line, "--- "))
		case strings.HasPrefix(line, "+++ "):
			add(strings.TrimPrefix(line, "+++ "))
		}
	}
	return paths
}

// changedPathsText 保持缺产出物提示可读：空清单也必须明确显示。
func changedPathsText(paths []string) string {
	if len(paths) == 0 {
		return "（无）"
	}
	return strings.Join(paths, "\n")
}

func diffPath(raw string) string {
	path := strings.TrimSpace(raw)
	if tab := strings.IndexByte(path, '\t'); tab >= 0 {
		path = path[:tab]
	}
	if len(path) >= 2 && path[0] == '"' && path[len(path)-1] == '"' {
		if unquoted, err := strconv.Unquote(path); err == nil {
			path = unquoted
		}
	}
	if path == "/dev/null" {
		return ""
	}
	if strings.HasPrefix(path, "a/") || strings.HasPrefix(path, "b/") {
		return path[2:]
	}
	return path
}

func diffHeaderFields(raw string) []string {
	fields := make([]string, 0, 2)
	for len(strings.TrimSpace(raw)) > 0 && len(fields) < 2 {
		raw = strings.TrimLeft(raw, " \t")
		if raw == "" {
			break
		}
		if raw[0] == '"' {
			end := 1
			escaped := false
			for end < len(raw) {
				if raw[end] == '"' && !escaped {
					break
				}
				if raw[end] == '\\' && !escaped {
					escaped = true
				} else {
					escaped = false
				}
				end++
			}
			if end >= len(raw) {
				break
			}
			fields = append(fields, raw[:end+1])
			raw = raw[end+1:]
			continue
		}
		end := strings.IndexAny(raw, " \t")
		if end < 0 {
			fields = append(fields, raw)
			break
		}
		fields = append(fields, raw[:end])
		raw = raw[end:]
	}
	return fields
}
