// install.go —— 下载、校验、自检、原子替换。
//
// 边界：
//   - 临时文件**必须**落在目标二进制的同目录：os.Rename 的原子性只在同一
//     文件系统内成立，从 /tmp rename 到 /usr/local/bin 会因跨设备直接失败
//   - 任何一步失败都清干净临时文件：留一份坏二进制在二进制目录里，
//     下一轮可能被误当成已就绪的 pending
//   - 不做自动回滚（D10）：只把旧二进制留成 .prev，回退是人工命令
package release

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// maxAssetBytes 是单个资产的读取上限（100MiB）。
//
// handoff 的二进制约 20MB，100MiB 给足余量；上限本身是防线——被劫持或
// 出错的响应不该把内存吃光。
const maxAssetBytes = 100 << 20

// selfCheckTimeout 是新二进制自检的时间上限。
//
// `handoff version` 不读配置不联网，正常是毫秒级；10s 只是防止一个坏掉的
// 二进制挂住不返回，把更新循环卡死。
const selfCheckTimeout = 10 * time.Second

// TempName 返回某版本的临时文件名。
//
// 前导点让它在目录列表里不显眼；带上 tag 使多次尝试不同版本时互不覆盖。
func TempName(tag string) string { return ".handoff.new-" + tag }

// PrevPath 返回某目标路径对应的旧二进制留存路径。
func PrevPath(target string) string { return target + ".prev" }

// Installer 执行下载与安装。
type Installer struct {
	HTTP *http.Client
	Log  *slog.Logger
}

// NewInstaller 构造默认 installer（10 分钟超时，覆盖慢网下的 20MB 下载）。
func NewInstaller(log *slog.Logger) *Installer {
	return &Installer{HTTP: &http.Client{Timeout: 10 * time.Minute}, Log: log}
}

// Fetch 下载本平台资产、校验、解包、自检，返回可供 Activate 的临时文件路径。
//
// 参数：
//   - ctx: 上下文
//   - rel: 目标发布
//   - destDir: 临时文件落点，**必须**与目标二进制同目录
//
// 返回：
//   - 临时二进制的完整路径（已 chmod 0755 并通过自检）
//   - 错误：缺资产、下载失败、校验不过、解包失败、自检不过
//
// 注意：
//   - 任何一步失败都会把临时文件删掉，不留残件
func (i *Installer) Fetch(ctx context.Context, rel Release, destDir string) (string, error) {
	goos, goarch := CurrentPlatform()
	asset, ok := rel.AssetFor(goos, goarch)
	if !ok {
		return "", fmt.Errorf("发布 %s 没有 %s/%s 的资产（%s）", rel.Tag, goos, goarch, AssetName(rel.Tag, goos, goarch))
	}
	ck, ok := rel.Checksums()
	if !ok {
		// 没有校验和就没法验完整性。宁可不更新，也不装一个来路不明的二进制
		return "", fmt.Errorf("发布 %s 没有 %s，无法校验完整性", rel.Tag, ChecksumsName)
	}

	i.Log.Info("开始下载新版本", "tag", rel.Tag, "asset", asset.Name, "url", asset.URL)
	tgz, err := i.get(ctx, asset.URL)
	if err != nil {
		return "", fmt.Errorf("下载 %s: %w", asset.Name, err)
	}
	sums, err := i.get(ctx, ck.URL)
	if err != nil {
		return "", fmt.Errorf("下载 %s: %w", ChecksumsName, err)
	}

	want, err := sumFor(string(sums), asset.Name)
	if err != nil {
		return "", err
	}
	got := sha256.Sum256(tgz)
	if hex.EncodeToString(got[:]) != want {
		// 不重试：完整性失败重试只会重下同一份坏数据（spec §4.7）
		return "", fmt.Errorf("sha256 校验不通过（期望 %s，实得 %s）", want, hex.EncodeToString(got[:]))
	}
	i.Log.Info("校验通过", "tag", rel.Tag, "sha256", want)

	tmp := filepath.Join(destDir, TempName(rel.Tag))
	// 从这里往后任何失败都要清干净
	cleanup := func() {
		if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
			i.Log.Warn("清理临时文件失败", "path", tmp, "cause", err)
		}
	}
	if err := extractBinary(tgz, tmp); err != nil {
		cleanup()
		return "", fmt.Errorf("解包 %s: %w", asset.Name, err)
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		cleanup()
		return "", fmt.Errorf("置可执行位 %s: %w", tmp, err)
	}
	if err := i.selfCheck(tmp, rel.Tag); err != nil {
		cleanup()
		return "", err
	}
	i.Log.Info("新版本已就绪", "tag", rel.Tag, "path", tmp)
	return tmp, nil
}

// get 取一个 URL 的全部内容，带大小上限。
func (i *Installer) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := i.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("返回 %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxAssetBytes))
}

