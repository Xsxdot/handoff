// coordinator_sessionref.go —— 协调者 attach 的 SessionRef 解析缝。
//
// 职责：attach 定位在 locator 消费 ref 前，由 resolver 从已登记协调者小队
// 已上线载体补齐空的 HomeDir，并确保展开为绝对路径。
//
// 归属说明（B233.26 刀2）：coordinator_home 的纯函数族（HOME 供给/规范化/
// ShellQuote）已归域 orchestration；本 resolver 因持 *Server（读编制域端口与
// 协调者小队）耦合 gateway 生命周期，按卡内判据（新导出面 >2 个符号）留
// gateway，图对账（刀3）按实然登记其容器。
package agentd

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/Xsxdot/handoff/internal/hostapi"
	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

// coordinatorSessionRefResolver 实现 keystone.SessionRefResolver：
// 在 ref 交给 TerminalLocator 之前，从已登记协调者小队已上线载体补齐空的 HomeDir，
// 并确保展开为绝对路径。
type coordinatorSessionRefResolver struct {
	server        *Server
	expandHomeDir func(string) (string, error)
}

// NewCoordinatorSessionRefResolver 构造 keystone 的 ref 解析缝（B233.18：类型与
// 解析逻辑留在本包，构造上移 cmd 组装点，故导出本构造函数；返回
// keystone.SessionRefResolver 使用方接口）。resolver 经 server 读编制域端口，
// server 不可为 nil。
func NewCoordinatorSessionRefResolver(server *Server,
	expandHomeDir func(string) (string, error)) keystone.SessionRefResolver {
	return coordinatorSessionRefResolver{server: server, expandHomeDir: expandHomeDir}
}

func (r coordinatorSessionRefResolver) ResolveSessionRef(card string, ref keysclient.SessionRef) (keysclient.SessionRef, error) {
	if r.server == nil || r.server.scheduling == nil {
		return ref, errors.New("协调者 attach 无编制域读取端口")
	}
	slog.Default().Info("协调者 SessionRef 解析开始", "card", card, "has_home", ref.HomeDir != "",
		"has_session", ref.SessionID != "")
	if ref.HomeDir == "" {
		squad, err := r.server.resolveCoordinatorSquad()
		if err != nil {
			slog.Default().Error("读取协调者小队失败", "card", card, "cause", err)
			return ref, fmt.Errorf("读取协调者小队以恢复卡 %s HOME: %w", card, err)
		}
		var found bool
		for _, member := range squad.Members {
			carrier, readErr := r.server.scheduling.Carrier(member.Carrier)
			if readErr != nil {
				slog.Default().Error("读取协调者载体失败", "card", card, "carrier", member.Carrier, "cause", readErr)
				return ref, fmt.Errorf("读取协调者载体 %s HOME: %w", member.Carrier, readErr)
			}
			if carrier.Status != scheduling.StatusOnline {
				continue
			}
			ref.HomeDir = carrier.HomeDir
			if strings.TrimSpace(ref.HomeDir) == "" {
				ref.HomeDir = "~"
			}
			found = true
			break
		}
		if !found {
			err := fmt.Errorf("协调者小队 %s 没有已上线载体可恢复 HOME", squad.Name)
			slog.Default().Error("恢复协调者 HOME 失败", "card", card, "squad", squad.Name, "cause", err)
			return ref, err
		}
	}
	expand := r.expandHomeDir
	if expand == nil {
		expand = hostapi.ExpandHomePath
	}
	expanded, err := expand(ref.HomeDir)
	if err != nil {
		slog.Default().Error("展开协调者 attach HOME 失败", "card", card, "home_dir", ref.HomeDir, "cause", err)
		return ref, fmt.Errorf("展开卡 %s 的协调者 attach HOME %q: %w", card, ref.HomeDir, err)
	}
	if !filepath.IsAbs(expanded) {
		err := fmt.Errorf("卡 %s 的协调者 attach HOME 不是绝对路径: %q", card, expanded)
		slog.Default().Error("协调者 attach HOME 非绝对路径", "card", card, "expanded", expanded, "cause", err)
		return ref, err
	}
	ref.HomeDir = expanded
	slog.Default().Info("协调者 SessionRef 解析成功", "card", card, "home_dir", ref.HomeDir)
	return ref, nil
}
