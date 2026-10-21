package agentd

// b452_artifacts_api_test.go —— B452 F15/F16 门面侧：产物下载接口与按机读取任务（V7 产物接口条）。
import (
	"archive/tar"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	handoffclient "github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/orchestration"
	"github.com/Xsxdot/handoff/internal/proto"
)

// b452TaskOnB 经 A 的 clientForTarget 在 B 上建一个冻结任务（不经 A 的镜像），返回任务。
func b452TaskOnB(t *testing.T, p *b452Pair) *proto.Task {
	t.Helper()
	home := ""
	cl, _ := p.a.srv.clientForTarget(b452MachineB)
	task, err := cl.Dispatch(context.Background(), handoffclient.DispatchOpts{
		ProjectID: p.projectID, Prompt: "x", Target: b452MachineB, Carrier: b452CarrierB,
		HomeDir: &home, FrozenTarget: b452MachineB, Executor: "fake", NewWorktree: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = cl.Stop(context.Background(), task.ID) })
	return task
}

func b452GetRaw(t *testing.T, h *b452Host, path string) (int, http.Header, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, h.ts.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := h.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, raw
}

func untarGz(t *testing.T, raw []byte) map[string]string {
	t.Helper()
	gz, err := gzip.NewReader(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("解 gzip: %v", err)
	}
	tr := tar.NewReader(gz)
	out := map[string]string{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("读 tar: %v", err)
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir {
			t.Fatalf("包里只应有普通文件与目录，得 %s type=%c", h.Name, h.Typeflag)
		}
		b, _ := io.ReadAll(tr)
		out[h.Name] = string(b)
	}
}

func TestB452ArtifactsEndpoint(t *testing.T) {
	p := newB452Pair(t, nil)
	task := b452TaskOnB(t, p)
	dir := orchestration.TaskArtifactsDir(p.b.srv.conf().DataDir, task.ID)
	path := "/api/tasks/" + task.ID + "/artifacts"

	if code, _, _ := b452GetRaw(t, p.b, path); code != http.StatusNoContent {
		t.Fatalf("空产物目录应 204，得 %d", code)
	}
	if err := os.MkdirAll(filepath.Join(dir, "shots"), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "report.md"), []byte("长报告\n"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "shots", "a.png"), []byte{0x89, 'P', 'N', 'G'}, 0o600)
	home, _ := os.UserHomeDir()
	if err := os.Symlink(home, filepath.Join(dir, "home-link")); err != nil {
		t.Fatal(err)
	}
	code, hdr, raw := b452GetRaw(t, p.b, path)
	if code != http.StatusOK || hdr.Get("Content-Type") != "application/gzip" {
		t.Fatalf("有产物应 200 gzip，得 %d %s", code, hdr.Get("Content-Type"))
	}
	got := untarGz(t, raw)
	if got["report.md"] != "长报告\n" || got["shots/a.png"] != "\x89PNG" {
		t.Fatalf("解包内容应逐字节一致，得 %q", got)
	}
	for name := range got {
		if strings.HasPrefix(name, "home-link") {
			t.Fatalf("符号链接不得出机：%s", name)
		}
	}
	// ?machine=B 经 A 转发得到同一包
	code, _, viaA := b452GetRaw(t, p.a, path+"?machine="+b452MachineB)
	if code != http.StatusOK {
		t.Fatalf("经 A 按机取产物应 200，得 %d", code)
	}
	if g := untarGz(t, viaA); g["report.md"] != got["report.md"] || len(g) != len(got) {
		t.Fatalf("经 A 取到的包应与 B 一致：%q vs %q", g, got)
	}
	// F15：GET /api/tasks/{id}?machine=B 经 A（A 无镜像行）
	code, _, detail := b452GetRaw(t, p.a, "/api/tasks/"+task.ID+"?machine="+b452MachineB)
	if code != http.StatusOK || !strings.Contains(string(detail), `"artifacts_dir":"`+dir+`"`) {
		t.Fatalf("经 A 按机读任务应 200 且带执行机 artifacts_dir，得 %d %s", code, detail)
	}
	// 超上限 → 413
	old := artifactsMaxBytes
	artifactsMaxBytes = 3
	t.Cleanup(func() { artifactsMaxBytes = old })
	if code, _, body := b452GetRaw(t, p.b, path); code != http.StatusRequestEntityTooLarge || !strings.Contains(string(body), "超过上限") {
		t.Fatalf("超上限应 413，得 %d %s", code, body)
	}
}

func TestB452ArtifactsEndpointUnknownTask404(t *testing.T) {
	p := newB452Pair(t, nil)
	if code, _, _ := b452GetRaw(t, p.b, "/api/tasks/no-such/artifacts"); code != http.StatusNotFound {
		t.Fatalf("任务不存在应 404，得 %d", code)
	}
}
