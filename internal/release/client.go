// Package release 负责「把某个版本的 handoff 二进制正确落到某个路径」。
//
// 职责：
//   - 查 GitHub 的 latest release，解出 tag 与本平台资产的下载 URL
//   - 下载、校 sha256、解包、自检、原子替换，并把旧二进制留成 .prev
//
// 边界：
//   - **不决定何时替换**：那是 internal/selfupdate 的事。本包是一个执行器，
//     调用方说装就装
//   - 不知道 agentd、不知道任务、不读 handoff 的配置
//   - 不做自动回滚（D10）：留下 .prev 供人工 handoff upgrade --rollback
package release

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"
)

// DefaultRepo 是 Release 所在的 GitHub 仓库。
//
// 注意 go.mod 的 module path（github.com/Xsxdot/handoff）已与 GitHub owner
// 一致：`go install github.com/Xsxdot/handoff@latest` 与下载链指向同一个仓库。
const DefaultRepo = "Xsxdot/handoff"

// DefaultAPIBase 是 GitHub REST API 的根。
//
// D11：自动更新链路一律打 GitHub 原生 URL，不走自有域名——域名过期、DNS 故障、
// 重定向规则改错，任何一样都会让所有机器的自动更新一起哑掉。
const DefaultAPIBase = "https://api.github.com"

// DownloadBase 是 release 资产的下载根（GitHub 的确定性地址）。
//
// D11 同理：自动更新链路一律打 GitHub 原生地址，不走自有域名。
const DownloadBase = "https://github.com"

// ChecksumsName 是校验和文件名，与 .github/workflows/release.yml 产出一致。
const ChecksumsName = "checksums.txt"

// Asset 是一个 release 资产。
type Asset struct {
	Name string
	URL  string
}

// Release 是一次发布。
type Release struct {
	Tag    string
	Assets []Asset
}

// archiveExt 返回某平台的归档扩展名。
//
// Windows 用 zip 而非 tar.gz：zip 在资源管理器里双击即开，而 tar.gz 必须敲
// 命令行；且 Expand-Archive 存在于每一个 PowerShell，tar.exe 只有 Win10
// 1803+ 才有。手动下载是 Windows 用户的常见路径，这个差异值得多一种格式。
func archiveExt(goos string) string {
	if goos == "windows" {
		return ".zip"
	}
	return ".tar.gz"
}

// AssetName 拼装某平台的资产名。
//
// 参数：
//   - tag: 版本号，形如 v0.1.0
//   - goos / goarch: 目标平台
//
// 返回：
//   - 资产文件名
//
// 注意：
//   - 格式必须与 .github/workflows/release.yml 里的产出**逐字一致**。
//     不一致的症状是查得到版本但下不到东西，且每轮重试
//   - 扩展名按平台分（见 archiveExt），install.sh / install.ps1 两边也依赖这条
func AssetName(tag, goos, goarch string) string {
	return fmt.Sprintf("handoff_%s_%s_%s%s", tag, goos, goarch, archiveExt(goos))
}

// AssetFor 取本平台的资产。
//
// 返回：
//   - 资产与是否找到。找不到说明这次发布漏了某平台
func (r Release) AssetFor(goos, goarch string) (Asset, bool) {
	want := AssetName(r.Tag, goos, goarch)
	for _, a := range r.Assets {
		if a.Name == want {
			return a, true
		}
	}
	return Asset{}, false
}

// Checksums 取校验和文件资产。
func (r Release) Checksums() (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == ChecksumsName {
			return a, true
		}
	}
	return Asset{}, false
}

// Client 查 GitHub release。
type Client struct {
	HTTP    *http.Client
	APIBase string
	Repo    string
}

// NewClient 构造 release 查询 client：30s 超时，打 GitHub 官方端点。
//
// 参数：
//   - tr: HTTP transport；**nil = 用标准库默认**（认 HTTPS_PROXY 等环境变量），
//     与本参数加入前的行为一字不差。要走配置里的代理，传 proxycfg.Transport 的产物
//
// 注意：
//   - 本包不读 handoff 配置（见 package 注释），所以收的是造好的 transport
//     而不是配置字符串——这条边界是刻意的，别"顺手"改成传 *config.Config
//   - 30s 而不是更长：查版本是一个可以失败的后台动作（失败就等下一个 interval），
//     卡住一个 goroutine 几分钟没有任何好处
func NewClient(tr http.RoundTripper) *Client {
	return &Client{
		HTTP:    &http.Client{Timeout: 30 * time.Second, Transport: tr},
		APIBase: DefaultAPIBase,
		Repo:    DefaultRepo,
	}
}

// ghRelease 是 GitHub API 响应里我们关心的那部分。
type ghRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// Latest 查最新发布。
//
// 参数：
//   - ctx: 上下文，用于超时与取消
//
// 返回：
//   - 解析后的 Release
//   - 错误：网络失败、非 200、响应畸形
//
// 注意：
//   - 匿名调用有 60 次/小时/IP 的限流，被限流时返回带 403 的错误。
//     **调用方不要重试**——interval 本身就是退避（spec §4.7）
func (c *Client) Latest(ctx context.Context) (Release, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/latest", strings.TrimSuffix(c.APIBase, "/"), c.Repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, fmt.Errorf("构造请求: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("查最新版本 %s: %w", url, err)
	}
	defer resp.Body.Close()
	// 限制读取量：畸形/被劫持的响应不该把内存吃光
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return Release{}, fmt.Errorf("读响应: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// 状态码必须带出来：403 是限流、404 是仓库没有 release、5xx 是 GitHub 挂了，
		// 三者的处置完全不同，只报「查版本失败」等于让人去猜
		return Release{}, fmt.Errorf("查最新版本返回 %d: %s", resp.StatusCode, firstLine(string(body)))
	}
	var gr ghRelease
	if err := json.Unmarshal(body, &gr); err != nil {
		return Release{}, fmt.Errorf("解析响应: %w", err)
	}
	if gr.TagName == "" {
		// 空 tag 会一路流到「tag != 当前版本」的比较里恒为真，于是每轮都去下载
		// 一个名字里带空版本号的资产，永远失败且永远重试
		return Release{}, fmt.Errorf("响应里没有 tag_name（仓库 %s 可能还没有任何 release）", c.Repo)
	}
	rel := Release{Tag: gr.TagName}
	for _, a := range gr.Assets {
		rel.Assets = append(rel.Assets, Asset{Name: a.Name, URL: a.URL})
	}
	return rel, nil
}

// CurrentPlatform 返回当前进程的 goos/goarch，便于调用方少写两个 runtime 引用。
func CurrentPlatform() (string, string) { return runtime.GOOS, runtime.GOARCH }

// AssetURL 拼一个 release 资产的下载地址。
//
// 参数：
//   - repo: owner/name，如 Xsxdot/handoff
//   - tag: 版本号，形如 v0.2.3
//   - name: 资产文件名，用 AssetName 生成
//
// 返回：
//   - 完整下载地址
//
// 注意：
//   - GitHub 的这个地址是**确定性**的，不需要先查 API 就能拼出来。agentd
//     自拉时用的正是它——api.github.com 有 60 次/小时/IP 的匿名限流，
//     而多台执行机很可能共用一个代理出口 IP，走 API 迟早互相打架
func AssetURL(repo, tag, name string) string {
	return fmt.Sprintf("%s/%s/releases/download/%s/%s", DownloadBase, repo, tag, name)
}

// firstLine 取多行文本的第一行，用作错误摘要。
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}
