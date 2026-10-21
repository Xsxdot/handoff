// artifacts_api.go —— B452 S8 产物下载接口 GET /api/tasks/{id}/artifacts（F16）。
//
// 职责：把执行机上 <DataDir>/tasks/<id>/artifacts 打成 tar.gz 整体发出；空目录 204，
// 超上限 413。与 bundle 同法：先落临时文件再带 Content-Length 整体发，打包中途失败不会
// 伪装成截断的 200。
//
// 边界：只收普通文件与目录，**跳过符号链接与其他特殊文件**（防止把 ~/.ssh 之类链出机外）；
// 条目名一律为相对产物目录的 / 分隔路径。跨机由路由层（machineFirst + byTask）转发，本文件
// 只处理本机任务。
package agentd

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Xsxdot/handoff/internal/orchestration"
)

// artifactsMaxBytes 是产物打包的总大小上限（F16：512 MiB）。变量而非常量：测试注入小上限。
var artifactsMaxBytes int64 = 512 << 20

// machineFirst 让显式 ?machine= 先于按任务归属路由生效（F15/F16）。
func (s *Server) machineFirst(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.forwardIfRequested(w, r) {
			return
		}
		next(w, r)
	}
}

// artifactEntry 是待打包的一项（相对路径 + 绝对路径 + 是否目录）。
type artifactEntry struct {
	rel, abs string
	dir      bool
	size     int64
	mode     os.FileMode
	modTime  time.Time
}

// collectArtifacts 遍历产物目录，返回可打包条目与普通文件总字节数。符号链接与特殊文件跳过。
func collectArtifacts(root string) ([]artifactEntry, int64, int, error) {
	var out []artifactEntry
	var total int64
	files := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		info, ierr := d.Info() // WalkDir 的 Info 不跟随符号链接
		if ierr != nil {
			return ierr
		}
		switch {
		case d.IsDir():
			out = append(out, artifactEntry{rel: filepath.ToSlash(rel), abs: p, dir: true, mode: info.Mode().Perm(), modTime: info.ModTime()})
		case info.Mode().IsRegular():
			out = append(out, artifactEntry{rel: filepath.ToSlash(rel), abs: p, size: info.Size(), mode: info.Mode().Perm(), modTime: info.ModTime()})
			total += info.Size()
			files++
		default:
			// 符号链接、设备、管道：一律不出机（门禁：产物通道不是任意文件下载器）
		}
		return nil
	})
	return out, total, files, err
}

// writeArtifactsTarGz 把条目写成 tar.gz 到 w。
func writeArtifactsTarGz(w io.Writer, entries []artifactEntry) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.rel, Mode: int64(e.mode), ModTime: e.modTime}
		if e.dir {
			hdr.Typeflag = tar.TypeDir
			hdr.Name += "/"
		} else {
			hdr.Typeflag = tar.TypeReg
			hdr.Size = e.size
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if e.dir {
			continue
		}
		f, err := os.Open(e.abs)
		if err != nil {
			return err
		}
		n, err := io.Copy(tw, f)
		_ = f.Close()
		if err != nil {
			return err
		}
		if n != e.size {
			return fmt.Errorf("文件 %s 在打包中途变化（%d → %d 字节）", e.rel, e.size, n)
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// handleTaskArtifacts 处理 GET /api/tasks/{id}/artifacts（F16）：200 tar.gz / 204 空 / 404 任务不存在 / 413 超限。
func (s *Server) handleTaskArtifacts(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	s.log.Info("产物下载请求", "task", taskID, "machine", s.selfLabel())
	if _, ok := s.taskOrErr(w, taskID); !ok {
		return
	}
	root := orchestration.TaskArtifactsDir(s.conf().DataDir, taskID)
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		s.log.Info("产物目录不存在，回 204", "task", taskID, "dir", root)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	entries, total, files, err := collectArtifacts(root)
	if err != nil {
		s.log.Error("遍历产物目录失败", "task", taskID, "dir", root, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": truncateRunes(err.Error(), 200)})
		return
	}
	if files == 0 {
		s.log.Info("产物目录无文件，回 204", "task", taskID, "dir", root)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if total > artifactsMaxBytes {
		s.log.Warn("产物超过打包上限", "task", taskID, "bytes", total, "limit", artifactsMaxBytes)
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": fmt.Sprintf(
			"产物共 %d 字节，超过上限 %d 字节；请在执行机上直接取 %s", total, artifactsMaxBytes, root)})
		return
	}
	tmp, err := os.CreateTemp("", "handoff-artifacts-*.tar.gz")
	if err != nil {
		s.log.Error("创建产物打包临时文件失败", "task", taskID, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": truncateRunes(err.Error(), 200)})
		return
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if err := writeArtifactsTarGz(tmp, entries); err != nil {
		s.log.Error("产物打包失败", "task", taskID, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": truncateRunes(err.Error(), 200)})
		return
	}
	size, err := tmp.Seek(0, io.SeekCurrent)
	if err == nil {
		_, err = tmp.Seek(0, io.SeekStart)
	}
	if err != nil {
		s.log.Error("产物打包临时文件定位失败", "task", taskID, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": truncateRunes(err.Error(), 200)})
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	n, err := io.Copy(w, tmp)
	if err != nil {
		s.log.Error("发送产物包中断", "task", taskID, "sent", n, "total", size, "cause", err)
		return
	}
	s.log.Info("产物包发送完成", "task", taskID, "files", files, "raw_bytes", total, "gz_bytes", n)
}
