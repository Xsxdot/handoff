// files.go —— env 文件的列举与读写（控制台配置面用，B158）。
//
// 职责：
//   - List/Read/Write：<DataDir>/env 下**纯文件名**的查与改
//   - resolvePath：包级的纯文件名校验，与 Resolver 共用，判据只有一处
//
// 边界：
//   - **本层不打日志**：纯文件操作，错误一律 %w 带上下文，日志由 agentd 的
//     handler 层统一打（与 internal/discipline/files.go 同一条纪律）
//   - **不解析内容**：语法校验是 Parse 的事，调用方在写盘前自行调用；本层
//     连「这是不是一个 env 文件」都不判断
//   - **错误文本里绝不出现文件内容**：env 的值常是凭据，错误会进日志与响应体
//   - 不碰配置映射（那是 Resolver 与 config 的事）
//   - 不做删除与改名：改名会让配置里的映射静默指空（见 spec §1.1）
package envfile

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MaxFileSize 是单个 env 文件的大小上限（64 KiB），与 Parse 的判据同源。
const MaxFileSize = maxEnvFileSize

var (
	// ErrBadName 表示文件名不是「纯文件名」，调用方应答 400。
	ErrBadName = errors.New("env 文件名非法")
	// ErrTooLarge 表示正文超过 MaxFileSize，调用方应答 400。
	ErrTooLarge = errors.New("env 文件超过大小上限")
	// ErrExists 表示新建时同名文件已存在，调用方应答 409。
	ErrExists = errors.New("同名 env 文件已存在")
	// ErrBaseMismatch 表示前置哈希与磁盘现状不符，调用方应答 409 并回带现状。
	ErrBaseMismatch = errors.New("env 文件已被改动")
)

// FileInfo 是 env 目录下的一个文件（不含正文）。
type FileInfo struct {
	Name   string
	Size   int64
	SHA256 string
}

// List 列举 env 目录下的全部普通文件，按名字升序。
//
// 参数：
//   - dir: env 目录，通常取 Dir(cfg.DataDir)
//
// 返回：
//   - 文件列表（含大小与哈希）；目录不存在时返回空切片与 nil
//
// 注意：
//   - **目录不存在不是错误**：<DataDir>/env 没有任何东西自动创建，首次打开
//     设置页时它本来就不存在，报错会把「还没建」画成「读不了」
//   - 子目录与非普通文件跳过：env 文件只有一层，不递归
func List(dir string) ([]FileInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []FileInfo{}, nil
		}
		return nil, fmt.Errorf("读取 env 目录 %s: %w", dir, err)
	}
	out := make([]FileInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !e.Type().IsRegular() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("读取 env 文件 %s: %w", e.Name(), err)
		}
		out = append(out, FileInfo{Name: e.Name(), Size: int64(len(data)), SHA256: hashOf(data)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Read 读一个 env 文件的正文。
//
// 返回：
//   - 正文、sha256、字节数；文件不存在时错误可用 errors.Is(err, fs.ErrNotExist) 判定
//
// 注意：返回的正文**含值**。调用方只应在用户显式要求「编辑正文」时把它交出去；
// 默认视图走 Parse + 丢值的路径（见 agentd 的 keys 端点）。
func Read(dir, name string) (content, sha string, size int64, err error) {
	path, err := resolvePath(dir, name)
	if err != nil {
		return "", "", 0, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", 0, fmt.Errorf("读取 env 文件 %s: %w", path, err)
	}
	return string(data), hashOf(data), int64(len(data)), nil
}

// Write 写一个 env 文件，带前置哈希保护。
//
// 参数：
//   - baseSHA: 空串 = 新建（目标必须不存在）；非空 = 覆盖（须与磁盘现状一致）
//
// 返回：
//   - 新内容的 sha256 与字节数；调用方可直接拿 sha 当下一次写入的 base
//   - 冲突时返回**磁盘现状的哈希** + ErrBaseMismatch，供 409 响应体带上现状
//
// 注意：
//   - **本函数不做语法校验**。调用方须在此之前跑 Parse——先校验再落盘，
//     写坏的文件不该进磁盘（写进去了才发现，症状会拖到下一次派发）
//   - 目录不存在时以 0700 创建；文件 0600——env 里带凭据是常态，权限基线
//     不能松于 DataDir 下其余内容
func Write(dir, name, content, baseSHA string) (sha string, size int64, err error) {
	path, err := resolvePath(dir, name)
	if err != nil {
		return "", 0, err
	}
	if len(content) > MaxFileSize {
		return "", 0, fmt.Errorf("%w: %s 有 %d 字节，上限 %d", ErrTooLarge, name, len(content), MaxFileSize)
	}
	cur, statErr := os.ReadFile(path)
	switch {
	case statErr == nil && baseSHA == "":
		// 新建撞名必须显式失败，避免保存按钮把别人的文件静默覆盖。
		return hashOf(cur), int64(len(cur)), fmt.Errorf("%w: %s", ErrExists, name)
	case statErr == nil && hashOf(cur) != baseSHA:
		return hashOf(cur), int64(len(cur)), fmt.Errorf("%w: %s", ErrBaseMismatch, name)
	case statErr != nil && !os.IsNotExist(statErr):
		return "", 0, fmt.Errorf("读取 env 文件 %s: %w", path, statErr)
	case statErr != nil && baseSHA != "":
		// 带 base 却读不到：文件在编辑期间被删了，与哈希不符同属冲突语义。
		return "", 0, fmt.Errorf("读取 env 文件 %s: %w", path, statErr)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", 0, fmt.Errorf("创建 env 目录 %s: %w", dir, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", 0, fmt.Errorf("写入 env 文件 %s: %w", path, err)
	}
	return hashOf(content), int64(len(content)), nil
}

// resolvePath 把配置里的文件名换算为绝对路径，并拒绝一切非「纯文件名」的写法。
//
// 为什么只收纯文件名：一杜绝路径穿越（../../etc 之类），二保证 env 文件只有
// 一个家、不会散落各处——运维找配置时只需要看一个目录。
func resolvePath(dir, name string) (string, error) {
	if strings.ContainsRune(name, filepath.Separator) || strings.ContainsRune(name, '/') {
		return "", fmt.Errorf("%w: %q 不能含路径分隔符：只支持 %s 下的纯文件名", ErrBadName, name, dir)
	}
	if name == "" || name == "." || name == ".." {
		return "", fmt.Errorf("%w: %q：只支持 %s 下的纯文件名", ErrBadName, name, dir)
	}
	return filepath.Join(dir, name), nil
}

// hashOf 返回内容的 sha256 十六进制串（写入与列举共用，保证两处口径一致）。
func hashOf[T string | []byte](data T) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}
