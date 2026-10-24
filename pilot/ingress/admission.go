package ingress

import (
	"context"
	"fmt"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	"github.com/ceyewan/resonance/model"
)

type SessionAuthority interface {
	GetSession(context.Context, string) (*model.Session, error)
}

// Assistant 是服务端唯一助手的冻结快照：入队时写进每一行 Run，客户端不可选择。
type Assistant struct {
	ProfileID      string
	ProfileVersion int64
	RuntimeVersion string
	BridgeVersion  string
	ModelID        string
	MaxAttempts    int
}

// Conversation 是准入结论：这条事件属于哪个租户的哪个 Pilot 私聊。
type Conversation struct {
	Found          bool
	TenantID       string
	ConversationID string
	ActorUsername  string
}

// ConversationAdmission 只回答一个问题：这条 ChatEvent 是否发生在 Bot 的私人会话里。
//
// 它不再查成员表和用户表：Logic 的 SendEvent 已经在写主事实的同一个调用里验证了
// 签名身份、租户、成员资格和"发送者是真人"，再查一遍是对自己上游的不信任，而且
// 会让 Pilot 为全系统每一条消息都做一次额外查询。
type ConversationAdmission struct {
	bot      string
	sessions SessionAuthority
}

func NewConversationAdmission(bot string, sessions SessionAuthority) (*ConversationAdmission, error) {
	if bot == "" || sessions == nil {
		return nil, fmt.Errorf("pilot admission requires a bot and a session reader")
	}
	return &ConversationAdmission{bot: bot, sessions: sessions}, nil
}

func (a *ConversationAdmission) Admit(ctx context.Context, event *commonv1.ChatEvent) (Conversation, error) {
	if event == nil {
		return Conversation{}, ErrAdmissionMismatch
	}
	conversation, err := a.sessions.GetSession(ctx, event.SessionId)
	if err != nil {
		return Conversation{}, err
	}
	if conversation == nil {
		return Conversation{}, ErrAdmissionMismatch
	}
	// 普通会话与 Bot 无关，属于正常流量，直接忽略而不是拒绝。
	if conversation.Kind != model.SessionKindAI || conversation.Type != 1 {
		return Conversation{}, nil
	}
	// AI 私聊只可能是 owner 与 Bot 两人的对话：owner 只能由 owner 本人触发，
	// Bot 自己的消息不能反过来触发新一轮推理。
	if conversation.TenantID == "" || conversation.OwnerUsername != event.FromUsername ||
		event.FromUsername == a.bot {
		return Conversation{}, ErrAdmissionDenied
	}
	return Conversation{
		Found:          true,
		TenantID:       conversation.TenantID,
		ConversationID: conversation.SessionID,
		ActorUsername:  event.FromUsername,
	}, nil
}

func validateAssistant(assistant Assistant) error {
	if assistant.ProfileID == "" || assistant.ProfileVersion < 1 ||
		assistant.RuntimeVersion == "" || assistant.BridgeVersion == "" || assistant.ModelID == "" ||
		assistant.MaxAttempts < 1 || assistant.MaxAttempts > 100 {
		return fmt.Errorf("pilot assistant snapshot is incomplete")
	}
	return nil
}
