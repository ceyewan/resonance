package pi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strconv"
	"strings"

	pilotruntime "github.com/ceyewan/resonance/pilot/runtime"
)

type wireHeader struct {
	Type string `json:"type"`
}

type wireResponse struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Command string          `json:"command"`
	Success *bool           `json:"success"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// WireEvent 保留 Pi 原始事件，只有 mapper 可以解释其字段。
type WireEvent struct {
	Type string
	Raw  json.RawMessage
}

// State 是 get_state 中 Pilot 启动核验所需的最小投影。
type State struct {
	Model               *Model `json:"model"`
	IsStreaming         bool   `json:"isStreaming"`
	IsCompacting        bool   `json:"isCompacting"`
	SessionFile         string `json:"sessionFile"`
	SessionID           string `json:"sessionId"`
	AutoCompaction      bool   `json:"autoCompactionEnabled"`
	MessageCount        int64  `json:"messageCount"`
	PendingMessageCount int64  `json:"pendingMessageCount"`
}

func (s *State) UnmarshalJSON(data []byte) error {
	var wire struct {
		Model               *Model `json:"model"`
		IsStreaming         *bool  `json:"isStreaming"`
		IsCompacting        *bool  `json:"isCompacting"`
		SessionFile         string `json:"sessionFile"`
		SessionID           string `json:"sessionId"`
		AutoCompaction      *bool  `json:"autoCompactionEnabled"`
		MessageCount        *int64 `json:"messageCount"`
		PendingMessageCount *int64 `json:"pendingMessageCount"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if wire.IsStreaming == nil || wire.IsCompacting == nil || wire.AutoCompaction == nil ||
		wire.MessageCount == nil || wire.PendingMessageCount == nil {
		return fmt.Errorf("get_state response is missing required boolean or counter fields")
	}
	if *wire.MessageCount < 0 || *wire.PendingMessageCount < 0 {
		return fmt.Errorf("get_state response contains negative counters")
	}
	*s = State{
		Model:               wire.Model,
		IsStreaming:         *wire.IsStreaming,
		IsCompacting:        *wire.IsCompacting,
		SessionFile:         wire.SessionFile,
		SessionID:           wire.SessionID,
		AutoCompaction:      *wire.AutoCompaction,
		MessageCount:        *wire.MessageCount,
		PendingMessageCount: *wire.PendingMessageCount,
	}
	return nil
}

// Model 是 Pi Model 的稳定核验字段。
type Model struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
}

type lastAssistantTextData struct {
	Text *string `json:"text"`
}

// SessionStats 是 get_session_stats 的最小投影。
type SessionStats struct {
	SessionFile string `json:"sessionFile"`
	SessionID   string `json:"sessionId"`
	Tokens      struct {
		Input      int64 `json:"input"`
		Output     int64 `json:"output"`
		CacheRead  int64 `json:"cacheRead"`
		CacheWrite int64 `json:"cacheWrite"`
		Total      int64 `json:"total"`
	} `json:"tokens"`
	Cost      float64  `json:"cost"`
	costExact *big.Rat `json:"-"`
}

