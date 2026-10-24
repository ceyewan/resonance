package logic

import (
	"testing"

	"github.com/stretchr/testify/require"

	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
)

func TestPilotOnlyReadsHistoryAndWritesBotMessages(t *testing.T) {
	methods := userPilotServiceMethods()
	require.Len(t, methods, 2)
	require.Contains(t, methods, logicv1.ChatService_SendEvent_FullMethodName)
	require.Contains(t, methods, logicv1.SessionService_GetHistoryEvents_FullMethodName)
	require.NotContains(t, methods, logicv1.SessionService_CreateSession_FullMethodName)
}
