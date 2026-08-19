// files.go —— 纪律块文件的列举与读写（控制台配置面用）。
//
// 职责：
//   - List/Read/Write：<DataDir>/discipline 下**纯文件名**的查与改
//   - 与 Resolver 共用 resolvePath 与 maxBlockSize，判据只有一处
//
// 边界：
//   - **本层不打日志**：纯文件操作，错误一律 %w 带上下文，日志由 agentd 的
//     handler 层统一打（与 internal/store 同一条纪律）
//   - 不理解纪律内容、不碰配置映射（那是 Resolver 与 config 的事）
//   - 不做删除与改名：改名会让配置里的映射静默指空（见 spec §1.1）
package discipline

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// MaxBlockSize 是单个纪律块文件的大小上限（64 KiB），与 Resolver 读盘时的判据同源。
const MaxBlockSize = maxBlockSize

var (
	// ErrBadName 表示文件名不是「纯文件名」，调用方应答 400。
	ErrBadName = errors.New("纪律块文件名非法")
	// ErrTooLarge 表示正文超过 MaxBlockSize，调用方应答 400。
	ErrTooLarge = errors.New("纪律块文件超过大小上限")
	// ErrExists 表示新建时同名文件已存在，调用方应答 409。
	ErrExists = errors.New("同名纪律块文件已存在")
	// ErrBaseMismatch 表示前置哈希与磁盘现状不符，调用方应答 409 并回带现状。
	ErrBaseMismatch = errors.New("纪律块文件已被改动")
)

// FileInfo 是纪律块目录下的一个文件（不含正文）。
type FileInfo struct {
	Name   string
	Size   int64
	SHA256 string
}

// List 列举纪律块目录下的全部普通文件，按名字升序。
//
// 参数：
//   - dir: 纪律块目录，通常取 Dir(cfg.DataDir)
//
// 返回：
//   - 文件列表（含大小与哈希）；目录不存在时返回空切片与 nil
//
// 注意：
//   - **目录不存在不是错误**：<DataDir>/discipline 没有任何东西自动创建，
//     首次打开设置页时它本来就不存在，报错会把「还没建」画成「读不了」
//   - 子目录与非普通文件跳过：纪律块只有一层，不递归
func List(dir string) ([]FileInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []FileInfo{}, nil
		}
		return nil, fmt.Errorf("读取纪律块目录 %s: %w", dir, err)
	}
	out := make([]FileInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !e.Type().IsRegular() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("读取纪律块文件 %s: %w", e.Name(), err)
		}
		out = append(out, FileInfo{Name: e.Name(), Size: int64(len(data)), SHA256: hashOf(data)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Read 读一个纪律块文件的正文。
//
// 返回：
//   - 正文、sha256、字节数；文件不存在时错误可用 errors.Is(err, fs.ErrNotExist) 判定
func Read(dir, name string) (content, sha string, size int64, err error) {
	path, err := resolvePath(dir, name)
	if err != nil {
		return "", "", 0, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", 0, fmt.Errorf("读取纪律块文件 %s: %w", path, err)
	}
	return string(data), hashOf(data), int64(len(data)), nil
}

// Write 写一个纪律块文件，带前置哈希保护。
//
// 参数：
//   - baseSHA: 空串 = 新建（目标必须不存在）；非空 = 覆盖（须与磁盘现状一致）
//
// 返回：
//   - 新内容的 sha256 与字节数；调用方可直接拿 sha 当下一次写入的 base
//   - 冲突时返回**磁盘现状的哈希** + ErrBaseMismatch，供 409 响应体带上现状
//
// 注意：
//   - 目录不存在时以 0700 创建；文件 0600——纪律块虽不含密钥，但与 DataDir
//     下其余内容保持同一权限基线，不给「有的能被同机别的账号读」留缝
func Write(dir, name, content, baseSHA string) (sha string, size int64, err error) {
	path, err := resolvePath(dir, name)
	if err != nil {
		return "", 0, err
	}
	if len(content) > MaxBlockSize {
		return "", 0, fmt.Errorf("%w: %s 有 %d 字节，上限 %d", ErrTooLarge, name, len(content), MaxBlockSize)
	}
	cur, statErr := os.ReadFile(path)
	switch {
	case statErr == nil && baseSHA == "":
		// 新建撞名必须显式失败，避免保存按钮把别人的文件静默覆盖。
		return hashOf(cur), int64(len(cur)), fmt.Errorf("%w: %s", ErrExists, name)
	case statErr == nil && hashOf(cur) != baseSHA:
		return hashOf(cur), int64(len(cur)), fmt.Errorf("%w: %s", ErrBaseMismatch, name)
	case statErr != nil && !os.IsNotExist(statErr):
		return "", 0, fmt.Errorf("读取纪律块文件 %s: %w", path, statErr)
	case statErr != nil && baseSHA != "":
		// 带 base 却读不到：文件在编辑期间被删了，与哈希不符同属冲突语义。
		return "", 0, fmt.Errorf("读取纪律块文件 %s: %w", path, statErr)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", 0, fmt.Errorf("创建纪律块目录 %s: %w", dir, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", 0, fmt.Errorf("写入纪律块文件 %s: %w", path, err)
	}
	return hashOf(content), int64(len(content)), nil
}

// hashOf 返回内容的 sha256 十六进制串（写入与列举共用，保证两处口径一致）。
func hashOf[T string | []byte](data T) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}
