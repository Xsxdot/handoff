// Package relay implements the coordinator/executor side of the relay tunnel.
//
// 职责：relay 控制阶段线格式与 WSS 交换（handoff 端）；边界：与
// handoff-server internal/wire 是同一契约的两侧，改动必须同步。
package relay

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/coder/websocket"
)

// FrameType identifies a relay control frame.
type FrameType string

const (
	Register   FrameType = "register"
	Connect    FrameType = "connect"
	Registered FrameType = "registered"
	ConnectOK  FrameType = "connect_ok"
	Error      FrameType = "error"
)

// Control error codes are shared with the relay server wire contract.
const (
	ErrInvalidFrame = "invalid_frame"
	ErrUnauthorized = "unauthorized"
	ErrCredential   = "invalid_credential"
	ErrNodeNotFound = "node_not_found"
	ErrNodeBusy     = "node_busy"
	ErrInternal     = "internal"
)

// Frame is a JSON relay control frame. Account is returned by successful
// REGISTER/CONNECT responses and is intentionally absent from requests.
type Frame struct {
	Type       FrameType `json:"type"`
	Node       string    `json:"node,omitempty"`
	Credential string    `json:"credential,omitempty"`
	Account    string    `json:"account,omitempty"`
	Code       string    `json:"code,omitempty"`
	Message    string    `json:"message,omitempty"`
}

// Encode serializes one control frame using the relay wire format.
func Encode(f Frame) ([]byte, error) {
	if !knownFrameType(f.Type) {
		return nil, fmt.Errorf("relay frame: unknown type %q", f.Type)
	}
	b, err := json.Marshal(f)
	if err != nil {
		return nil, fmt.Errorf("encode relay frame: %w", err)
	}
	return b, nil
}

// Decode parses one control frame and rejects unknown frame types.
func Decode(b []byte) (Frame, error) {
	var f Frame
	if err := json.Unmarshal(b, &f); err != nil {
		return Frame{}, fmt.Errorf("decode relay frame: %w", err)
	}
	if !knownFrameType(f.Type) {
		return Frame{}, fmt.Errorf("decode relay frame: unknown type %q", f.Type)
	}
	return f, nil
}

func knownFrameType(t FrameType) bool {
	switch t {
	case Register, Connect, Registered, ConnectOK, Error:
		return true
	default:
		return false
	}
}

// sendControl sends a single control frame as a WebSocket text message.
func sendControl(ctx context.Context, ws *websocket.Conn, f Frame) error {
	b, err := Encode(f)
	if err != nil {
		return err
	}
	if err := ws.Write(ctx, websocket.MessageText, b); err != nil {
		return fmt.Errorf("send relay control frame: %w", err)
	}
	return nil
}

// recvControl reads one text control message. An Error frame is returned as a
// ControlError so callers can preserve the relay's machine-readable code.
func recvControl(ctx context.Context, ws *websocket.Conn) (Frame, error) {
	typ, b, err := ws.Read(ctx)
	if err != nil {
		return Frame{}, fmt.Errorf("receive relay control frame: %w", err)
	}
	if typ != websocket.MessageText {
		return Frame{}, fmt.Errorf("receive relay control frame: expected text message, got %s", typ)
	}
	f, err := Decode(b)
	if err != nil {
		return Frame{}, err
	}
	if f.Type == Error {
		return Frame{}, &ControlError{Code: f.Code, Message: f.Message}
	}
	return f, nil
}

// ControlError reports an Error frame returned by the relay.
type ControlError struct {
	Code    string
	Message string
}

func (e *ControlError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}
