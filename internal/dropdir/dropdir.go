// Package dropdir 把拖入的文件写到本机用户的 ~/.handoff/drop/ 收件箱。
//
// 职责：
//   - 按 basename 落盘，同名加 -2/-3 后缀，不覆盖已有文件
//   - 强制 32 MiB 未编码字节上限；超限整次拒绝，不留成品名
//   - 先写点开头临时文件再放到目标名，失败即删临时文件
//
// 边界：
//   - 不认 HTTP、不认 PTY、不认工作树。HOME 由调用方注入，本包不调 os.UserHomeDir
//   - 不列目录、不删除用户文件、不清理崩溃残留的点开头临时文件
//   - 不把文件内容写进日志
package dropdir

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// MaxBytes 是单文件未编码内容上限（32 MiB）。
const MaxBytes = 32 << 20

var (
	// ErrTooLarge 是内容超过 MaxBytes。
	ErrTooLarge = errors.New("文件超过 32 MiB")
	// ErrBadName 是文件名不合法（空、.、..）。
	ErrBadName = errors.New("文件名不合法")
	// ErrNoHome 是调用方没给出 HOME。
	ErrNoHome = errors.New("HOME 为空")
	// ErrNotFile 是调用方声明这不是普通文件（目录等）。
	ErrNotFile = errors.New("不是普通文件")
)

// putMu 把同一进程内的 Put 串起来，让「找空名 + 放到目标名」在本进程不互相覆盖。
// 一台机器通常只有一个 agentd；跨进程残留靠目标名 O_EXCL 式 link。
var putMu sync.Mutex

func log() *slog.Logger { return slog.Default() }

// Dir 返回该 HOME 下的 drop 收件箱路径。
func Dir(home string) string {
	return filepath.Join(home, ".handoff", "drop")
}

// Put 把 r 的内容写进 home/.handoff/drop/，返回实际绝对路径和写入字节数。
//
// 参数：
//   - home: 用户主目录（测试注入临时目录；生产由调用方 os.UserHomeDir）
//   - originalName: 用户给的文件名，本函数只取 basename
//   - r: 文件内容
//
// 返回：写成的绝对路径、字节数；失败时路径为空。
func Put(home, originalName string, r io.Reader) (string, int64, error) {
	putMu.Lock()
	defer putMu.Unlock()

	log().Info("drop 写入开始", "name", originalName)
	if home == "" {
		log().Warn("drop 写入拒绝：HOME 为空")
		return "", 0, ErrNoHome
	}
	base, err := sanitizeName(originalName)
	if err != nil {
		log().Warn("drop 写入拒绝：文件名不合法", "name", originalName, "cause", err)
		return "", 0, err
	}
	dir := Dir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log().Error("drop 创建目录失败", "dir", dir, "cause", err)
		return "", 0, fmt.Errorf("创建 drop 目录: %w", err)
	}

	data, err := io.ReadAll(io.LimitReader(r, MaxBytes+1))
	if err != nil {
		log().Error("drop 读取内容失败", "name", base, "cause", err)
		return "", 0, fmt.Errorf("读取内容: %w", err)
	}
	n := int64(len(data))
	if n > MaxBytes {
		log().Warn("drop 写入拒绝：超过上限", "name", base, "bytes", n, "limit", MaxBytes)
		return "", 0, fmt.Errorf("%w: %d", ErrTooLarge, n)
	}

	tmp, err := os.CreateTemp(dir, ".drop-*.tmp")
	if err != nil {
		log().Error("drop 创建临时文件失败", "dir", dir, "cause", err)
		return "", 0, fmt.Errorf("创建临时文件: %w", err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			if rmErr := os.Remove(tmpName); rmErr != nil {
				log().Warn("drop 清理临时文件失败", "tmp", tmpName, "cause", rmErr)
			}
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		log().Error("drop 写临时文件失败", "tmp", tmpName, "cause", err)
		return "", 0, fmt.Errorf("写临时文件: %w", err)
	}
	if err := tmp.Close(); err != nil {
		log().Error("drop 关闭临时文件失败", "tmp", tmpName, "cause", err)
		return "", 0, fmt.Errorf("关闭临时文件: %w", err)
	}

	dest, err := placeExclusive(tmpName, dir, base)
	if err != nil {
		log().Error("drop 放到目标名失败", "name", base, "cause", err)
		return "", 0, err
	}
	committed = true
	log().Info("drop 写入完成", "path", dest, "bytes", n)
	return dest, n, nil
}

// sanitizeName 只保留单层 basename，拒绝空、.、..。
func sanitizeName(original string) (string, error) {
	base := filepath.Base(original)
	if base == "" || base == "." || base == ".." {
		return "", fmt.Errorf("%w: %q", ErrBadName, original)
	}
	if strings.ContainsRune(base, os.PathSeparator) {
		return "", fmt.Errorf("%w: %q", ErrBadName, original)
	}
	return base, nil
}

// splitStemExt 按 spec：点开头且没有第二个点的整名当 stem（.env → .env-2）。
func splitStemExt(base string) (stem, ext string) {
	if strings.HasPrefix(base, ".") && strings.Count(base, ".") == 1 {
		return base, ""
	}
	ext = filepath.Ext(base)
	stem = strings.TrimSuffix(base, ext)
	if stem == "" {
		return base, ""
	}
	return stem, ext
}

// placeExclusive 把 tmp 放到 dir 下未被占用的 basename（冲突则 -2、-3…）。
func placeExclusive(tmp, dir, base string) (string, error) {
	candidates := []string{base}
	stem, ext := splitStemExt(base)
	for n := 2; n < 10000; n++ {
		candidates = append(candidates, fmt.Sprintf("%s-%d%s", stem, n, ext))
	}
	for _, name := range candidates {
		dest := filepath.Join(dir, name)
		if err := linkNoReplace(tmp, dest); err == nil {
			_ = os.Remove(tmp)
			return dest, nil
		} else if !isExist(err) {
			return "", fmt.Errorf("放到 %s: %w", dest, err)
		}
	}
	return "", fmt.Errorf("drop 目录同名过多: %s", base)
}

// linkNoReplace 把 old 链到 new；new 已存在则返回 exist 错误，不覆盖。
func linkNoReplace(old, new string) error {
	err := os.Link(old, new)
	if err == nil {
		return nil
	}
	if isExist(err) {
		return err
	}
	// 某些文件系统不支持硬链：回退到「不存在才 rename」。调用方持 putMu。
	if _, statErr := os.Lstat(new); statErr == nil {
		return os.ErrExist
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	return os.Rename(old, new)
}

func isExist(err error) bool {
	return err != nil && (os.IsExist(err) || errors.Is(err, os.ErrExist))
}
