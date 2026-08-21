// 本文件实现「查最新版」与「下载桌面端安装包」：
//
//	GET  /api/update/latest            —— 缓存的最新 tag（?refresh=1 绕过）
//	POST /api/update/desktop/download  —— 下载本平台薄壳安装包、校验、打开
//	GET  /api/update/desktop/download  —— 进度
//
// 边界（承重）：
//   - 不做换版。下完把安装包交给用户，最后一步（拖进应用程序 / 解压覆盖）由人
//     完成。自我替换需要一条控制台→薄壳的指令通道，比它服务的动作还贵（spec §5）。
//   - 不复用 POST /api/workspaces/reveal。那个端点的 revealTarget 会硬拒绝跑出
//     工作树的路径——那是它的设计目的，不是缺陷。这里揭示的是下载目录里的文件，
//     两者的安全边界不同，必须各写各的。
//   - 检查缓存与 CLI、薄壳共用同一个文件（selfupdate.CLICheckPath）：
//     api.github.com 有 60 次/小时/IP 的匿名限流，多消费者各查各的正是触发它的方式。
package agentd

import (
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

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/release"
	"github.com/Xsxdot/handoff/internal/selfupdate"
)

// desktopDownloadFetcher 构造生产下载缝：校验和走 release 的 checksums.txt，
// 安装包走确定性的 release 资产地址；两者都不经过控制台或 reveal 路由。
//
// 参数：inst 是带 agentd 出网代理与日志的 release installer。
// 返回：按 tag/资产名返回安装包字节、期望 sha256 与错误。
func desktopDownloadFetcher(inst *release.Installer) func(context.Context, string, string) ([]byte, string, error) {
	return func(ctx context.Context, tag, assetName string) ([]byte, string, error) {
		want, err := desktopDownloadChecksum(inst)(ctx, tag, assetName)
		if err != nil {
			return nil, "", err
		}

		url := release.AssetURL(release.DefaultRepo, tag, assetName)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, "", fmt.Errorf("构造安装包请求: %w", err)
		}
		resp, err := inst.HTTP.Do(req)
		if err != nil {
			return nil, "", fmt.Errorf("下载安装包: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, "", fmt.Errorf("下载安装包返回 %d", resp.StatusCode)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxUpdateBytes+1))
		if err != nil {
			return nil, "", fmt.Errorf("读取安装包: %w", err)
		}
		if len(body) > maxUpdateBytes {
			return nil, "", fmt.Errorf("安装包超过 %d 字节上限", maxUpdateBytes)
		}
		return body, want, nil
	}
}

// desktopDownloadChecksum 构造生产校验和查询缝。
//
// 参数：inst 是带 agentd 出网代理与日志的 release installer。
// 返回：按 tag/资产名从该 release 的 checksums.txt 解出的 sha256。
func desktopDownloadChecksum(inst *release.Installer) func(context.Context, string, string) (string, error) {
	return func(ctx context.Context, tag, assetName string) (string, error) {
		rel := release.Release{Tag: tag, Assets: []release.Asset{
			{Name: release.ChecksumsName, URL: release.AssetURL(release.DefaultRepo, tag, release.ChecksumsName)},
		}}
		return inst.FetchChecksumFor(ctx, rel, assetName)
	}
}

// openDownloadedFile 按当前平台唤起文件管理器。
//
// 参数：path 是已落盘的绝对路径。
// 返回：只有子进程无法启动时返回错误；Start 成功后由后台 goroutine Wait 收尸，
// 防止长驻 agentd 每次下载都积累僵尸子进程。Windows explorer 的退出码不参与判断，
// 因为它天生可能用非零码结束。
func openDownloadedFile(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("explorer", "/select,"+path)
	default:
		cmd = exec.Command("xdg-open", filepath.Dir(path))
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	// agentd 长驻；不 Wait 会让 open/xdg-open 秒退后的子进程一直占 zombie 槽位。
	// 不把 Wait 的退出码传给调用方：调用方只关心是否成功唤起，Windows explorer
	// 非零退出码不表示启动失败。
	go func() { _ = cmd.Wait() }()
	return nil
}

// handleUpdateLatest 返回缓存的最新 release tag。
//
// 参数：refresh=1 时忽略 24h 缓存；其他参数不改变缓存策略。
// 返回：始终 200 + LatestResp；查找失败时 Tag 为空，不把网络故障升级成控制台故障。
func (s *Server) handleUpdateLatest(w http.ResponseWriter, r *http.Request) {
	refresh := r.URL.Query().Get("refresh") == "1"
	c, err := s.loadLatestCheck(r.Context(), refresh)
	if err != nil {
		s.log.Error("查最新版失败，按空结果返回", "refresh", refresh, "cause", err)
		writeJSON(w, http.StatusOK, proto.LatestResp{})
		return
	}
	writeJSON(w, http.StatusOK, latestResp(c))
}

// loadLatestCheck 读取或刷新 CLI/薄壳共用的 24h release 缓存。
//
// 参数：ctx 用于 release 查询；refresh=true 强制查询。
// 返回：缓存内容；网络、缓存写入或依赖缺失错误由调用方转成空 tag。
func (s *Server) loadLatestCheck(ctx context.Context, refresh bool) (*selfupdate.CLICheck, error) {
	cached := selfupdate.LoadCLICheck(s.conf().DataDir)
	now := time.Now().UTC()
	if !refresh && !selfupdate.CLICheckStale(cached, now) {
		return cached, nil
	}
	if s.latestFetch == nil {
		return nil, errors.New("最新版查询依赖未注入")
	}
	rel, err := s.latestFetch(ctx)
	if err != nil {
		return nil, err
	}
	c := &selfupdate.CLICheck{CheckedAt: now, Latest: rel.Tag}
	if err := selfupdate.SaveCLICheck(s.conf().DataDir, c); err != nil {
		return nil, fmt.Errorf("保存最新版缓存: %w", err)
	}
	return c, nil
}

// latestResp 将内部缓存转换为线上响应。
func latestResp(c *selfupdate.CLICheck) proto.LatestResp {
	if c == nil {
		return proto.LatestResp{}
	}
	return proto.LatestResp{Tag: c.Latest, CheckedAt: c.CheckedAt.Format(time.RFC3339)}
}

// handleDesktopDownloadStart 下载、校验并打开本平台桌面安装包。
//
// 参数：可用 query tag 指定版本；省略时使用共用的 latest 缓存。
// 返回：成功 200；平台不支持 400；并发下载 409；网络/校验/落盘失败 502。
// 注意：下载 I/O 不持 downloadMu，GET 进度可以在下载期间读取。
func (s *Server) handleDesktopDownloadStart(w http.ResponseWriter, r *http.Request) {
	tag := r.URL.Query().Get("tag")
	if tag == "" {
		c, err := s.loadLatestCheck(r.Context(), false)
		if err != nil || c == nil || c.Latest == "" {
			if err == nil {
				err = errors.New("最新版 tag 为空")
			}
			s.log.Error("下载被拒：没有可用的最新版", "cause", err)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		tag = c.Latest
	}

	if !s.beginDesktopDownload(tag) {
		s.log.Warn("下载被拒：已有桌面安装包下载在进行中", "tag", tag)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "已有桌面安装包下载在进行中"})
		return
	}

	goos, goarch := release.CurrentPlatform()
	if s.downloadPlatform != nil {
		goos, goarch = s.downloadPlatform()
	}
	assetName, ok := release.DesktopAssetName(tag, goos, goarch)
	if !ok {
		msg := fmt.Sprintf("平台 %s/%s 没有桌面端发布物", goos, goarch)
		s.setDesktopDownloadFailed(tag, msg)
		s.log.Warn("下载被拒：平台没有薄壳发布物", "tag", tag, "platform", goos+"/"+goarch)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
		return
	}

	dataDir := filepath.Join(s.conf().DataDir, "downloads")
	path, err := filepath.Abs(filepath.Join(dataDir, assetName))
	if err != nil {
		s.finishDesktopDownloadError(w, tag, "解析安装包路径", err)
		return
	}
	s.log.Info("开始下载桌面安装包", "tag", tag, "asset", assetName, "path", path)

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		s.finishDesktopDownloadError(w, tag, "创建下载目录", err)
		return
	}
	skipped := false
	var body []byte
	var want string
	if _, err := os.Stat(path); err == nil && s.downloadChecksum != nil {
		s.setDesktopDownloadStage("verifying", -1)
		want, err = s.downloadChecksum(r.Context(), tag, assetName)
		if err != nil {
			s.finishDesktopDownloadError(w, tag, "取得安装包校验和", err)
			return
		}
		existing, readErr := os.ReadFile(path)
		if readErr == nil {
			got := sha256Hex(existing)
			if strings.EqualFold(got, want) {
				s.log.Info("安装包校验通过", "tag", tag, "asset", assetName, "want", want, "got", got, "path", path)
				s.log.Info("安装包已存在，跳过下载", "tag", tag, "asset", assetName, "path", path)
				skipped = true
			}
		}
	}

	if !skipped {
		if s.downloadFetch == nil {
			s.finishDesktopDownloadError(w, tag, "下载依赖未注入", errors.New("downloadFetch 为 nil"))
			return
		}
		s.setDesktopDownloadStage("downloading", -1)
		body, want, err = s.downloadFetch(r.Context(), tag, assetName)
		if err != nil {
			s.finishDesktopDownloadError(w, tag, "下载安装包", err)
			return
		}
		s.setDesktopDownloadStage("verifying", -1)
		if err := os.WriteFile(path, body, 0o600); err != nil {
			s.finishDesktopDownloadError(w, tag, "写入安装包", err)
			return
		}
		got := sha256Hex(body)
		if !strings.EqualFold(got, want) {
			removeErr := os.Remove(path)
			if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				s.log.Error("安装包校验不通过，删除文件失败", "want", want, "got", got, "path", path, "cause", removeErr)
			}
			s.log.Error("安装包校验不通过，已删除", "want", want, "got", got, "path", path)
			s.finishDesktopDownloadStatus(w, tag, http.StatusBadGateway,
				fmt.Sprintf("sha256 校验不通过（期望 %s，实得 %s）", want, got))
			return
		}
		s.log.Info("安装包校验通过", "tag", tag, "asset", assetName, "want", want, "got", got, "path", path)
	}

	removed := cleanupOldDesktopDownloads(s.log, filepath.Dir(path), filepath.Base(path))
	s.log.Info("清理旧安装包完成", "dir", filepath.Dir(path), "removed", removed)
	opened := false
	if s.downloadOpen == nil {
		s.log.Warn("唤起文件管理器失败", "path", path, "cause", errors.New("downloadOpen 为 nil"))
	} else if err := s.downloadOpen(path); err != nil {
		s.log.Warn("唤起文件管理器失败", "path", path, "cause", err)
	} else {
		opened = true
		s.log.Info("已唤起文件管理器", "path", path)
	}
	s.setDesktopDownloadDone(tag, path, opened)
	writeJSON(w, http.StatusOK, s.desktopDownloadSnapshot())
}

