// clicheck.go —— CLI 侧的限流版本检查与提示行。
//
// 边界：
//   - **CLI 永远不自动替换自己**（D13）：CLI 是交互工具，在用户敲命令时不知情地
//     换掉自己不合适，脚本化场景下行为还会突变。这里只打一行提示
//   - 读缓存的失败一律静默：这条路径挂在**每一条** handoff 命令上，
//     一个坏掉的缓存文件让所有命令都吐错误，代价远大于少提示一次更新
//   - 本文件持有全仓唯一的版本比较入口 CompareVersion
package selfupdate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// cliCheckInterval 是两次 CLI 版本检查之间的最小间隔。
//
// 24h：CLI 提示的价值是「让人知道有新版了」，天级足够；查得更勤只会
// 增加 GitHub 限流压力，而限流一旦触发，agentd 的自动更新也会跟着失败。
const cliCheckInterval = 24 * time.Hour

// CLICheck 是 CLI 侧版本检查的缓存。
type CLICheck struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest"`
}

// CLICheckPath 返回缓存文件路径。
func CLICheckPath(dataDir string) string {
	return filepath.Join(dataDir, "update", "cli-check.json")
}

// LoadCLICheck 读缓存。
//
// 返回：
//   - 缓存；**缺失或损坏一律返回 nil，不返回错误**（理由见文件头注释）
func LoadCLICheck(dataDir string) *CLICheck {
	b, err := os.ReadFile(CLICheckPath(dataDir))
	if err != nil {
		return nil
	}
	var c CLICheck
	if err := json.Unmarshal(b, &c); err != nil {
		return nil
	}
	return &c
}

// SaveCLICheck 写缓存，自动建目录。
func SaveCLICheck(dataDir string, c *CLICheck) error {
	path := CLICheckPath(dataDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("建目录 %s: %w", filepath.Dir(path), err)
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化: %w", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("写 %s: %w", path, err)
	}
	return nil
}

// CLICheckStale 判断是否该重新检查。nil（没查过）视为过期。
func CLICheckStale(c *CLICheck, now time.Time) bool {
	if c == nil {
		return true
	}
	return now.Sub(c.CheckedAt) >= cliCheckInterval
}

// NotifyLine 生成提示行；不需要提示时返回空串。
//
// 参数：
//   - c: 缓存（可为 nil）
//   - current: 本进程版本；**空串表示非 release 构建**
//
// 注意：
//   - current 为空时一律不提示。开发时每条命令都被劝「有新版」是纯噪音，
//     而且本地构建本来就不该被劝去装 release
//   - **只有缓存里的版本严格新于当前版本才提示**。缓存最长会陈 24h，
//     刚升级完的机器读到的仍是升级前那次检查的结果；只判「不相等」就会
//     反过来劝人降级。这不是假设：v0.1.1 发布当天实测被劝
//     「有新版本 v0.1.0（当前 v0.1.1）」，且这条错误提示会挂满一整个刷新周期
func NotifyLine(c *CLICheck, current string) string {
	if c == nil || c.Latest == "" || current == "" || c.Latest == current {
		return ""
	}
	if cmp, ok := CompareVersion(c.Latest, current); !ok || cmp <= 0 {
		return ""
	}
	return fmt.Sprintf("有新版本 %s（当前 %s），运行 handoff upgrade --now 升级", c.Latest, current)
}

// CompareVersion 比较两个版本号，是**全仓唯一**的版本比较入口。
//
// 参数：
//   - a, b: 形如 v0.1.2 或 v0.1.2-rc3 的标签，前缀 v 可有可无，
//     +构建元数据 按 semver 规则忽略
//
// 返回：
//   - a 小于/等于/大于 b 时分别为 -1/0/1
//   - 核心段不是三段非负整数时 ok 为 false，此时第一个返回值无意义
//
// 注意：
//   - 三段核心号都按整数比，不能用字典序——字典序会判定 v0.10.0 比 v0.9.0 旧
//   - **预发布号一律早于同号正式版**（semver 规则）：v0.3.0-rc11 < v0.3.0
//   - **标识符内部按「自然序」比，这是对严格 semver 的刻意偏离。**
//     semver 规定字母数字标识符按 ASCII 字典序比，那样 "rc11" < "rc8"
//     （因为 '1' < '8'）——正好和我们要的相反。本仓库的 tag 用 -rcN 而不是
//     semver 推荐的 -rc.N，所以这里把数字段与非数字段拆开、数字段按数值比，
//     于是 rc2 < rc8 < rc11。点分形态（-rc.1）照样正确
//   - **不要在别处另写一份。** 消费者已有三个（CLI 提示、桌面同步、桌面
//     通知）；本函数被写错过一次（B59 验收抓出的反向提示），三份实现意味着
//     错一次要修三处、而且一定有一处会被漏掉
//
// 边界：本函数只回答「谁新谁旧」。**「该不该给用户装这个版本」是另一回事**
// ——install.sh 的 latest_tag 只认 releases/latest 的重定向，GitHub 本就把
// 预发布排除在外，所以放宽这里不会让谁被自动装上一个 rc。
func CompareVersion(a, b string) (int, bool) {
	pa, ok := parseVersion(a)
	if !ok {
		return 0, false
	}
	pb, ok := parseVersion(b)
	if !ok {
		return 0, false
	}
	for i := range pa.core {
		switch {
		case pa.core[i] < pb.core[i]:
			return -1, true
		case pa.core[i] > pb.core[i]:
			return 1, true
		}
	}
	return comparePre(pa.pre, pb.pre), true
}

