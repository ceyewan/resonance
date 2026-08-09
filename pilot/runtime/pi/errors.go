package pi

import (
	"errors"
	"fmt"
)

var (
	// ErrFrameTooLarge 表示单个 JSONL frame 超过硬限制。
	ErrFrameTooLarge = errors.New("pi rpc frame too large")
	// ErrOutputTooLarge 表示一次进程 stdout 累计输出超过硬限制。
	ErrOutputTooLarge = errors.New("pi rpc output too large")
	// ErrMalformedJSON 表示 stdout 出现非 JSON 协议内容或无效 JSON。
	ErrMalformedJSON = errors.New("pi rpc malformed json")
	// ErrEventBackpressure 表示上层未及时消费事件，协议泵主动失败以保持内存有界。
	ErrEventBackpressure = errors.New("pi rpc event backpressure")
	// ErrRunNotFound 表示指定 Run 不在当前 Adapter 的活动集合中。
	ErrRunNotFound = errors.New("pi runtime run not found")
	// ErrRunExists 表示同一 Run ID 已经在执行。
	ErrRunExists = errors.New("pi runtime run already exists")
	// ErrRuntimeNotReady 表示尚未通过固定版本 Probe。
	ErrRuntimeNotReady = errors.New("pi runtime has not passed version probe")
	// ErrRuntimeClosed 表示 Adapter 已进入永久关闭状态。
	ErrRuntimeClosed = errors.New("pi runtime is shutting down")
)

// CommandOutcomeUnknownError 表示命令 frame 已经至少部分写入，但 ACK 未知。
// 调用方必须按“可能已执行”处理，不能把它当成安全的未发送失败。
type CommandOutcomeUnknownError struct {
	Command string
	Cause   error
}

func (e *CommandOutcomeUnknownError) Error() string {
	return fmt.Sprintf("pi rpc command %q outcome is unknown: %v", e.Command, e.Cause)
}

func (e *CommandOutcomeUnknownError) Unwrap() error { return e.Cause }

// CommandError 表示 Pi 拒绝了一个 RPC 命令。
type CommandError struct {
	Command string
	Message string
}

func (e *CommandError) Error() string {
	return fmt.Sprintf("pi rpc command %q failed: %s", e.Command, safeDiagnostic(e.Message))
}

// ProtocolError 表示 stdout 协议违反约定。
// Preview 只包含有限长度的诊断片段，不能存放完整 Prompt 或 Tool Result。
type ProtocolError struct {
	Kind    error
	Preview string
	Cause   error
}

func (e *ProtocolError) Error() string {
	if e.Preview == "" {
		return fmt.Sprintf("pi rpc protocol error: %v", e.Kind)
	}
	return fmt.Sprintf("pi rpc protocol error: %v (%s)", e.Kind, e.Preview)
}

func (e *ProtocolError) Unwrap() error {
	if e.Cause != nil {
		return errors.Join(e.Kind, e.Cause)
	}
	return e.Kind
}

func safeDiagnostic(message string) string {
	if message == "" {
		return "no diagnostic"
	}
	return safePreview([]byte(message))
}