func (s *SessionStats) UnmarshalJSON(data []byte) error {
	var wire struct {
		SessionFile string `json:"sessionFile"`
		SessionID   string `json:"sessionId"`
		Tokens      struct {
			Input      *int64 `json:"input"`
			Output     *int64 `json:"output"`
			CacheRead  *int64 `json:"cacheRead"`
			CacheWrite *int64 `json:"cacheWrite"`
			Total      *int64 `json:"total"`
		} `json:"tokens"`
		Cost json.RawMessage `json:"cost"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if wire.Tokens.Input == nil || wire.Tokens.Output == nil || wire.Tokens.CacheRead == nil ||
		wire.Tokens.CacheWrite == nil || wire.Tokens.Total == nil || len(wire.Cost) == 0 || string(wire.Cost) == "null" {
		return fmt.Errorf("get_session_stats response is missing required usage fields")
	}
	cost, costExact, err := parseCost(wire.Cost)
	if err != nil {
		return err
	}
	if *wire.Tokens.Input < 0 || *wire.Tokens.Output < 0 || *wire.Tokens.CacheRead < 0 ||
		*wire.Tokens.CacheWrite < 0 || *wire.Tokens.Total < 0 || costExact.Sign() < 0 {
		return fmt.Errorf("get_session_stats response contains negative usage")
	}
	*s = SessionStats{SessionFile: wire.SessionFile, SessionID: wire.SessionID, Cost: cost, costExact: costExact}
	s.Tokens.Input = *wire.Tokens.Input
	s.Tokens.Output = *wire.Tokens.Output
	s.Tokens.CacheRead = *wire.Tokens.CacheRead
	s.Tokens.CacheWrite = *wire.Tokens.CacheWrite
	s.Tokens.Total = *wire.Tokens.Total
	return nil
}

// parseCost 保留 JSON 十进制成本的有理数表示，避免先转 float 再对两个累计值相减。
// Pi 在 JavaScript 中累加成本，JSON 可能包含较长的浮点展开，因此不能强制固定小数位数。
func parseCost(raw json.RawMessage) (float64, *big.Rat, error) {
	text := string(raw)
	if len(text) > 64 {
		return 0, nil, fmt.Errorf("get_session_stats response contains unsupported cost precision")
	}
	mantissa := text
	exponent := int64(0)
	if index := strings.IndexAny(text, "eE"); index >= 0 {
		mantissa = text[:index]
		parsed, err := strconv.ParseInt(text[index+1:], 10, 32)
		if err != nil {
			return 0, nil, fmt.Errorf("get_session_stats response contains invalid cost")
		}
		exponent = parsed
	}
	cost := new(big.Rat)
	if _, ok := cost.SetString(mantissa); !ok {
		return 0, nil, fmt.Errorf("get_session_stats response contains invalid cost")
	}
	if exponent != 0 {
		if exponent > 100 || exponent < -100 {
			return 0, nil, fmt.Errorf("get_session_stats response contains unsupported cost magnitude")
		}
		power := new(big.Int).Exp(big.NewInt(10), big.NewInt(absInt64(exponent)), nil)
		if exponent > 0 {
			cost.Mul(cost, new(big.Rat).SetInt(power))
		} else {
			cost.Quo(cost, new(big.Rat).SetInt(power))
		}
	}
	if cost.Sign() < 0 {
		return 0, nil, fmt.Errorf("get_session_stats response contains negative usage")
	}
	scaledMicros := new(big.Rat).Mul(cost, new(big.Rat).SetInt64(1_000_000))
	if new(big.Int).Quo(scaledMicros.Num(), scaledMicros.Denom()).BitLen() > 63 {
		return 0, nil, fmt.Errorf("get_session_stats response contains unsupported cost magnitude")
	}
	value, _ := cost.Float64()
	return value, cost, nil
}

func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}

type entriesData struct {
	LeafID *string `json:"leafId"`
}

const bridgeReadyCommand = "resonance_bridge_ready"

type slashCommand struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
}

type commandsData struct {
	Commands []slashCommand `json:"commands"`
}

type bridgeReadiness struct {
	ProtocolVersion int    `json:"bridge_protocol"`
	ProfileID       string `json:"profile_id"`
	ProfileVersion  int64  `json:"profile_version"`
	ToolCount       int    `json:"tool_count"`
}

func verifyBridgeCommands(data commandsData, profile pilotruntime.ProfileSnapshot) error {
	ready := false
	for _, command := range data.Commands {
		switch {
		case command.Name == bridgeReadyCommand:
			if ready || command.Source != "extension" || command.Description == "" {
				return fmt.Errorf("trusted Bridge readiness command is invalid")
			}
			decoder := json.NewDecoder(bytes.NewReader([]byte(command.Description)))
			decoder.DisallowUnknownFields()
			var readiness bridgeReadiness
			if err := decoder.Decode(&readiness); err != nil {
				return fmt.Errorf("trusted Bridge readiness payload is invalid")
			}
			var extra any
			if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
				return fmt.Errorf("trusted Bridge readiness payload contains trailing data")
			}
			if readiness.ProtocolVersion != 1 || readiness.ProfileID != profile.ID ||
				readiness.ProfileVersion != profile.Version || readiness.ToolCount < 1 || readiness.ToolCount > 32 {
				return fmt.Errorf("trusted Bridge readiness does not match the Run profile")
			}
			ready = true
		case command.Name == "llama" && command.Source == "extension" &&
			command.Description == "Manage llama.cpp router models":
			// Pi 0.84.1 always includes this hidden provider command even with
			// --no-extensions. It registers no Tool; the child env omits every
			// LLAMA_* variable and the Runtime network cannot reach its endpoint.
		default:
			return fmt.Errorf("pi exposed an unexpected extension command")
		}
	}
	if !ready {
		return fmt.Errorf("trusted Bridge readiness command is missing")
	}
	return nil
}

func decodeData[T any](raw json.RawMessage) (T, error) {
	var value T
	if len(raw) == 0 || string(raw) == "null" {
		return value, fmt.Errorf("pi rpc response data is missing")
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return value, fmt.Errorf("decode pi rpc response data: %w", err)
	}
	return value, nil
}
