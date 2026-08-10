package runtimehost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	deterministicProviderAddress = "127.0.0.1:18096"
	deterministicProviderBaseURL = "http://127.0.0.1:18096/compatible-mode/v1"
	deterministicProviderModel   = "resonance-deterministic-v1"
	deterministicProviderAPIKey  = "resonance-local-deterministic-key"
	maxDeterministicRequestBytes = 4 << 20
)

var deterministicMutationPrompt = regexp.MustCompile(`\[deterministic:set_tenant_member_status username=([a-zA-Z0-9_.-]{1,64}) status=(ACTIVE|DISABLED)\]`)

type deterministicProvider struct {
	server *http.Server
	errors chan error
}

type deterministicChatRequest struct {
	Model    string                     `json:"model"`
	Stream   bool                       `json:"stream"`
	Messages []deterministicChatMessage `json:"messages"`
}

type deterministicChatMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

func newDeterministicProviderFromEnvironment() (*deterministicProvider, error) {
	baseURL := os.Getenv("DASHSCOPE_BASE_URL")
	model := os.Getenv("DASHSCOPE_MODEL")
	apiKey := os.Getenv("DASHSCOPE_API_KEY")
	enabled := baseURL == deterministicProviderBaseURL || model == deterministicProviderModel || apiKey == deterministicProviderAPIKey
	if !enabled {
		return nil, nil
	}
	if baseURL != deterministicProviderBaseURL || model != deterministicProviderModel || apiKey != deterministicProviderAPIKey {
		return nil, fmt.Errorf("local deterministic Provider environment is incomplete")
	}
	provider := &deterministicProvider{errors: make(chan error, 1)}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /compatible-mode/v1/chat/completions", provider.handleChatCompletion)
	provider.server = &http.Server{
		Addr:              deterministicProviderAddress,
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    8 << 10,
	}
	return provider, nil
}

func (p *deterministicProvider) Start() error {
	if p == nil {
		return nil
	}
	listener, err := net.Listen("tcp", deterministicProviderAddress)
	if err != nil {
		return fmt.Errorf("listen deterministic Provider: %w", err)
	}
	go func() {
		if serveErr := p.server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			select {
			case p.errors <- fmt.Errorf("serve deterministic Provider: %w", serveErr):
			default:
			}
		}
	}()
	return nil
}

func (p *deterministicProvider) Close(ctx context.Context) error {
	if p == nil {
		return nil
	}
	return p.server.Shutdown(ctx)
}

func (p *deterministicProvider) Errors() <-chan error {
	if p == nil {
		return nil
	}
	return p.errors
}

func (p *deterministicProvider) handleChatCompletion(writer http.ResponseWriter, request *http.Request) {
	if request.Header.Get("Authorization") != "Bearer "+deterministicProviderAPIKey ||
		request.Header.Get("Content-Type") != "application/json" {
		http.Error(writer, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, maxDeterministicRequestBytes))
	if err != nil {
		http.Error(writer, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	var chatRequest deterministicChatRequest
	if err := json.Unmarshal(body, &chatRequest); err != nil || chatRequest.Model != deterministicProviderModel ||
		!chatRequest.Stream || len(chatRequest.Messages) == 0 {
		http.Error(writer, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	last := chatRequest.Messages[len(chatRequest.Messages)-1]
	text := deterministicMessageText(last.Content)
	if deterministicUserHistoryContains(chatRequest.Messages, "[deterministic:runtime_failure]") {
		http.Error(writer, "deterministic runtime failure", http.StatusServiceUnavailable)
		return
	}
	if deterministicUserHistoryContains(chatRequest.Messages, "[deterministic:timeout]") {
		// The local Runtime request timeout is five seconds. Keeping this path
		// deterministic and just beyond that bound gives the Compose E2E a real
		// timeout without contacting a cloud Provider.
		timer := time.NewTimer(6 * time.Second)
		defer timer.Stop()
		select {
		case <-request.Context().Done():
			return
		case <-timer.C:
			http.Error(writer, "deterministic timeout exceeded", http.StatusGatewayTimeout)
			return
		}
	}

	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	completionID := "chatcmpl-resonance-local"
	if last.Role == "user" {
		if strings.Contains(text, "[deterministic:get_my_profile]") {
			p.writeToolCall(writer, completionID, deterministicCallID(body), "get_my_profile", `{}`)
			return
		}
		if matches := deterministicMutationPrompt.FindStringSubmatch(text); len(matches) == 3 {
			arguments, _ := json.Marshal(map[string]any{
				"target_username": matches[1], "desired_status": matches[2],
				"expected_version": 1, "dry_run": false,
			})
			p.writeToolCall(writer, completionID, deterministicCallID(body), "set_tenant_member_status", string(arguments))
			return
		}
	}
	reply := "resonance-agent-e2e-ok"
	if last.Role == "tool" {
		reply = "resonance-deterministic-tool-complete"
	}
	p.writeText(writer, completionID, reply)
}

func deterministicUserHistoryContains(messages []deterministicChatMessage, marker string) bool {
	for _, message := range messages {
		if message.Role == "user" && strings.Contains(deterministicMessageText(message.Content), marker) {
			return true
		}
	}
	return false
}

func deterministicMessageText(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []any:
		var text strings.Builder
		for _, item := range value {
			object, ok := item.(map[string]any)
			if !ok || object["type"] != "text" {
				continue
			}
			part, _ := object["text"].(string)
			text.WriteString(part)
		}
		return text.String()
	default:
		return ""
	}
}

func deterministicCallID(body []byte) string {
	digest := sha256.Sum256(body)
	return "deterministic-" + hex.EncodeToString(digest[:8])
}

func (p *deterministicProvider) writeText(writer io.Writer, completionID, text string) {
	p.writeChunk(writer, completionID, map[string]any{"role": "assistant", "content": text}, nil, false)
	finish := "stop"
	p.writeChunk(writer, completionID, map[string]any{}, &finish, true)
	_, _ = io.WriteString(writer, "data: [DONE]\n\n")
}

func (p *deterministicProvider) writeToolCall(writer io.Writer, completionID, callID, name, arguments string) {
	delta := map[string]any{
		"role": "assistant",
		"tool_calls": []any{map[string]any{
			"index": 0, "id": callID, "type": "function",
			"function": map[string]any{"name": name, "arguments": arguments},
		}},
	}
	p.writeChunk(writer, completionID, delta, nil, false)
	finish := "tool_calls"
	p.writeChunk(writer, completionID, map[string]any{}, &finish, true)
	_, _ = io.WriteString(writer, "data: [DONE]\n\n")
}

func (p *deterministicProvider) writeChunk(writer io.Writer, completionID string, delta map[string]any, finishReason *string, withUsage bool) {
	chunk := map[string]any{
		"id": completionID, "object": "chat.completion.chunk", "created": time.Now().Unix(),
		"model":   deterministicProviderModel,
		"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finishReason}},
	}
	if withUsage {
		chunk["usage"] = map[string]any{
			"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15,
			"prompt_tokens_details": map[string]any{"cached_tokens": 0},
		}
	}
	encoded, err := json.Marshal(chunk)
	if err == nil {
		_, _ = fmt.Fprintf(writer, "data: %s\n\n", encoded)
	}
}