// handleDesktopDownloadState 返回当前下载状态快照。
//
// 参数：w/r 是标准 HTTP 响应与请求。
// 返回：始终 200 + DownloadState；状态只存在内存中，agentd 重启后回到 idle。
func (s *Server) handleDesktopDownloadState(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.desktopDownloadSnapshot())
}

// beginDesktopDownload 抢占一个下载槽并设置 downloading 状态。
func (s *Server) beginDesktopDownload(tag string) bool {
	s.downloadMu.Lock()
	defer s.downloadMu.Unlock()
	if s.downloadState != nil && (s.downloadState.Stage == "downloading" || s.downloadState.Stage == "verifying") {
		return false
	}
	s.downloadState = &proto.DownloadState{Stage: "downloading", Tag: tag, Percent: -1}
	return true
}

// setDesktopDownloadStage 更新仍在运行的下载阶段。
func (s *Server) setDesktopDownloadStage(stage string, percent int) {
	s.downloadMu.Lock()
	defer s.downloadMu.Unlock()
	if s.downloadState == nil {
		s.downloadState = &proto.DownloadState{}
	}
	s.downloadState.Stage = stage
	s.downloadState.Percent = percent
}

// setDesktopDownloadFailed 记录一个失败结果。
func (s *Server) setDesktopDownloadFailed(tag, message string) {
	s.downloadMu.Lock()
	defer s.downloadMu.Unlock()
	s.downloadState = &proto.DownloadState{Stage: "failed", Tag: tag, Percent: -1, Error: message}
}

