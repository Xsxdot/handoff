// runshell.go —— handoff run 的 shell 解析。
//
// 职责：为 RunCmd 选出执行 `-c <cmdline>` 的 shell 可执行文件路径。
//
// 边界：
//   - 只做解析，不执行、不拼参数（那是 RunCmd 的事）
//   - 不做 shell 方言转换：本文件的全部意义就是让协调者写的 unix 风格命令
//     在两个平台上是同一句话
package agentd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// runShellCandidates 返回本平台上 sh 的已知安装位置（绝对路径，按优先级）。
//
// 参数：goos 取 runtime.GOOS；抽成参数是为了让 Windows 分支在 mac/linux 上测得到。
//
// 返回：非 Windows 恒为空——那些平台上 sh 一定在 PATH 上，不需要兜底。
//
// 为什么必须有兜底：Git for Windows 的默认安装只把 Git\cmd 加进 PATH，而 sh.exe
// 住在 Git\bin，**默认不在 PATH 上**（真机实测）。只靠 LookPath 会在绝大多数正常
// 安装的机器上失败。这与 internal/pathenv 的「已知安装目录兜底」是同一个模式。
func runShellCandidates(goos string) []string {
	if goos != "windows" {
		return nil
	}
	var out []string
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA"} {
		root := os.Getenv(env)
		if root == "" {
			continue
		}
		if env == "LOCALAPPDATA" {
			out = append(out, filepath.Join(root, "Programs", "Git", "bin", "sh.exe"))
			continue
		}
		out = append(out, filepath.Join(root, "Git", "bin", "sh.exe"))
	}
	// 环境变量缺失时的硬兜底：默认安装位置是确定的。
	out = append(out, `C:\Program Files\Git\bin\sh.exe`, `C:\Program Files (x86)\Git\bin\sh.exe`)
	return dedupStrings(out)
}

// dedupStrings 按首次出现顺序去重。
func dedupStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := in[:0:0]
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// resolveRunShell 解析 run 命令要用的 shell。
//
// 参数：
//   - goos: 平台标识（生产路径传 runtime.GOOS）
//   - lookPath: PATH 查找函数（生产路径传 exec.LookPath）
//   - stat: 文件存在性判定（生产路径传 statFile）
//
// 返回：
//   - shell 的路径或名字，可直接交给 exec.CommandContext
//   - error: 只在 Windows 上找不到任何 sh 时返回，文案指出装什么能修
//
// 为什么找不到时**硬失败**而不是降级到 cmd 或 PowerShell：那会让协调者写的
// unix 风格命令（管道、$(…)、&&）以难以理解的方式半跑，排障成本远高于一条
// 明确的「请装 Git for Windows」。
func resolveRunShell(goos string, lookPath func(string) (string, error), stat func(string) error) (string, error) {
	if goos != "windows" {
		return "sh", nil
	}
	if p, err := lookPath("sh"); err == nil && p != "" {
		log().Info("run 的 shell 解析自 PATH", "sh", p)
		return p, nil
	}
	cands := runShellCandidates(goos)
	for _, c := range cands {
		if err := stat(c); err == nil {
			log().Info("run 的 shell 解析自已知安装目录", "sh", c)
			return c, nil
		}
	}
	log().Error("找不到 sh，run 命令无法执行", "candidates", cands)
	return "", fmt.Errorf("找不到 sh：请在本机安装完整的 Git for Windows"+
		"（MinGit 不带 sh），已查找 PATH 与 %v", cands)
}

// statFile 是 resolveRunShell 的生产存在性判据。
func statFile(p string) error {
	_, err := os.Stat(p)
	return err
}

// runShell 是 RunCmd 的调用入口，把生产依赖接上。
func runShell() (string, error) {
	return resolveRunShell(runtime.GOOS, exec.LookPath, statFile)
}
