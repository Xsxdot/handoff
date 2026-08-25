// path.go —— 写文件目标路径的范围归属判定。
//
// 职责：
//   - 把可能是相对路径、可能经软链的目标路径归一化为真实绝对路径
//   - 判定它是否落在任务范围（Workdir、TaskDir 或 TaskTmpDir）的子树内
//
// 边界：
//   - 只读文件系统（EvalSymlinks 探测），不创建、不修改任何东西
//   - 不认识工具名、不做黑名单匹配
//
// 已知残余风险（TOCTOU）：判定通过后、executor 实际写入前，软链可能被换掉。
// 闭合它需要在 executor 侧持有文件句柄，而写入动作发生在 agent 进程里，
// 超出 handoff 的可控范围——spec §5.4 明确接受此风险。
package permgate

import (
	"fmt"
	"path/filepath"
	"strings"
)

// InScope 判定目标路径是否落在任务范围内。
//
// 参数：
//   - path: 目标路径，可为相对路径（按 scope.Workdir 解析）
//   - scope: 任务范围；其中为空的基准目录被跳过，不参与判定
//
// 返回：
//   - in: 是否落在范围内
//   - base: in=true 时命中的基准目录（归一化后），供日志说明「凭哪条放行」
//   - err: 路径归一化失败；调用方须按 fail-closed 处理为升级人工
//
// 注意：
//   - 用 filepath.Rel 判归属而非字符串前缀——strings.HasPrefix("/repo-evil/x",
//     "/repo") 为真，前缀匹配会把仓库外的路径判成内部
//   - 对已存在的最长前缀求 EvalSymlinks——目标文件常常尚不存在（Write 新建），
//     不解软链则 `ln -s ~ /repo/link` 之后写 /repo/link/.ssh/authorized_keys
//     直接绕过
func InScope(path string, scope Scope) (in bool, base string, err error) {
	p := path
	if !filepath.IsAbs(p) {
		p = filepath.Join(scope.Workdir, p)
	}
	p, err = filepath.Abs(p)
	if err != nil {
		return false, "", fmt.Errorf("绝对化目标路径 %q: %w", path, err)
	}
	p = resolveExistingPrefix(p)

	for _, b := range []string{scope.Workdir, scope.TaskDir, scope.TaskTmpDir} {
		if b == "" {
			continue
		}
		rb, aerr := filepath.Abs(b)
		if aerr != nil {
			return false, "", fmt.Errorf("绝对化基准目录 %q: %w", b, aerr)
		}
		rb = resolveExistingPrefix(rb)
		rel, rerr := filepath.Rel(rb, p)
		if rerr != nil {
			// 跨卷等无法求相对路径的情形：视作不在该基准内，继续判下一个
			continue
		}
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true, rb, nil
		}
	}
	return false, "", nil
}

// resolveExistingPrefix 对路径中「已存在的最长前缀」求 EvalSymlinks，
// 再把剩余不存在的部分接回去。
//
// 为什么不能直接 EvalSymlinks(p)：Write 创建新文件时目标路径尚不存在，
// EvalSymlinks 会直接报错，那样每一次新建文件都会走 fail-closed 升级人工。
//
// 到根都解不动时原样返回——此时没有软链可解，原路径即真实路径。
func resolveExistingPrefix(p string) string {
	rest := ""
	cur := p
	for {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			if rest == "" {
				return r
			}
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}
