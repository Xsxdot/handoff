// peer Workspace 资源 authority registry。
//
// 职责：
//   - 按稳定 machine_id 持有远端 agentd Client
//   - 从 SyncMachine endpoint + SecretRef resolver 构造 peer authority
//
// 边界：
//   - token 只进入 Client Authorization header，不进入控制面 Machine 或返回值
//   - registry 不判断机器在线状态；resourcegateway 先用控制面状态门禁
package peer

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/xushixin/handoff/internal/controlplane"
	"github.com/xushixin/handoff/internal/desktopapi"
	"github.com/xushixin/handoff/internal/workspaceapi"
)

// AuthorityRegistry 是 resourcegateway.PeerAuthorityResolver 的实现。
type AuthorityRegistry struct {
	mu      sync.RWMutex
	clients map[string]*Client
}

// CommanderForMachine 返回指定机器的远端项目命令客户端。
func (r *AuthorityRegistry) CommanderForMachine(_ context.Context, machineID string) (controlplane.MachineCommander, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	client := r.clients[machineID]
	if client == nil {
		return nil, fmt.Errorf("%w: 未配置 machine_id=%s 的 peer client", ErrUnavailable, machineID)
	}
	return client, nil
}

// NewAuthorityRegistry 从配置机器构造资源 client registry。
func NewAuthorityRegistry(machines []SyncMachine, credentialResolver func(string) string) *AuthorityRegistry {
	registry := &AuthorityRegistry{clients: make(map[string]*Client, len(machines))}
	for _, machine := range machines {
		token := ""
		if credentialResolver != nil {
			token = credentialResolver(machine.SecretRef)
		}
		registry.clients[machine.MachineID] = NewClient(ClientConfig{Endpoint: machine.Endpoint, Token: token})
	}
	return registry
}

// AuthorityForMachine 返回指定机器的远端资源 authority。
func (r *AuthorityRegistry) AuthorityForMachine(_ context.Context, machineID string) (workspaceapi.Authority, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	client := r.clients[machineID]
	if client == nil {
		return nil, fmt.Errorf("%w: 未配置 machine_id=%s 的 peer client", ErrUnavailable, machineID)
	}
	return client, nil
}

// Close 关闭全部 peer client。
func (r *AuthorityRegistry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, client := range r.clients {
		client.Close()
	}
}

// ForwardPreviewProxy 把 desktop 的 Preview 流量转发到远端 owner agentd。
func (r *AuthorityRegistry) ForwardPreviewProxy(w http.ResponseWriter, req *http.Request, machineID, nonce string) {
	r.mu.RLock()
	client := r.clients[machineID]
	r.mu.RUnlock()
	if client == nil {
		desktopapi.WriteProblem(w, http.StatusServiceUnavailable, desktopapi.Problem{
			Code: desktopapi.ProblemMachineOffline, Message: "远端开发机当前不可用", Retryable: true,
		})
		return
	}
	client.PreviewProxy(w, req, nonce)
}

// ClosePreviewSession 关闭远端 owner 的 Preview 会话。
func (r *AuthorityRegistry) ClosePreviewSession(ctx context.Context, machineID, previewSessionID string) error {
	r.mu.RLock()
	client := r.clients[machineID]
	r.mu.RUnlock()
	if client == nil {
		return fmt.Errorf("%w: 未配置 machine_id=%s 的 peer client", ErrUnavailable, machineID)
	}
	return client.ClosePreview(ctx, previewSessionID)
}
