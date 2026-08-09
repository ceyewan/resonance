package event

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	"github.com/ceyewan/resonance/model"
)

func TestBuildMessageEventFromModelRedactsRecalledContent(t *testing.T) {
	recalledAt := time.Date(2026, 8, 9, 3, 0, 0, 0, time.UTC)
	event := BuildMessageEventFromModel("conversation-a", &model.MessageContent{
		EventID: 10, SeqID: 2, SessionID: "conversation-a", SenderUsername: "alice",
		MsgType: int(commonv1.MessageType_MESSAGE_TYPE_TEXT), Content: "private recalled text",
		RecalledAt: &recalledAt, CreatedAt: recalledAt.Add(-time.Minute),
	})

	require.Equal(t, commonv1.MessageType_MESSAGE_TYPE_UNSPECIFIED, event.GetMessage().GetType())
	require.Empty(t, event.GetMessage().GetContent())
	require.True(t, event.GetMessage().GetRecalled())
	require.NotContains(t, event.String(), "private recalled text")
}
