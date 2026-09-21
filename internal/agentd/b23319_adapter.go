// 本文件是 B233.19 迁出（项目登记/事件镜像/预览仓主 → internal/workspace）后
// gateway 侧的注入适配器：workspace 侧窄接口（RemoteTaskSource /
// ProjectTreeSource / PreviewStore）与本机具体实现（targetclient 池、store）
// 之间的翻译层。
//
// 边界：
//   - 只做类型与语义适配，不承载域逻辑；域逻辑在 internal/workspace
//   - MarkForwarded（一跳封顶）语义在这里包进 RemoteTaskClient / ProjectTreeClient
//   - preview 行类型经 PreviewRow ↔ store.PreviewRecord 互转；持久层错误原样穿透
package agentd

import (
	"fmt"
	"time"

	"github.com/Xsxdot/handoff/internal/store"
	"github.com/Xsxdot/handoff/internal/targetclient"
	"github.com/Xsxdot/handoff/internal/workspace"
)

// MirrorTaskSource 把 *targetclient.Pool 适配成 workspace.RemoteTaskSource，
// 供 cmd 装配 workspace.NewMirror。
type MirrorTaskSource struct {
	pool *targetclient.Pool
}

// NewMirrorTaskSource 返回事件镜像的 target 源适配器。
func NewMirrorTaskSource(pool *targetclient.Pool) workspace.RemoteTaskSource {
	return MirrorTaskSource{pool: pool}
}

// Names 透传池的活机器清单（判据只有一处：池）。
func (s MirrorTaskSource) Names() []string { return s.pool.Names() }

// For 取该机器的客户端并包上 MarkForwarded 语义（一跳封顶：对端不再扇出）。
func (s MirrorTaskSource) For(name string) (workspace.RemoteTaskClient, error) {
	c, err := s.pool.For(name)
	if err != nil {
		return nil, err
	}
	return c.MarkForwarded(), nil
}

// ProjectTreeSource 把 *targetclient.Pool 适配成 workspace.ProjectTreeSource，
// 供项目树扇出（workspace.FanoutProjectTree）使用。
type ProjectTreeSource struct {
	pool *targetclient.Pool
}

// NewProjectTreeSource 返回项目树扇出的 target 源适配器。
func NewProjectTreeSource(pool *targetclient.Pool) workspace.ProjectTreeSource {
	return ProjectTreeSource{pool: pool}
}

// Names 透传池的活机器清单。
func (s ProjectTreeSource) Names() []string { return s.pool.Names() }

// For 取该机器的客户端并包上 MarkForwarded 语义（一跳封顶）。
func (s ProjectTreeSource) For(name string) (workspace.ProjectTreeClient, error) {
	c, err := s.pool.For(name)
	if err != nil {
		return nil, err
	}
	return c.MarkForwarded(), nil
}

// previewStore 把 *store.Store 适配成 workspace.PreviewStore：行类型经
// PreviewRow 互转，TouchPreview 的「未命中」包成与原实现逐字节同文案的
// not-found 错误。
type previewStore struct {
	st *store.Store
}

// NewPreviewStore 返回预览 owner 的持久化适配器，供 cmd 装配
// workspace.NewPreviewOwner。
func NewPreviewStore(st *store.Store) workspace.PreviewStore {
	return previewStore{st: st}
}

func previewRowFromStore(r store.PreviewRecord) workspace.PreviewRow {
	return workspace.PreviewRow{
		Session: r.Session,
		Source: workspace.PreviewSource{
			Kind:          r.Source.Kind,
			Port:          r.Source.Port,
			WorkspaceRoot: r.Source.WorkspaceRoot,
			RelativePath:  r.Source.RelativePath,
		},
		LastActiveAt: r.LastActiveAt,
		ClosedAt:     r.ClosedAt,
	}
}

func previewRowToStore(r workspace.PreviewRow) store.PreviewRecord {
	return store.PreviewRecord{
		Session: r.Session,
		Source: store.PreviewSource{
			Kind:          r.Source.Kind,
			Port:          r.Source.Port,
			WorkspaceRoot: r.Source.WorkspaceRoot,
			RelativePath:  r.Source.RelativePath,
		},
		LastActiveAt: r.LastActiveAt,
		ClosedAt:     r.ClosedAt,
	}
}

func (a previewStore) InsertPreview(row workspace.PreviewRow) error {
	return a.st.InsertPreview(previewRowToStore(row))
}

func (a previewStore) GetPreview(id string) (workspace.PreviewRow, error) {
	r, err := a.st.GetPreview(id)
	return previewRowFromStore(r), err
}

func (a previewStore) ListActivePreviews(now time.Time) ([]workspace.PreviewRow, error) {
	rows, err := a.st.ListActivePreviews(now)
	if err != nil {
		return nil, err
	}
	out := make([]workspace.PreviewRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, previewRowFromStore(r))
	}
	return out, nil
}

func (a previewStore) ClosePreview(id string, at time.Time) (workspace.PreviewRow, bool, error) {
	r, changed, err := a.st.ClosePreview(id, at)
	return previewRowFromStore(r), changed, err
}

func (a previewStore) TouchPreview(id string, at time.Time) error {
	ok, err := a.st.TouchPreview(id, at)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("预览会话 %s: %w", id, store.ErrNotFound)
	}
	return nil
}

func (a previewStore) ExpirePreviews(now time.Time) ([]workspace.PreviewRow, error) {
	rows, err := a.st.ExpirePreviews(now)
	if err != nil {
		return nil, err
	}
	out := make([]workspace.PreviewRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, previewRowFromStore(r))
	}
	return out, nil
}

func (a previewStore) UpdatePreviewEntry(id, entryURL string) error {
	return a.st.UpdatePreviewEntry(id, entryURL)
}

// 编译期锚：适配器必须始终满足 workspace 侧窄接口。
var (
	_ workspace.RemoteTaskSource  = MirrorTaskSource{}
	_ workspace.ProjectTreeSource = ProjectTreeSource{}
	_ workspace.PreviewStore      = previewStore{}
)
