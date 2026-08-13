// install.go —— 下载、校验、自检、原子替换。
//
// 边界：
//   - 临时文件**必须**落在目标二进制的同目录：os.Rename 的原子性只在同一
//     文件系统内成立，从 /tmp rename 到 /usr/local/bin 会因跨设备直接失败
//   - 任何一步失败都清干净临时文件：留一份坏二进制在二进制目录里，
//     下一轮可能被误当成已就绪的 pending
//   - 不做自动回滚（D10）：只把旧二进制留成 .prev，回退是人工命令
//   - 下载与安装是两件事，前者可跨平台、后者必须在目标平台执行
package release

import (
	"archive/tar"
	"archive/zip"
	"bytes"
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
	"runtime"
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
// Windows 上追加 .exe——selfCheck 要 exec 这个临时文件跑 version，
// 没有该后缀的文件在 Windows 上起不来。
func TempName(tag string) string { return tempName(tag, runtime.GOOS) }

// tempName 是 TempName 的可测实现，平台由调用方给定。
//
// 拆出这一层的唯一理由是可测性：判据写死成 runtime.GOOS 时，
// 「Windows 上带 .exe」这条行为在非 Windows 的 CI 上永远测不到。
func tempName(tag, goos string) string {
	name := ".handoff.new-" + tag
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// PrevPath 返回某目标路径对应的旧二进制留存路径。
func PrevPath(target string) string { return target + ".prev" }

// Installer 执行下载与安装。
type Installer struct {
	HTTP *http.Client
	Log  *slog.Logger
	// DownloadBase 是资产下载根，默认 release.DownloadBase。
	// 存在的唯一理由是可测性：不覆盖它，FetchByTag 的测试必须真的打 github.com
	DownloadBase string
}

// NewInstaller 构造默认 installer（10 分钟超时，覆盖慢网下的 20MB 下载）。
//
// 参数：
//   - log: 日志入口
//   - tr: HTTP transport；**nil = 用标准库默认**（认 HTTPS_PROXY 等环境变量）
func NewInstaller(log *slog.Logger, tr http.RoundTripper) *Installer {
	return &Installer{
		HTTP:         &http.Client{Timeout: 10 * time.Minute, Transport: tr},
		Log:          log,
		DownloadBase: DownloadBase,
	}
}

// FetchArchive 按指定平台下载资产并校验完整性，返回字节与期望哈希。
//
// 参数：
//   - ctx: 上下文
//   - rel: 目标发布
//   - goos / goarch: **目标机器**的平台，不是本机——跨平台推送时远端可能
//     是 linux/amd64 而本机是 darwin/arm64，必须知道该下哪份资产
//
// 返回：
//   - tgz: 资产原文（tar.gz 字节，**未经解包**）
//   - 期望的 sha256（十六进制小写），**来自 checksums.txt 的声明**——
//     这是信任链的第一道校验，消费方把它原样传给 InstallArchive 让两端
//     比同一个来自 release 的声明，不互相背书
//   - 错误：缺资产、下载失败、校验不过
//
// 注意：
//   - **不解包、不自检**。自检要 exec 执行新二进制，而本机执行别的平台的
//     二进制必然失败——自检必须在目标平台上做（agentd 收到推送后）
//   - 不重试：完整性失败重试只会重下同一份坏数据（spec §4.7）
func (i *Installer) FetchArchive(ctx context.Context, rel Release, goos, goarch string) ([]byte, string, error) {
	asset, ok := rel.AssetFor(goos, goarch)
	if !ok {
		return nil, "", fmt.Errorf("发布 %s 没有 %s/%s 的资产（%s）", rel.Tag, goos, goarch, AssetName(rel.Tag, goos, goarch))
	}
	ck, ok := rel.Checksums()
	if !ok {
		// 没有校验和就没法验完整性。宁可不更新，也不装一个来路不明的二进制
		return nil, "", fmt.Errorf("发布 %s 没有 %s，无法校验完整性", rel.Tag, ChecksumsName)
	}

	i.Log.Info("开始下载资产", "tag", rel.Tag, "platform", goos+"/"+goarch, "asset", asset.Name, "url", asset.URL)
	tgz, err := i.get(ctx, asset.URL)
	if err != nil {
		return nil, "", fmt.Errorf("下载 %s: %w", asset.Name, err)
	}
	sums, err := i.get(ctx, ck.URL)
	if err != nil {
		return nil, "", fmt.Errorf("下载 %s: %w", ChecksumsName, err)
	}

	want, err := sumFor(string(sums), asset.Name)
	if err != nil {
		return nil, "", err
	}
	got := sha256.Sum256(tgz)
	if hex.EncodeToString(got[:]) != want {
		return nil, "", fmt.Errorf("sha256 校验不通过（期望 %s，实得 %s）", want, hex.EncodeToString(got[:]))
	}
	i.Log.Info("资产校验通过", "tag", rel.Tag, "asset", asset.Name, "sha256", want, "bytes", len(tgz))
	return tgz, want, nil
}

// FetchChecksum 只下载 checksums.txt 并解出某平台资产的期望哈希。
//
// 参数：
//   - ctx: 上下文
//   - rel: 目标发布（需要它的 Assets 里有 checksums.txt 的 URL）
//   - goos / goarch: 目标机器的平台
//
// 返回：
//   - 该平台资产的 sha256（十六进制小写）
//   - 错误：缺 checksums 资产、下载失败、文件里没有该资产的行
//
// 注意：
//   - **不下资产**。这正是自拉模式的省流量点：协调者只下几百字节的 checksums，
//     20MB 的资产由执行机自己去下（spec §5.5）
//   - 一次 upgrade --now 涉及多台机器时，调用方应当只调一次并缓存——
//     同一个 release 的 checksums.txt 对所有平台是同一份
func (i *Installer) FetchChecksum(ctx context.Context, rel Release, goos, goarch string) (string, error) {
	ck, ok := rel.Checksums()
	if !ok {
		return "", fmt.Errorf("发布 %s 没有 %s，无法校验完整性", rel.Tag, ChecksumsName)
	}
	i.Log.Info("下载校验和文件", "tag", rel.Tag, "url", ck.URL)
	sums, err := i.get(ctx, ck.URL)
	if err != nil {
		i.Log.Error("下载校验和文件失败", "tag", rel.Tag, "url", ck.URL, "cause", err)
		return "", fmt.Errorf("下载 %s: %w", ChecksumsName, err)
	}
	sum, err := sumFor(string(sums), AssetName(rel.Tag, goos, goarch))
	if err != nil {
		i.Log.Error("校验和文件里没有该平台的行", "tag", rel.Tag,
			"platform", goos+"/"+goarch, "cause", err)
		return "", err
	}
	i.Log.Info("取得校验和", "tag", rel.Tag, "platform", goos+"/"+goarch, "sha256", sum)
	return sum, nil
}

// FetchByTag 按 tag 拼出下载地址、下载资产并用**给定的** sha256 校验。
//
// 参数：
//   - ctx: 上下文
//   - repo: owner/name
//   - tag: 目标版本
//   - goos / goarch: 本机平台
//   - wantSum: 期望的 sha256（十六进制小写）。自拉模式下**来自协调者下发**
//
// 返回：
//   - 资产原文（tar.gz / zip 字节，未解包）
//   - 错误：下载失败、sha256 不符
//
// 注意：
//   - 与 FetchArchive 的区别是**不需要 Release 对象、不查 API**：地址由
//     AssetURL 确定性拼出，wantSum 由调用方给。这让执行机完全不碰
//     api.github.com（避开 60 次/小时/IP 的匿名限流）
//   - wantSum 由调用方给而不是自己去取 checksums，是刻意的：校验和与资产
//     走两条不同的信任路径，本机代理/镜像被投毒时才抓得住（spec §5.5）。
//     **别"优化"成自己下 checksums**
//   - 不重试：完整性失败重试只会重下同一份坏数据（spec §4.7）
func (i *Installer) FetchByTag(ctx context.Context, repo, tag, goos, goarch, wantSum string) ([]byte, error) {
	base := i.DownloadBase
	if base == "" {
		base = DownloadBase
	}
	name := AssetName(tag, goos, goarch)
	url := fmt.Sprintf("%s/%s/releases/download/%s/%s", base, repo, tag, name)

	i.Log.Info("开始下载资产", "tag", tag, "platform", goos+"/"+goarch, "asset", name, "url", url)
	b, err := i.get(ctx, url)
	if err != nil {
		i.Log.Error("下载资产失败", "tag", tag, "url", url, "cause", err)
		return nil, fmt.Errorf("下载 %s: %w", name, err)
	}
	got := sha256.Sum256(b)
	if hex.EncodeToString(got[:]) != wantSum {
		i.Log.Error("资产校验不通过", "tag", tag, "asset", name,
			"want", wantSum, "got", hex.EncodeToString(got[:]), "bytes", len(b))
		return nil, fmt.Errorf("sha256 校验不通过（期望 %s，实得 %s）", wantSum, hex.EncodeToString(got[:]))
	}
	i.Log.Info("资产校验通过", "tag", tag, "asset", name, "sha256", wantSum, "bytes", len(b))
	return b, nil
}

// InstallArchive 校验、解包、自检一份已下载的资产，返回可供 Activate 的临时文件路径。
//
// 参数：
//   - tgz: FetchArchive 返回的资产原文
//   - wantSum: 期望的 sha256（十六进制小写），agentd 侧来自 CLI 推来的
//     query 参数，是信任链的第二道校验（传输完整性）
//   - wantTag: 目标版本，自检时拿新二进制 version 首行与它比对
//   - destDir: 临时文件落点，**必须**与目标二进制同目录
//
// 返回：
//   - 临时二进制的完整路径（已 chmod 0755 并通过自检）
//   - 错误：校验不过、解包失败、置位失败、自检不过
//
// 注意：
//   - 任何一步失败都会把临时文件删掉，不留残件
func (i *Installer) InstallArchive(tgz []byte, wantSum, wantTag, destDir string) (string, error) {
	got := sha256.Sum256(tgz)
	if hex.EncodeToString(got[:]) != wantSum {
		i.Log.Error("安装被拒：sha256 校验不通过", "tag", wantTag,
			"want", wantSum, "got", hex.EncodeToString(got[:]))
		return "", fmt.Errorf("sha256 校验不通过（期望 %s，实得 %s）", wantSum, hex.EncodeToString(got[:]))
	}

	i.Log.Info("开始安装资产", "tag", wantTag, "dest_dir", destDir, "bytes", len(tgz))
	tmp := filepath.Join(destDir, TempName(wantTag))
	// 从这里往后任何失败都要清干净
	cleanup := func() {
		if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
			i.Log.Warn("清理临时文件失败", "path", tmp, "cause", err)
		}
	}
	format, err := extractBinary(tgz, tmp)
	if err != nil {
		cleanup()
		i.Log.Error("安装被拒：解包失败", "tag", wantTag, "path", tmp, "cause", err)
		return "", fmt.Errorf("解包 %s: %w", wantTag, err)
	}
	// 装的到底是 zip 还是 tar.gz 是排查「资产格式与平台不符」时的第一个问题，
	// 而它此前只能靠资产名去猜
	i.Log.Info("归档解包完成", "tag", wantTag, "format", format, "path", tmp)
	if err := os.Chmod(tmp, 0o755); err != nil {
		cleanup()
		i.Log.Error("安装被拒：置可执行位失败", "tag", wantTag, "path", tmp, "cause", err)
		return "", fmt.Errorf("置可执行位 %s: %w", tmp, err)
	}
	if err := i.selfCheck(tmp, wantTag); err != nil {
		cleanup()
		i.Log.Error("安装被拒：自检失败", "tag", wantTag, "path", tmp, "cause", err)
		return "", err
	}
	i.Log.Info("新版本已就绪", "tag", wantTag, "path", tmp)
	return tmp, nil
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
//   - 本函数是 FetchArchive + InstallArchive 的本机组合，行为与拆分前一致
func (i *Installer) Fetch(ctx context.Context, rel Release, destDir string) (string, error) {
	goos, goarch := CurrentPlatform()
	tgz, sum, err := i.FetchArchive(ctx, rel, goos, goarch)
	if err != nil {
		return "", err
	}
	return i.InstallArchive(tgz, sum, rel.Tag, destDir)
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

// gzipMagic / zipMagic 是两种归档的文件头。
var (
	gzipMagic = []byte{0x1f, 0x8b}
	zipMagic  = []byte{'P', 'K', 0x03, 0x04}
)

// binaryNames 是归档内可接受的可执行文件名。
//
// Windows 资产里是 handoff.exe，其余平台是 handoff。两个都认而不按平台分派，
// 理由同 extractBinary：判据来自归档本身，不来自调用方对平台的声明。
var binaryNames = map[string]bool{"handoff": true, "handoff.exe": true}

// extractBinary 从归档里取出 handoff 可执行文件写到 dest。
//
// 参数：
//   - data: 归档原文（tar.gz 或 zip）
//   - dest: 落点路径
//
// 返回：
//   - format: 实际识别出的格式（"tar.gz" / "zip"），供调用方打进日志
//   - 错误：格式不认、解包失败、包内没有可执行文件
//
// 注意：
//   - 格式**按魔数判定，不按调用方传入的平台**。传平台会制造第二个真相来源，
//     一旦它与实际字节不符，报错会指向错误的方向；字节才是权威。这条选择的
//     额外好处是 InstallArchive / Fetch / FetchArchive 三个签名都不用动
func extractBinary(data []byte, dest string) (string, error) {
	switch {
	case bytes.HasPrefix(data, gzipMagic):
		return "tar.gz", extractFromTarGz(data, dest)
	case bytes.HasPrefix(data, zipMagic):
		return "zip", extractFromZip(data, dest)
	default:
		head := data
		if len(head) > 4 {
			head = head[:4]
		}
		return "", fmt.Errorf("无法识别的归档格式：既不是 gzip 也不是 zip（前 %d 字节 %x）", len(head), head)
	}
}

// extractFromTarGz 从 tar.gz 里取出可执行文件写到 dest。
func extractFromTarGz(data []byte, dest string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return errors.New("包里没有名为 handoff / handoff.exe 的文件")
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		if !binaryNames[filepath.Base(h.Name)] || h.Typeflag != tar.TypeReg {
			continue
		}
		return writeExtracted(tr, dest)
	}
}

// extractFromZip 从 zip 里取出可执行文件写到 dest。
func extractFromZip(data []byte, dest string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("zip: %w", err)
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !binaryNames[filepath.Base(f.Name)] {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("zip 打开 %s: %w", f.Name, err)
		}
		defer rc.Close()
		return writeExtracted(rc, dest)
	}
	return errors.New("包里没有名为 handoff / handoff.exe 的文件")
}

// writeExtracted 把归档条目的内容写到 dest，带大小上限。
//
// 抽出来是因为两种格式的写盘部分完全一样，而这段恰好是唯一会在磁盘上
// 留下痕迹的地方——只有一处，出问题时也只需要看一处。
func writeExtracted(r io.Reader, dest string) error {
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, io.LimitReader(r, maxAssetBytes))
	return err
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
//   - **两次 rename 的顺序在 Windows 上是承重的**：Windows 允许 rename 一个
//     正在运行的 exe，但不允许覆盖或删除它。所以「先把旧的挪走、再把新的挪进来」
//     恰好就是 Windows 自更新的标准手法。**不要**把它「优化」成先删后写——
//     那在 unix 上照样绿，在 Windows 上当场炸
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
