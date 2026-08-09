package runtimehost

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeterministicProviderRequiresExactLocalEnvironment(t *testing.T) {
	t.Setenv("DASHSCOPE_BASE_URL", deterministicProviderBaseURL)
	t.Setenv("DASHSCOPE_MODEL", deterministicProviderModel)
	t.Setenv("DASHSCOPE_API_KEY", deterministicProviderAPIKey)
	provider, err := newDeterministicProviderFromEnvironment()
	require.NoError(t, err)
	require.NotNil(t, provider)

	t.Setenv("DASHSCOPE_API_KEY", "wrong-key")
	_, err = newDeterministicProviderFromEnvironment()
	require.ErrorContains(t, err, "incomplete")
}

func TestDeterministicProviderStreamsTextAndToolCalls(t *testing.T) {
	provider := &deterministicProvider{}
	for _, testCase := range []struct {
		name     string
		content  string
		expected []string
	}{
		{name: "text", content: "hello", expected: []string{"resonance-agent-e2e-ok", `"finish_reason":"stop"`, "data: [DONE]"}},
		{name: "tool", content: "[deterministic:get_my_profile]", expected: []string{`"name":"get_my_profile"`, `"arguments":"{}"`, `"finish_reason":"tool_calls"`}},
		{name: "mutation", content: "[deterministic:set_tenant_member_status username=member-1 status=DISABLED]", expected: []string{`"name":"set_tenant_member_status"`, `member-1`, `DISABLED`, `expected_version`}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			body := `{"model":"` + deterministicProviderModel + `","stream":true,"messages":[{"role":"user","content":` + quoteJSON(testCase.content) + `}]}`
			request := httptest.NewRequest(http.MethodPost, "/compatible-mode/v1/chat/completions", strings.NewReader(body))
			request.Header.Set("Authorization", "Bearer "+deterministicProviderAPIKey)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			provider.handleChatCompletion(response, request)
			require.Equal(t, http.StatusOK, response.Code)
			for _, expected := range testCase.expected {
				require.Contains(t, response.Body.String(), expected)
			}
		})
	}
}

func TestDeterministicProviderRejectsUnauthorizedRequests(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/compatible-mode/v1/chat/completions", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	(&deterministicProvider{}).handleChatCompletion(response, request)
	require.Equal(t, http.StatusUnauthorized, response.Code)
}

func quoteJSON(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
}