// setDesktopDownloadDone 记录成功结果。
func (s *Server) setDesktopDownloadDone(tag, path string, opened bool) {
	s.downloadMu.Lock()
	defer s.downloadMu.Unlock()
	s.downloadState = &proto.DownloadState{Stage: "done", Tag: tag, Percent: 100, Path: path, Opened: opened}
}

// finishDesktopDownloadError 将内部错误转成 502，并保留 failed 状态。
func (s *Server) finishDesktopDownloadError(w http.ResponseWriter, tag, action string, err error) {
	s.log.Error("桌面安装包下载失败", "tag", tag, "action", action, "cause", err)
	s.setDesktopDownloadFailed(tag, action+": "+err.Error())
	writeJSON(w, http.StatusBadGateway, map[string]string{"error": action + ": " + err.Error()})
}

// finishDesktopDownloadStatus 写指定状态码的失败结果。
func (s *Server) finishDesktopDownloadStatus(w http.ResponseWriter, tag string, status int, message string) {
	s.setDesktopDownloadFailed(tag, message)
	writeJSON(w, status, map[string]string{"error": message})
}

// desktopDownloadSnapshot 返回线程安全的下载状态副本。
func (s *Server) desktopDownloadSnapshot() proto.DownloadState {
	s.downloadMu.Lock()
	defer s.downloadMu.Unlock()
	if s.downloadState == nil {
		return proto.DownloadState{Stage: "idle", Percent: -1}
	}
	return *s.downloadState
}

// sha256Hex 返回字节的十六进制 sha256。
func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// cleanupOldDesktopDownloads 删除同目录里旧的 handoff-desktop_* 普通文件。
//
// 参数：dir 是下载目录，keep 是当前安装包文件名。
// 返回：成功删除的文件数量；目录读取或单文件删除失败只记日志并继续清理。
func cleanupOldDesktopDownloads(log *slog.Logger, dir, keep string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Error("清理旧安装包失败", "dir", dir, "cause", err)
		return 0
	}
	removed := 0
	for _, entry := range entries {
		if entry.Name() == keep || !strings.HasPrefix(entry.Name(), "handoff-desktop_") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			log.Warn("检查旧安装包失败", "path", filepath.Join(dir, entry.Name()), "cause", err)
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if err := os.Remove(path); err != nil {
			log.Warn("删除旧安装包失败", "path", path, "cause", err)
			continue
		}
		removed++
	}
	return removed
}
