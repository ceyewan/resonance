package pi

import (
	"encoding/json"
	"fmt"

	pilotruntime "github.com/ceyewan/resonance/pilot/runtime"
)

type eventDisposition uint8

const (
	eventIgnored eventDisposition = iota
	eventMapped
	eventSettled
)

type messageUpdate struct {
	AssistantMessageEvent struct {
		Type  string  `json:"type"`
		Delta *string `json:"delta"`
	} `json:"assistantMessageEvent"`
}

type toolExecutionEvent struct {
	ToolCallID string          `json:"toolCallId"`
	ToolName   string          `json:"toolName"`
	Args       json.RawMessage `json:"args"`          // 只用于协议校验，绝不越过 mapper 边界。
	Partial    json.RawMessage `json:"partialResult"` // 只用于协议校验，绝不越过 mapper 边界。
	Result     json.RawMessage `json:"result"`        // 只用于协议校验，绝不越过 mapper 边界。
	IsError    *bool           `json:"isError"`
}

type compactionEvent struct {
	Reason    string `json:"reason"`
	Aborted   *bool  `json:"aborted"`
	WillRetry *bool  `json:"willRetry"`
}

type retryEvent struct {
	Attempt     *int64 `json:"attempt"`
	MaxAttempts *int64 `json:"maxAttempts"`
	DelayMS     *int64 `json:"delayMs"`
	Success     *bool  `json:"success"`
}

type extensionErrorEvent struct {
	ExtensionPath string `json:"extensionPath"`
	Event         string `json:"event"`
	Error         string `json:"error"`
}

