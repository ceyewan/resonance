package relay

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRelay_ForwardsOnlyTrustedBridgeSurfaceOverUnixSocket(t *testing.T) {
	var calls atomic.Int64
	socket, closeUpstream := startUnixHTTPServer(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		require.Equal(t, "Bearer capability", request.Header.Get("Authorization"))
		require.Empty(t, request.Header.Get("X-Forwarded-For"))
		require.Empty(t, request.Header.Get("X-Untrusted"))
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = io.WriteString(writer, `{"ok":true}`)
	}))
	defer closeUpstream()
	relay := startRelay(t, Config{ListenAddress: "127.0.0.1:0", BrokerSocket: socket})

	request, err := http.NewRequest(http.MethodGet, relay.Endpoint()+"/v1/manifest", nil)
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer capability")
	request.Header.Set("X-Forwarded-For", "10.0.0.1")
	request.Header.Set("X-Untrusted", "secret")
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	payload, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.JSONEq(t, `{"ok":true}`, string(payload))
	require.Equal(t, int64(1), calls.Load())

	for _, target := range []string{
		relay.Endpoint() + "/v1/manifest?tenant=other",
		relay.Endpoint() + "/v1/unknown",
	} {
		request, err = http.NewRequest(http.MethodGet, target, nil)
		require.NoError(t, err)
		request.Header.Set("Authorization", "Bearer capability")
		response, err = http.DefaultClient.Do(request)
		require.NoError(t, err)
		require.Equal(t, http.StatusNotFound, response.StatusCode)
		require.NoError(t, response.Body.Close())
	}
	require.Equal(t, int64(1), calls.Load())
}

func TestRelay_BoundsResponseAndConcurrentRequests(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	socket, closeUpstream := startUnixHTTPServer(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v1/manifest" {
			select {
			case started <- struct{}{}:
			default:
			}
			<-release
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"value":"`+strings.Repeat("x", 128)+`"}`)
	}))
	defer closeUpstream()
	relay := startRelay(t, Config{
		ListenAddress: "127.0.0.1:0", BrokerSocket: socket,
		MaxResponseBytes: 64, MaxConcurrent: 1, RequestTimeout: time.Second,
	})

	firstDone := make(chan *http.Response, 1)
	go func() { firstDone <- relayRequest(relay.Endpoint() + "/v1/manifest") }()
	<-started
	second := relayRequest(relay.Endpoint() + "/v1/manifest")
	require.Equal(t, http.StatusServiceUnavailable, second.StatusCode)
	require.NoError(t, second.Body.Close())
	close(release)
	first := <-firstDone
	require.Equal(t, http.StatusBadGateway, first.StatusCode)
	require.NoError(t, first.Body.Close())
}

func TestRelay_RejectsAmbiguousAuthorizationAndNonJSONExecution(t *testing.T) {
	socket, closeUpstream := startUnixHTTPServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("invalid requests must not reach the broker")
	}))
	defer closeUpstream()
	relay := startRelay(t, Config{ListenAddress: "127.0.0.1:0", BrokerSocket: socket})

	request, err := http.NewRequest(http.MethodGet, relay.Endpoint()+"/v1/manifest", nil)
	require.NoError(t, err)
	request.Header.Add("Authorization", "Bearer one")
	request.Header.Add("Authorization", "Bearer two")
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, response.StatusCode)
	require.NoError(t, response.Body.Close())

	request, err = http.NewRequest(http.MethodPost, relay.Endpoint()+"/v1/execute", strings.NewReader("payload"))
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer one")
	request.Header.Set("Content-Type", "text/plain")
	response, err = http.DefaultClient.Do(request)
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, response.StatusCode)
	require.NoError(t, response.Body.Close())
}

func startRelay(t *testing.T, config Config) *Relay {
	t.Helper()
	relay, err := New(config)
	require.NoError(t, err)
	require.NoError(t, relay.Start())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		require.NoError(t, relay.Close(ctx))
	})
	return relay
}

func startUnixHTTPServer(t *testing.T, handler http.Handler) (string, func()) {
	t.Helper()
	root, err := filepath.EvalSymlinks("/tmp")
	require.NoError(t, err)
	directory, err := os.MkdirTemp(root, "resonance-relay-")
	require.NoError(t, err)
	require.NoError(t, os.Chmod(directory, 0o700))
	socket := filepath.Join(directory, "broker.sock")
	listener, err := net.Listen("unix", socket)
	require.NoError(t, err)
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	return socket, func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		require.NoError(t, server.Shutdown(ctx))
		require.NoError(t, os.RemoveAll(directory))
	}
}

func relayRequest(target string) *http.Response {
	request, _ := http.NewRequest(http.MethodGet, target, nil)
	request.Header.Set("Authorization", "Bearer capability")
	response, _ := http.DefaultClient.Do(request)
	return response
}
