package session

import (
	"xgames/internal/platform/protocol"
)

// newMessage 构造 JSON 信封消息（复用 protocol.NewMessage）
func newMessage(t protocol.MessageType, payload any) protocol.Message {
	return protocol.NewMessage(t, payload)
}
