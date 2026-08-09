// Package envfile 解析 handoff 的 env 文件，并把它换算成可注入子进程的环境变量。
//
// 职责：
//   - Parse：把 dotenv 形态的文本解析为有序 KV，值支持单层 $VAR/${VAR} 展开
//   - Resolver（resolver.go）：按 agent 名定位 <DataDir>/env/<文件名>，读盘并
//     返回 KEY=VALUE 切片
//
// 边界：
//   - 不是 shell：不做命令替换、不支持多行值、不支持行内注释（理由见 Parse 注释）
//   - 不管密钥：不加密、不接 secret 后端；值一律不进日志（本包只在 Resolver 里
//     打 key 名）
//   - 不启动进程：注入由各 adapter 自行完成（经 executor.StartReq.Env）
package envfile

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// maxEnvFileSize 是单个 env 文件的大小上限（64KiB）。
//
// 为什么要有上限：误把二进制文件配成 env 文件时，逐行解析会产出一堆垃圾变量名，
// 或者报一长串无意义的行号错误；一个上限把它变成一句可读的拒绝。
const maxEnvFileSize = 64 << 10

// keyRe 是合法环境变量名的形状。宽于 POSIX 但与主流 shell 一致。
var keyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// KV 是一条解析结果，按文件内首次出现的顺序排列。
type KV struct {
	Key   string
	Value string
}

// Parse 解析 env 文件内容。
//
// 参数：
//   - r: 文件内容
//   - lookup: 展开时的外部变量查找（生产传 os.LookupEnv）；nil 表示外部无变量
//
// 返回：
//   - kvs: 按首次出现顺序排列的键值对（重复键后者覆盖前者的值，位置保持在首次出现处）
//   - dups: 出现过重复定义的键名，供调用方打 WARN（本函数是纯函数，不打日志）
//   - err: 语法错误（带行号与原行）或超出大小上限
//
// 语法（完整规则见 spec §3）：
//   - 行尾 \r 先剥离（兼容 CRLF）；trim 后的空行与 # 开头行跳过
//   - 可选 `export ` 前缀；第一个 = 分割；key 须匹配 keyRe
//   - 值 trim 后：'...' 字面量不展开，"..." 与无引号都展开
//
// 为什么不支持行内注释：`HTTPS_PROXY=http://host/a#b` 里 # 是合法字符，支持行内
// 注释会把这类值静默吃掉半截——症状是「代理配了但连不上」，离根因隔了十万八千里。
//
// 为什么展开时文件内的键优先于外部环境：让文件自洽，读文件的人不必脑补外部环境
// 是什么。查不到的变量展开为空串（os.Expand 的默认行为）。
func Parse(r io.Reader, lookup func(string) (string, bool)) (kvs []KV, dups []string, err error) {
	// 多读 1 字节用于判定「是否超限」：正好等于上限时 LimitReader 读满但未越界
	b, err := io.ReadAll(io.LimitReader(r, maxEnvFileSize+1))
	if err != nil {
		return nil, nil, fmt.Errorf("读取 env 内容: %w", err)
	}
	if len(b) > maxEnvFileSize {
		return nil, nil, fmt.Errorf("env 文件超过大小上限 %d 字节（64KiB）", maxEnvFileSize)
	}

	idx := map[string]int{}     // key → 在 kvs 中的下标，用于重复键就地覆盖
	vals := map[string]string{} // 已解析键的当前值，供后续行展开使用
	// expand 对值做一次变量展开，查找顺序为「本文件已解析的键 → lookup → 空串」。
	// 只展开一次：展开结果里的 $ 不再二次展开，这是「不是 shell」的边界所在。
	expand := func(s string) string {
		return os.Expand(s, func(name string) string {
			if v, ok := vals[name]; ok {
				return v
			}
			if lookup != nil {
				if v, ok := lookup(name); ok {
					return v
				}
			}
			return ""
		})
	}

	for i, raw := range strings.Split(string(b), "\n") {
		lineNo := i + 1
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "export"); ok &&
			rest != "" && (rest[0] == ' ' || rest[0] == '\t') {
			line = strings.TrimSpace(rest)
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return nil, nil, fmt.Errorf("env 第 %d 行缺少 '='：%q", lineNo, line)
		}
		key = strings.TrimSpace(key)
		if !keyRe.MatchString(key) {
			return nil, nil, fmt.Errorf("env 第 %d 行键名非法 %q（须匹配 [A-Za-z_][A-Za-z0-9_]*）", lineNo, key)
		}
		val = strings.TrimSpace(val)
		switch {
		case len(val) >= 2 && strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'"):
			val = val[1 : len(val)-1] // 单引号：字面量，不展开
		case len(val) >= 2 && strings.HasPrefix(val, `"`) && strings.HasSuffix(val, `"`):
			val = expand(val[1 : len(val)-1])
		default:
			val = expand(val)
		}
		if i, dup := idx[key]; dup {
			kvs[i].Value = val // 就地覆盖：保持首次出现的位置，语义是「后者生效」
			vals[key] = val
			dups = append(dups, key)
			continue
		}
		idx[key] = len(kvs)
		vals[key] = val
		kvs = append(kvs, KV{Key: key, Value: val})
	}
	return kvs, dups, nil
}
