package protocol

import (
	"encoding/json"
	"fmt"
)

// SeqWindow is the maximum accepted gap between consecutive inbound sequences.
const SeqWindow = 32

type Type string

const (
	TypeRequest     Type = "request"
	TypeResponse    Type = "response"
	TypeInput       Type = "input"
	TypeOutput      Type = "output"
	TypeResize      Type = "resize"
	TypeSessionExit Type = "session_exit"
)

type Method string

const (
	MethodListAgents   Method = "list_agents"
	MethodStartSession Method = "start_session"
	MethodListSessions Method = "list_sessions"
	MethodStopSession  Method = "stop_session"
	MethodGetHistory   Method = "get_history"
)

// Message is the plaintext protocol unit. Entire messages are encrypted
// before being sent as WebSocket binary frames.
type Message struct {
	Type      Type            `json:"type"`
	Seq       uint64          `json:"seq"`
	ID        string          `json:"id,omitempty"`
	Method    Method          `json:"method,omitempty"`
	SessionID string          `json:"session_id,omitempty"`
	OK        *bool           `json:"ok,omitempty"`
	Error     string          `json:"error,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Data      []byte          `json:"data,omitempty"`
	Rows      uint16          `json:"rows,omitempty"`
	Cols      uint16          `json:"cols,omitempty"`
	Agent     string          `json:"agent,omitempty"`
	ExitCode  *int            `json:"exit_code,omitempty"`
}

type AgentInfo struct {
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	Path      string `json:"path,omitempty"`
}

type ListAgentsResult struct {
	Agents []AgentInfo `json:"agents"`
}

type StartSessionParams struct {
	Agent string `json:"agent"`
	CWD   string `json:"cwd,omitempty"`
}

type StartSessionResult struct {
	SessionID string `json:"session_id"`
	Agent     string `json:"agent"`
}

type SessionInfo struct {
	SessionID string `json:"session_id"`
	Agent     string `json:"agent"`
}

type ListSessionsResult struct {
	Sessions []SessionInfo `json:"sessions"`
}

type StopSessionParams struct {
	SessionID string `json:"session_id"`
}

type GetHistoryParams struct {
	SessionID string `json:"session_id"`
}

type GetHistoryResult struct {
	History []byte `json:"history"`
}

func Encode(msg Message) ([]byte, error) {
	if err := Validate(msg); err != nil {
		return nil, err
	}
	return json.Marshal(msg)
}

func Decode(data []byte) (Message, error) {
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return Message{}, fmt.Errorf("decode message: %w", err)
	}
	if err := Validate(msg); err != nil {
		return Message{}, err
	}
	return msg, nil
}

func Validate(msg Message) error {
	if msg.Seq == 0 {
		return fmt.Errorf("seq is required")
	}
	switch msg.Type {
	case TypeRequest:
		if msg.ID == "" {
			return fmt.Errorf("request requires id")
		}
		if msg.Method == "" {
			return fmt.Errorf("request requires method")
		}
		return validateMethod(msg.Method)
	case TypeResponse:
		if msg.ID == "" {
			return fmt.Errorf("response requires id")
		}
		if msg.OK == nil {
			return fmt.Errorf("response requires ok")
		}
		return nil
	case TypeInput, TypeOutput:
		if msg.SessionID == "" {
			return fmt.Errorf("%s requires session_id", msg.Type)
		}
		return nil
	case TypeResize:
		if msg.SessionID == "" {
			return fmt.Errorf("resize requires session_id")
		}
		if msg.Rows == 0 || msg.Cols == 0 {
			return fmt.Errorf("resize requires non-zero rows and cols")
		}
		return nil
	case TypeSessionExit:
		if msg.SessionID == "" {
			return fmt.Errorf("session_exit requires session_id")
		}
		return nil
	default:
		return fmt.Errorf("unknown message type %q", msg.Type)
	}
}

func validateMethod(method Method) error {
	switch method {
	case MethodListAgents, MethodStartSession, MethodListSessions, MethodStopSession, MethodGetHistory:
		return nil
	default:
		return fmt.Errorf("unknown method %q", method)
	}
}

// AcceptSeq reports whether seq is acceptable given the last accepted recvSeq
// using a sliding window of SeqWindow. On success, returns the updated recvSeq.
func AcceptSeq(recvSeq, seq uint64) (uint64, bool) {
	if seq == 0 {
		return recvSeq, false
	}
	if seq <= recvSeq {
		return recvSeq, false
	}
	if seq > recvSeq+SeqWindow {
		return recvSeq, false
	}
	return seq, true
}

func BoolPtr(v bool) *bool { return &v }

func IntPtr(v int) *int { return &v }

func EncodeParams(v any) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode params: %w", err)
	}
	return b, nil
}

func DecodeParams[T any](raw json.RawMessage) (T, error) {
	var out T
	if len(raw) == 0 {
		return out, fmt.Errorf("missing params")
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("decode params: %w", err)
	}
	return out, nil
}