func mapWireEvent(event WireEvent, sequence uint64) (pilotruntime.RuntimeEvent, eventDisposition, error) {
	mapped := pilotruntime.RuntimeEvent{Sequence: sequence}
	switch event.Type {
	case "agent_start":
		mapped.Kind = pilotruntime.EventStarted
		return mapped, eventMapped, nil
	case "agent_end", "turn_start", "turn_end", "message_start", "message_end", "queue_update",
		"summarization_retry_scheduled", "summarization_retry_attempt_start", "summarization_retry_finished":
		return mapped, eventIgnored, nil
	case "agent_settled":
		return mapped, eventSettled, nil
	case "message_update":
		var update messageUpdate
		if err := json.Unmarshal(event.Raw, &update); err != nil {
			return mapped, eventIgnored, protocolFieldError(event.Type, err)
		}
		switch update.AssistantMessageEvent.Type {
		case "text_delta":
			if update.AssistantMessageEvent.Delta == nil {
				return mapped, eventIgnored, protocolFieldError(event.Type, fmt.Errorf("text_delta missing delta"))
			}
			mapped.Kind = pilotruntime.EventTextDelta
			mapped.Text = *update.AssistantMessageEvent.Delta
			return mapped, eventMapped, nil
		case "text_start", "text_end", "thinking_start", "thinking_delta", "thinking_end",
			"toolcall_start", "toolcall_delta", "toolcall_end":
			return mapped, eventIgnored, nil
		default:
			// 新增的非关键 delta 保持向前兼容。
			return mapped, eventIgnored, nil
		}
	case "tool_execution_start", "tool_execution_update", "tool_execution_end":
		var tool toolExecutionEvent
		if err := json.Unmarshal(event.Raw, &tool); err != nil {
			return mapped, eventIgnored, protocolFieldError(event.Type, err)
		}
		if tool.ToolCallID == "" || tool.ToolName == "" {
			return mapped, eventIgnored, protocolFieldError(event.Type, fmt.Errorf("missing toolCallId or toolName"))
		}
		switch event.Type {
		case "tool_execution_start":
			if !rawFieldPresent(tool.Args) {
				return mapped, eventIgnored, protocolFieldError(event.Type, fmt.Errorf("missing args"))
			}
		case "tool_execution_update":
			if !rawFieldPresent(tool.Args) || !rawFieldPresent(tool.Partial) {
				return mapped, eventIgnored, protocolFieldError(event.Type, fmt.Errorf("missing args or partialResult"))
			}
		case "tool_execution_end":
			if !rawFieldPresent(tool.Result) || tool.IsError == nil {
				return mapped, eventIgnored, protocolFieldError(event.Type, fmt.Errorf("missing result or isError"))
			}
		}
		isError := false
		if tool.IsError != nil {
			isError = *tool.IsError
		}
		mapped.Tool = &pilotruntime.ToolEvent{
			CallID:  tool.ToolCallID,
			Name:    tool.ToolName,
			IsError: isError,
		}
		switch event.Type {
		case "tool_execution_start":
			mapped.Kind = pilotruntime.EventToolStarted
		case "tool_execution_update":
			mapped.Kind = pilotruntime.EventToolUpdated
		case "tool_execution_end":
			mapped.Kind = pilotruntime.EventToolEnded
		}
		return mapped, eventMapped, nil
	case "compaction_start":
		var compaction compactionEvent
		if err := json.Unmarshal(event.Raw, &compaction); err != nil || !validCompactionReason(compaction.Reason) {
			return mapped, eventIgnored, protocolFieldError(event.Type, fmt.Errorf("missing or invalid reason"))
		}
		mapped.Kind = pilotruntime.EventCompactionStarted
		return mapped, eventMapped, nil
	case "compaction_end":
		var compaction compactionEvent
		if err := json.Unmarshal(event.Raw, &compaction); err != nil || !validCompactionReason(compaction.Reason) ||
			compaction.Aborted == nil || compaction.WillRetry == nil {
			return mapped, eventIgnored, protocolFieldError(event.Type, fmt.Errorf("missing or invalid compaction fields"))
		}
		mapped.Kind = pilotruntime.EventCompactionEnded
		return mapped, eventMapped, nil
	case "auto_retry_start":
		var retry retryEvent
		if err := json.Unmarshal(event.Raw, &retry); err != nil || retry.Attempt == nil || retry.MaxAttempts == nil ||
			retry.DelayMS == nil || *retry.Attempt <= 0 || *retry.MaxAttempts < *retry.Attempt || *retry.DelayMS < 0 {
			return mapped, eventIgnored, protocolFieldError(event.Type, fmt.Errorf("missing or invalid retry fields"))
		}
		mapped.Kind = pilotruntime.EventRetryStarted
		return mapped, eventMapped, nil
	case "auto_retry_end":
		var retry retryEvent
		if err := json.Unmarshal(event.Raw, &retry); err != nil || retry.Attempt == nil || retry.Success == nil || *retry.Attempt <= 0 {
			return mapped, eventIgnored, protocolFieldError(event.Type, fmt.Errorf("missing or invalid retry fields"))
		}
		mapped.Kind = pilotruntime.EventRetryEnded
		return mapped, eventMapped, nil
	case "extension_error":
		var extensionErr extensionErrorEvent
		if err := json.Unmarshal(event.Raw, &extensionErr); err != nil {
			return mapped, eventIgnored, protocolFieldError(event.Type, err)
		}
		if extensionErr.Event == "" || extensionErr.Error == "" {
			return mapped, eventIgnored, protocolFieldError(event.Type, fmt.Errorf("missing event or error"))
		}
		return mapped, eventIgnored, fmt.Errorf("pi extension failed during %q: %s",
			extensionErr.Event, safeDiagnostic(extensionErr.Error))
	default:
		// 未知顶层事件只计数/忽略；具体指标在 Pilot observability 切片接入。
		return mapped, eventIgnored, nil
	}
}

func rawFieldPresent(value json.RawMessage) bool {
	return len(value) > 0 && string(value) != "null"
}

func validCompactionReason(reason string) bool {
	return reason == "manual" || reason == "threshold" || reason == "overflow"
}

func protocolFieldError(eventType string, cause error) error {
	return &ProtocolError{
		Kind:    ErrMalformedJSON,
		Preview: fmt.Sprintf("known event %q has invalid fields", eventType),
		Cause:   cause,
	}
}