// parsedVersion 是拆开后的版本号。pre 为空表示正式版。
type parsedVersion struct {
	core [3]int
	pre  []string
}

// parseVersion 把 v?X.Y.Z[-预发布][+构建] 拆开；形态不符时 ok 为 false。
func parseVersion(v string) (parsedVersion, bool) {
	var out parsedVersion
	v = strings.TrimPrefix(v, "v")
	// 构建元数据不参与比较（semver 规则），先剪掉
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	core := v
	// 只按**第一个**减号切：预发布段内部允许再有减号
	if i := strings.IndexByte(v, '-'); i >= 0 {
		core = v[:i]
		pre := v[i+1:]
		if pre == "" {
			// 尾随的减号不算合法预发布段
			return out, false
		}
		out.pre = strings.Split(pre, ".")
		for _, id := range out.pre {
			if id == "" {
				return out, false
			}
		}
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out.core[i] = n
	}
	return out, true
}

// comparePre 比较两个预发布标识符列表，返回 -1/0/1。
//
// 规则（前两条是 semver 原文，第三条是本仓库的偏离，理由见 CompareVersion）：
//   - 有预发布段的一方**更旧**；两边都没有则相等
//   - 逐个标识符比；全部相等时标识符少的一方更旧
//   - 单个标识符按自然序比（数字段按数值），而非 ASCII 字典序
func comparePre(a, b []string) int {
	switch {
	case len(a) == 0 && len(b) == 0:
		return 0
	case len(a) == 0:
		// a 是正式版，b 是预发布 → a 更新
		return 1
	case len(b) == 0:
		return -1
	}
	for i := 0; i < len(a) && i < len(b); i++ {
		if c := compareIdent(a[i], b[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

// compareIdent 按自然序比较单个标识符：把数字段与非数字段交替拆开，
// 数字段按数值比、非数字段按字典序比。
//
// 为什么不用字典序：本仓库的 tag 形如 -rc8 / -rc11，字典序会判 rc11 比 rc8 旧。
func compareIdent(a, b string) int {
	for a != "" || b != "" {
		if a == "" {
			return -1
		}
		if b == "" {
			return 1
		}
		da, ra := takeRun(a)
		db, rb := takeRun(b)
		aNum, bNum := isDigits(da), isDigits(db)
		switch {
		case aNum && bNum:
			// 数值比。段长有上限（tag 不会有天文数字），Atoi 失败不可能发生，
			// 但真发生时退回字典序而不是 panic
			na, ea := strconv.Atoi(da)
			nb, eb := strconv.Atoi(db)
			if ea != nil || eb != nil {
				if c := strings.Compare(da, db); c != 0 {
					return c
				}
			} else if na != nb {
				if na < nb {
					return -1
				}
				return 1
			}
		case aNum != bNum:
			// 数字段早于字母段（semver：纯数字标识符低于字母数字标识符）
			if aNum {
				return -1
			}
			return 1
		default:
			if c := strings.Compare(da, db); c != 0 {
				return c
			}
		}
		a, b = ra, rb
	}
	return 0
}

// takeRun 从头切下一段同类字符（全数字或全非数字），返回该段与剩余部分。
func takeRun(s string) (string, string) {
	if s == "" {
		return "", ""
	}
	digit := s[0] >= '0' && s[0] <= '9'
	for i := 1; i < len(s); i++ {
		if (s[i] >= '0' && s[i] <= '9') != digit {
			return s[:i], s[i:]
		}
	}
	return s, ""
}

// isDigits 判断整段是否全为十进制数字（空串为否）。
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
