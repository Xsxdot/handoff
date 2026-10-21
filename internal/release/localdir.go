// localdir.go —— 本机构建打包目录作为 release 来源（B462 S2）。
//
// 职责：把 scripts/build-deploy.sh --pack 产出的目录（每平台一个
// handoff_<tag>_<goos>_<goarch>.{tar.gz|zip} + checksums.txt，形态与 GitHub release
// 资产逐字一致）读成一个 Release，并给出一个从该目录「下载」的 Installer。
//
// 边界：
//   - 只换来源与传输层：sha 校验、checksums 解析（sumFor）、缺资产报错、解包、自检
//     全部复用 Installer 的既有实现，本文件不复写任何信任链判据
//   - 资产命名只认 AssetName（命名规则唯一定义点）：拆名得出候选 tag/平台后用
//     AssetName 回拼，与原文件名逐字相等才算资产
//   - 不读 handoff 配置；目录路径由调用方给
package release

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// LoadLocalDir 读一个本机构建打包目录，得出 Release。
//
// 参数：
//   - dir: 打包目录（build-deploy.sh --pack 的输出目录）
//
// 返回：
//   - Release：Tag 由包名得出（不由目录名猜），Assets 为目录里全部可识别的 CLI 包
//     加 checksums.txt；资产 URL 是 file:///<文件名>，只能由 NewLocalDirInstaller(dir)
//     构造的 Installer 读取
//   - 错误：目录不可读、缺 checksums.txt、零个包、包之间 tag 不一（列出全部 tag）
//
// 注意：
//   - checksums.txt 里出现而目录里没有的包不入清单；目录里有而 checksums.txt 缺行的包
//     照样入清单——缺行由 FetchArchive 按机器报「没有 X 的校验和」，只让该机失败
//   - 薄壳（handoff-desktop_*）、备份文件、命名回拼不等的文件一律不算资产
func LoadLocalDir(dir string) (Release, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return Release{}, fmt.Errorf("读本机构建目录 %s: %w", dir, err)
	}
	var assets []Asset
	hasSums := false
	tags := map[string]bool{}
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == ChecksumsName {
			hasSums = true
			continue
		}
		tag, ok := parseAssetName(name)
		if !ok {
			continue
		}
		tags[tag] = true
		assets = append(assets, Asset{Name: name, URL: localAssetURL(name)})
	}
	if !hasSums {
		return Release{}, fmt.Errorf("本机构建目录 %s 没有 %s，无法校验完整性（应为 scripts/build-deploy.sh --pack 的输出目录）", dir, ChecksumsName)
	}
	if len(assets) == 0 {
		return Release{}, fmt.Errorf("本机构建目录 %s 里没有任何 handoff_<tag>_<os>_<arch> 包（应为 scripts/build-deploy.sh --pack 的输出目录）", dir)
	}
	if len(tags) > 1 {
		list := make([]string, 0, len(tags))
		for t := range tags {
			list = append(list, t)
		}
		sort.Strings(list)
		return Release{}, fmt.Errorf("本机构建目录 %s 混有多个版本 %s，一个目录只能放一次打包的产物", dir, strings.Join(list, "、"))
	}
	var tag string
	for t := range tags {
		tag = t
	}
	assets = append(assets, Asset{Name: ChecksumsName, URL: localAssetURL(ChecksumsName)})
	return Release{Tag: tag, Assets: assets}, nil
}

// NewLocalDirInstaller 构造一个从本机构建目录读取资产的 Installer。
//
// 参数：log 日志入口；dir 与 LoadLocalDir 的同一目录。
// 返回：HTTP 传输层换成 http.NewFileTransport(dir) 的 Installer——Release 里的
// file:///<文件名> 由它解析到 dir 下；其余行为（重试、上限、校验、自检）与 NewInstaller 一致。
func NewLocalDirInstaller(log *slog.Logger, dir string) *Installer {
	return &Installer{
		HTTP:         &http.Client{Timeout: 10 * time.Minute, Transport: http.NewFileTransport(http.Dir(dir))},
		Log:          log,
		DownloadBase: DownloadBase,
	}
}

// localAssetURL 把目录内文件名写成 file 传输层可解析的 URL。
func localAssetURL(name string) string {
	return (&url.URL{Scheme: "file", Path: "/" + name}).String()
}

// parseAssetName 从 handoff_<tag>_<goos>_<goarch>.<ext> 拆出 tag，并用 AssetName 回拼复核。
// 末两段是平台（goos/goarch 不含下划线），其余是 tag；回拼不等（扩展名不符平台、
// 尾缀多余等）即不是资产。
func parseAssetName(name string) (string, bool) {
	if !strings.HasPrefix(name, "handoff_") {
		return "", false
	}
	stem := strings.TrimPrefix(name, "handoff_")
	for _, ext := range []string{".tar.gz", ".zip"} {
		if !strings.HasSuffix(stem, ext) {
			continue
		}
		parts := strings.Split(strings.TrimSuffix(stem, ext), "_")
		if len(parts) < 3 {
			return "", false
		}
		n := len(parts)
		tag := strings.Join(parts[:n-2], "_")
		if tag == "" || AssetName(tag, parts[n-2], parts[n-1]) != name {
			return "", false
		}
		return tag, true
	}
	return "", false
}
