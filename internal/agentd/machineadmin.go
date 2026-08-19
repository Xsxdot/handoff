// 本文件实现开发机的增删：校验、写时复制落盘。
//
// 职责：
//   - 校验新增请求（名字、地址、令牌）并判重名
//   - 把改动落进配置文件（经 Server.swapConf）
//
// 边界：
//   - **不做 HTTP 编解码**：状态码与响应体由 machines.go 的 handler 决定
//   - **不做网络探测**：可达性探测是 I/O，属 handler 层，本文件保持纯逻辑
//   - **不建表**：机器的真相仍是 config.yaml 的 targets 段（同 machines.go）
package agentd

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
)

// ErrMachineExists 表示同名开发机已存在，调用方应答 409。
var ErrMachineExists = errors.New("同名开发机已存在")

// ErrMachineNotFound 表示要删的开发机不存在，调用方应答 404。
var ErrMachineNotFound = errors.New("开发机不存在")

// validateAddMachine 校验新增开发机的请求。
//
// 参数：
//   - req: 请求体
//   - existing: 现有 targets，用于判重名
//
// 返回：
//   - 包装了 ErrMachineExists 的错误表示重名（调用方应答 409）
//   - 其余非 nil 错误都是请求体本身的问题（调用方应答 400）
func validateAddMachine(req proto.AddMachineReq, existing map[string]config.Target) error {
	// 空名字就是本机的保留名（proto.Machine 里 Name=="" 即本机），必须挡住
	if req.Name == "" {
		return errors.New("name 不能为空")
	}
	if strings.ContainsAny(req.Name, " \t\r\n") {
		return errors.New("name 不能含空白字符")
	}
	if _, ok := existing[req.Name]; ok {
		return fmt.Errorf("%w: %s", ErrMachineExists, req.Name)
	}
	if _, _, err := net.SplitHostPort(req.Addr); err != nil {
		return fmt.Errorf("addr 需形如 host:port: %w", err)
	}
	if req.Token == "" {
		return errors.New("token 不能为空")
	}
	return nil
}

// addMachine 校验并把一台开发机写入配置、落盘。
//
// 参数：
//   - req: 已由 handler 反序列化的请求体（Force 在本层无意义，探测在 handler）
//
// 返回：
//   - 校验错误（可能包装 ErrMachineExists）或落盘错误；成功时 nil
//
// 注意：重名判定在 swapConf 的临界区内再做一次。校验时的那次是为了尽早
// 返回清晰的错误，但两次请求并发到达时只有临界区内的判定作数。
func (s *Server) addMachine(req proto.AddMachineReq) error {
	if err := validateAddMachine(req, s.conf().Targets); err != nil {
		return err
	}
	// 只打名字与地址，绝不打 token
	s.log.Info("新增开发机", "name", req.Name, "addr", req.Addr, "user", req.User)
	return s.swapConf(func(c *config.Config) error {
		if _, ok := c.Targets[req.Name]; ok {
			return fmt.Errorf("%w: %s", ErrMachineExists, req.Name)
		}
		c.Targets[req.Name] = config.Target{Addr: req.Addr, Token: req.Token, User: req.User}
		return nil
	})
}

// removeMachine 从配置里删除一台开发机并落盘。
//
// 参数：
//   - name: 机器名（不能为空；空串是本机的保留名）
//
// 返回：
//   - 包装了 ErrMachineNotFound 的错误表示不存在（调用方应答 404）
func (s *Server) removeMachine(name string) error {
	if name == "" {
		return fmt.Errorf("%w: 名字为空", ErrMachineNotFound)
	}
	s.log.Info("删除开发机", "name", name)
	return s.swapConf(func(c *config.Config) error {
		if _, ok := c.Targets[name]; !ok {
			return fmt.Errorf("%w: %s", ErrMachineNotFound, name)
		}
		delete(c.Targets, name)
		return nil
	})
}