// sumFor 从 checksums.txt 里取某个资产的期望哈希。
//
// 行格式是 sha256sum 的产出：`<hex>  <name>`。GNU sha256sum 在二进制模式下会
// 给文件名加 `*` 前缀，这里一并容忍——生成端换个实现不该让所有机器停止更新。
func sumFor(body, name string) (string, error) {
	for _, line := range strings.Split(body, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		if strings.TrimPrefix(f[1], "*") == name {
			return f[0], nil
		}
	}
	return "", fmt.Errorf("%s 里没有 %s 的校验和", ChecksumsName, name)
}

// extractBinary 从 tar.gz 里取出名为 handoff 的文件写到 dest。
func extractBinary(tgz []byte, dest string) error {
	gz, err := gzip.NewReader(strings.NewReader(string(tgz)))
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("包里没有名为 handoff 的文件")
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		if filepath.Base(h.Name) != "handoff" || h.Typeflag != tar.TypeReg {
			continue
		}
		f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := io.Copy(f, io.LimitReader(tr, maxAssetBytes)); err != nil {
			return err
		}
		return nil
	}
}

// selfCheck 跑新二进制的 version 子命令，要求首行等于期望 tag。
//
// 这是 D10 的第一道防线。它挡下的是「sha256 对但二进制跑不起来」——架构拿错、
// 动态库缺失、资产本身构建错。没有它，换版会把一个跑不了的东西 rename 到位，
// 然后 agentd 再也起不来，而且现场只剩一个「进程起不来」的空结论。
func (i *Installer) selfCheck(path, wantTag string) error {
	ctx, cancel := context.WithTimeout(context.Background(), selfCheckTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version").CombinedOutput()
	if err != nil {
		// stderr 原文是唯一能说明「为什么跑不起来」的东西，必须带出来
		return fmt.Errorf("新二进制自检失败（%s version 执行出错）: %s（%w）", path, firstLine(string(out)), err)
	}
	got := firstLine(string(out))
	if got != wantTag {
		return fmt.Errorf("新二进制自检失败：version 首行为 %q，期望 %q", got, wantTag)
	}
	i.Log.Info("新二进制自检通过", "path", path, "version", got)
	return nil
}

// Activate 把新二进制换到目标路径，旧的留成 <target>.prev。
//
// 参数：
//   - newPath: Fetch 返回的临时文件路径（必须与 target 同目录）
//   - target: 目标二进制路径（应已 EvalSymlinks 解析过）
//
// 返回：
//   - 留存的旧二进制路径
//   - 错误：目录不可写、rename 失败
//
// 注意：
//   - 两次 rename 都是同目录内操作，因而是原子的。中途失败最坏的结果是
//     「旧的已挪到 .prev、新的还没就位」——此时目标路径暂时缺失，
//     所以第二次 rename 失败时会把 .prev 挪回来
func Activate(newPath, target string) (string, error) {
	dir := filepath.Dir(target)
	// 先探一次写权限：直接 rename 得到的是扁平的 permission denied，
	// 用户不知道真因是「二进制装在了 /usr/local/bin 这种要 root 的地方」（B45）
	if err := checkDirWritable(dir); err != nil {
		return "", err
	}
	prev := PrevPath(target)
	if err := os.Rename(target, prev); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("留存旧二进制到 %s: %w", prev, err)
	}
	if err := os.Rename(newPath, target); err != nil {
		// 把旧的挪回去，别留下一个没有二进制的目标路径
		if rerr := os.Rename(prev, target); rerr != nil {
			return "", fmt.Errorf("换入新二进制失败且旧二进制未能复位（现在在 %s，请手动 mv 回 %s）: %w", prev, target, err)
		}
		return "", fmt.Errorf("换入新二进制 %s: %w", target, err)
	}
	return prev, nil
}

// Rollback 把 <target>.prev 换回 target。
//
// 注意：
//   - 这是 D10 的第二道防线，**只由人工命令触发**，自动更新循环永远不调它
func Rollback(target string) error {
	prev := PrevPath(target)
	if _, err := os.Stat(prev); err != nil {
		return fmt.Errorf("没有可回滚的旧二进制（%s 不存在）", prev)
	}
	if err := checkDirWritable(filepath.Dir(target)); err != nil {
		return err
	}
	if err := os.Rename(prev, target); err != nil {
		return fmt.Errorf("回滚 %s: %w", target, err)
	}
	return nil
}

// checkDirWritable 试着在目录里建一个临时文件，以此判断可写性。
//
// 为什么不看权限位：权限位判可写要考虑 uid/gid/ACL/只读挂载，判错的方向
// 还不确定。实际写一次是唯一可靠的判据。
func checkDirWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".handoff-wtest-")
	if err != nil {
		return fmt.Errorf("目录 %s 没有写权限，无法替换二进制；把 handoff 装到 ~/.local/bin，或用 sudo 手动升级（原因: %w）", dir, err)
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return nil
}
