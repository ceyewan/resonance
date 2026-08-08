package pi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	pilotruntime "github.com/ceyewan/resonance/pilot/runtime"
)

func TestPiRuntime_SettledReturnsAuthoritativeFinalText(t *testing.T) {
	starter := &fakeStarter{script: normalRunScript}
	adapter := newTestAdapter(t, starter, Config{})
	req := testRunRequest(t, "run-normal")

	stream, err := adapter.Run(context.Background(), req)
	require.NoError(t, err)

	var events []pilotruntime.RuntimeEvent
	for event := range stream.Events() {
		events = append(events, event)
	}
	result, err := stream.Wait()
	require.NoError(t, err)
	require.Equal(t, "authoritative final text", result.FinalText)
	require.Equal(t, "session-1", result.SessionID)
	require.Equal(t, "leaf-9", result.LeafEntryID)
	require.Equal(t, pilotruntime.UsageStateExact, result.Usage.State)
	require.Equal(t, int64(15), result.Usage.TotalTokens)
	require.Equal(t, int64(10_000), result.Usage.CostMicros)
	repeated, err := stream.Wait()
	require.NoError(t, err)
	require.Equal(t, result, repeated, "Wait 必须可重复读取同一冻结结果")

	kinds := make([]pilotruntime.EventKind, 0, len(events))
	for _, event := range events {
		kinds = append(kinds, event.Kind)
	}
	require.Equal(t, []pilotruntime.EventKind{
		pilotruntime.EventStarted,
		pilotruntime.EventTextDelta,
		pilotruntime.EventRetryStarted,
		pilotruntime.EventRetryEnded,
		pilotruntime.EventCompactionStarted,
		pilotruntime.EventCompactionEnded,
		pilotruntime.EventSettled,
	}, kinds)
	require.Equal(t, "partial only", events[1].Text, "最终事实不能由 delta 拼接")
	for i, event := range events {
		require.Equal(t, uint64(i+1), event.Sequence)
	}

	process := starter.lastProcess()
	require.Equal(t, int32(1), process.waitCalls.Load())
	require.Empty(t, process.signalsSnapshot())
	require.Equal(t, int32(0), process.killCalls.Load())

	spec := starter.lastSpec()
	require.True(t, hasArgPair(spec.Args, "--mode", "rpc"))
	for _, required := range []string{
		"--no-builtin-tools", "--no-extensions", "--no-skills", "--no-prompt-templates",
		"--no-context-files", "--no-themes", "--no-approve",
	} {
		require.True(t, hasArg(spec.Args, required), "missing secure flag %s", required)
	}
	require.True(t, hasArgPair(spec.Args, "--extension", "/opt/resonance/bridge/index.ts"))
	require.Contains(t, spec.Env, "RESONANCE_AGENT_CAPABILITY=capability-for-test")
	require.Contains(t, spec.Env, "RESONANCE_TOOL_BROKER_URL=http://127.0.0.1:15094")
	require.Contains(t, spec.Env, "RESONANCE_AGENT_RUN_ID=run-normal")
	require.Contains(t, spec.Env, piAgentDirEnvName+"="+adapter.config.AgentDir)
	require.Contains(t, spec.Env, "RESONANCE_AGENT_MAX_TOTAL_TOKENS=20000")
	require.Contains(t, spec.Env, "RESONANCE_AGENT_MAX_COST_MICROS=100000")
	require.Contains(t, spec.Env, "RESONANCE_AGENT_MAX_PROVIDER_CALLS=8")
	require.NotContains(t, spec.Env, "SHOULD_NOT_LEAK=host-secret")
}

func TestPiRuntime_UsageIsPerAttemptDeltaAcrossRounds(t *testing.T) {
	var round atomic.Int32
	starter := &fakeStarter{script: func(process *fakeProcess) error {
		if round.Add(1) == 1 {
			return statsRunScript(process,
				fakeStatsData(process, 100, 0, 0, 0, json.Number("0.10000000000000002")),
				fakeStatsData(process, 130, 0, 0, 0, json.Number("0.10000090000000001")))
		}
		return statsRunScript(process,
			fakeStatsData(process, 130, 0, 0, 0, json.Number("0.10000090000000001")),
			fakeStatsData(process, 150, 0, 0, 0, json.Number("0.10000210000000003")))
	}}
	adapter := newTestAdapter(t, starter, Config{})

	first := runAndWait(t, adapter, testRunRequest(t, "run-delta-1"))
	require.Equal(t, pilotruntime.UsageStateExact, first.Usage.State)
	require.Equal(t, int64(30), first.Usage.TotalTokens, "累计 100→130 只能为本轮计入 30")
	require.Equal(t, int64(1), first.Usage.CostMicros)
	require.InDelta(t, 0.0000009, first.Usage.Cost, 0.0000000001)

	second := runAndWait(t, adapter, testRunRequest(t, "run-delta-2"))
	require.Equal(t, pilotruntime.UsageStateExact, second.Usage.State)
	require.Equal(t, int64(20), second.Usage.TotalTokens)
	require.Equal(t, int64(2), second.Usage.CostMicros)
}

func TestPiRuntime_UsageDeltaFailsClosed(t *testing.T) {
	tests := []struct {
		name  string
		final func(*fakeProcess) any
	}{
		{
			name: "counter decreased",
			final: func(process *fakeProcess) any {
				return fakeStatsData(process, 99, 0, 0, 0, 0.1)
			},
		},
		{
			name: "cost decreased",
			final: func(process *fakeProcess) any {
				return fakeStatsData(process, 101, 0, 0, 0, 0.09)
			},
		},
		{
			name: "session id changed",
			final: func(process *fakeProcess) any {
				stats := fakeStatsData(process, 101, 0, 0, 0, 0.1)
				stats["sessionId"] = "different-session"
				return stats
			},
		},
		{
			name: "session file changed",
			final: func(process *fakeProcess) any {
				stats := fakeStatsData(process, 101, 0, 0, 0, 0.1)
				stats["sessionFile"] = filepath.Join(filepath.Dir(fakeSessionFile(process)), "other.jsonl")
				return stats
			},
		},
		{
			name: "inconsistent total",
			final: func(process *fakeProcess) any {
				stats := fakeStatsData(process, 101, 0, 0, 0, 0.1)
				stats["tokens"].(map[string]any)["total"] = int64(1000)
				return stats
			},
		},
		{name: "missing final stats", final: func(*fakeProcess) any { return nil }},
		{
			name: "malformed final stats",
			final: func(process *fakeProcess) any {
				return map[string]any{"sessionId": "session-1", "sessionFile": fakeSessionFile(process), "tokens": map[string]any{}}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			starter := &fakeStarter{script: func(process *fakeProcess) error {
				return statsRunScript(process, fakeStatsData(process, 100, 0, 0, 0, 0.1), test.final(process))
			}}
			adapter := newTestAdapter(t, starter, Config{})
			stream, err := adapter.Run(context.Background(), testRunRequest(t, "run-invalid-delta"))
			require.NoError(t, err)
			for range stream.Events() {
			}
			result, err := stream.Wait()
			require.Error(t, err)
			require.NotNil(t, result.Usage)
			require.Equal(t, pilotruntime.UsageStateUnknown, result.Usage.State)
		})
	}
}

func TestPiRuntime_BaselineFailureIsNotStarted(t *testing.T) {
	tests := []struct {
		name string
		data func(*fakeProcess) any
	}{
		{name: "missing", data: func(*fakeProcess) any { return nil }},
		{name: "malformed", data: func(*fakeProcess) any { return map[string]any{"tokens": map[string]any{}} }},
		{
			name: "session changed",
			data: func(process *fakeProcess) any {
				stats := fakeStatsData(process, 0, 0, 0, 0, 0)
				stats["sessionId"] = "different-session"
				return stats
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			starter := &fakeStarter{script: func(process *fakeProcess) error {
				reader := bufio.NewReader(process.stdinReader)
				state, err := readFakeCommand(reader)
				if err != nil {
					return err
				}
				if err := writeFakeResponse(process, state, fakeStateData(process)); err != nil {
					return err
				}
				if err := serveFakeStats(process, reader, test.data(process)); err != nil {
					return err
				}
				_, err = reader.ReadByte()
				if !errors.Is(err, io.EOF) {
					return err
				}
				process.exit(nil)
				return nil
			}}
			adapter := newTestAdapter(t, starter, Config{})
			_, err := adapter.Run(context.Background(), testRunRequest(t, "run-baseline-failed"))
			require.Error(t, err)
			requireRunErrorUsageState(t, err, pilotruntime.UsageStateNotStarted)
		})
	}
}

func TestPiRuntime_BridgeMustBeReadyBeforePrompt(t *testing.T) {
	promptSeen := make(chan struct{}, 1)
	starter := &fakeStarter{script: func(process *fakeProcess) error {
		reader := bufio.NewReader(process.stdinReader)
		state, err := readFakeCommand(reader)
		if err != nil {
			return err
		}
		if err := writeFakeResponse(process, state, fakeStateData(process)); err != nil {
			return err
		}
		commands, err := readFakeCommand(reader)
		if err != nil {
			return err
		}
		if commands["type"] != "get_commands" {
			return fmt.Errorf("expected get_commands, got %v", commands["type"])
		}
		if err := writeFakeResponse(process, commands, map[string]any{"commands": []any{}}); err != nil {
			return err
		}
		if next, nextErr := readFakeCommand(reader); nextErr == nil {
			if next["type"] == "prompt" {
				promptSeen <- struct{}{}
			}
			return fmt.Errorf("unexpected command after missing Bridge readiness: %v", next["type"])
		} else if !errors.Is(nextErr, io.EOF) {
			return nextErr
		}
		process.exit(nil)
		return nil
	}}
	adapter := newTestAdapter(t, starter, Config{})

	_, err := adapter.Run(context.Background(), testRunRequest(t, "run-bridge-not-ready"))
	require.ErrorContains(t, err, "Bridge readiness")
	requireRunErrorUsageState(t, err, pilotruntime.UsageStateNotStarted)
	select {
	case <-promptSeen:
		t.Fatal("Prompt was sent without a ready trusted Bridge")
	default:
	}
}

func TestPiRuntime_ProcessCrashAfterPromptHasUnknownUsage(t *testing.T) {
	crash := make(chan struct{})
	starter := &fakeStarter{script: func(process *fakeProcess) error {
		reader := bufio.NewReader(process.stdinReader)
		if err := serveFakeHandshake(process, reader); err != nil {
			return err
		}
		<-crash
		process.exit(errors.New("provider process crashed"))
		return nil
	}}
	adapter := newTestAdapter(t, starter, Config{})
	stream, err := adapter.Run(context.Background(), testRunRequest(t, "run-crash-after-prompt"))
	require.NoError(t, err)
	close(crash)
	for range stream.Events() {
	}
	result, err := stream.Wait()
	require.Error(t, err)
	require.Equal(t, pilotruntime.UsageStateUnknown, result.Usage.State)
}

func TestPiRuntime_AbortGraceful(t *testing.T) {
	starter := &fakeStarter{script: gracefulAbortScript}
	adapter := newTestAdapter(t, starter, Config{})
	stream, err := adapter.Run(context.Background(), testRunRequest(t, "run-abort"))
	require.NoError(t, err)

	abortCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, adapter.Abort(abortCtx, "run-abort"))
	result, err := stream.Wait()
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, pilotruntime.UsageStateExact, result.Usage.State)
	require.Equal(t, int64(2), result.Usage.TotalTokens)

	process := starter.lastProcess()
	require.Equal(t, int32(1), process.waitCalls.Load())
	require.Empty(t, process.signalsSnapshot())
	require.Equal(t, int32(0), process.killCalls.Load())
	require.ErrorIs(t, adapter.Abort(context.Background(), "run-abort"), ErrRunNotFound)
}

func TestPiRuntime_ContextCancelEscalatesToTermAndKill(t *testing.T) {
	starter := &fakeStarter{script: ignoreAbortScript}
	adapter := newTestAdapter(t, starter, Config{
		CommandTimeout: 20 * time.Millisecond,
		AbortGrace:     20 * time.Millisecond,
		TermGrace:      20 * time.Millisecond,
		KillGrace:      20 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := adapter.Run(ctx, testRunRequest(t, "run-cancel"))
	require.NoError(t, err)
	cancel()

	result, err := stream.Wait()
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, pilotruntime.UsageStateUnknown, result.Usage.State)
	process := starter.lastProcess()
	require.Equal(t, int32(1), process.waitCalls.Load())
	require.Equal(t, []os.Signal{syscall.SIGTERM}, process.signalsSnapshot())
	require.Equal(t, int32(1), process.killCalls.Load())
}

func TestPiRuntime_StderrFloodIsTruncatedButDrained(t *testing.T) {
	starter := &fakeStarter{script: func(process *fakeProcess) error {
		if _, err := process.stderrWriter.Write(make([]byte, 1<<20)); err != nil {
			return err
		}
		return normalRunScript(process)
	}}
	adapter := newTestAdapter(t, starter, Config{MaxStderrBytes: 1024})
	stream, err := adapter.Run(context.Background(), testRunRequest(t, "run-stderr"))
	require.NoError(t, err)
	adapter.mu.Lock()
	active := adapter.active["run-stderr"]
	adapter.mu.Unlock()
	require.NotNil(t, active)
	for range stream.Events() {
	}
	_, err = stream.Wait()
	require.NoError(t, err)
	require.Equal(t, int32(1), starter.lastProcess().waitCalls.Load())
	total, truncated := active.stderr.stats()
	require.Equal(t, int64(1<<20), total)
	require.True(t, truncated)
}

func TestPiRuntime_ProbePinsExactVersion(t *testing.T) {
	t.Run("match", func(t *testing.T) {
		starter := &fakeStarter{script: func(process *fakeProcess) error {
			if _, err := process.stdoutWriter.Write([]byte("pi-test\n")); err != nil {
				return err
			}
			process.exit(nil)
			return nil
		}}
		adapter := newTestAdapter(t, starter, Config{})
		require.NoError(t, adapter.Probe(context.Background()))
		require.Equal(t, []string{"--version"}, starter.lastSpec().Args)
		require.Equal(t, int32(1), starter.lastProcess().waitCalls.Load())
	})

	t.Run("mismatch", func(t *testing.T) {
		starter := &fakeStarter{script: func(process *fakeProcess) error {
			if _, err := process.stdoutWriter.Write([]byte("pi-other\n")); err != nil {
				return err
			}
			process.exit(nil)
			return nil
		}}
		adapter := newTestAdapter(t, starter, Config{})
		err := adapter.Probe(context.Background())
		require.ErrorContains(t, err, "version mismatch")
		require.NotContains(t, err.Error(), "pi-other")
		require.Contains(t, err.Error(), "sha256=")
	})
}

func TestPiRuntime_ConcurrentProbeFailureRevokesReadiness(t *testing.T) {
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	var calls atomic.Int32
	starter := &fakeStarter{script: func(process *fakeProcess) error {
		if calls.Add(1) == 1 {
			close(firstEntered)
			<-releaseFirst
			_, _ = process.stdoutWriter.Write([]byte("pi-test\n"))
			process.exit(nil)
			return nil
		}
		_, _ = process.stdoutWriter.Write([]byte("pi-other\n"))
		process.exit(nil)
		return nil
	}}
	adapter := newTestAdapter(t, starter, Config{})
	adapter.probed.Store(false)

	firstResult := make(chan error, 1)
	go func() { firstResult <- adapter.Probe(context.Background()) }()
	<-firstEntered
	secondResult := make(chan error, 1)
	go func() { secondResult <- adapter.Probe(context.Background()) }()
	close(releaseFirst)

	require.NoError(t, <-firstResult)
	require.ErrorContains(t, <-secondResult, "version mismatch")
	require.False(t, adapter.probed.Load(), "the last failed probe must leave runtime not ready")
}

func TestPiRuntime_RunRequiresSuccessfulProbe(t *testing.T) {
	adapter, err := newAdapter(testAdapterConfig(t, Config{}), &fakeStarter{})
	require.NoError(t, err)

	_, err = adapter.Run(context.Background(), testRunRequest(t, "run-before-probe"))
	require.ErrorIs(t, err, ErrRuntimeNotReady)
	requireRunErrorUsageState(t, err, pilotruntime.UsageStateNotStarted)
}

func TestPiRuntime_StartupFailureUnblocksConcurrentAbort(t *testing.T) {
	starter := &blockingStartFailure{entered: make(chan struct{})}
	adapter := newTestAdapter(t, starter, Config{})
	req := testRunRequest(t, "run-start-failure")
	runErr := make(chan error, 1)
	go func() {
		_, err := adapter.Run(context.Background(), req)
		runErr <- err
	}()
	<-starter.entered

	abortDone := make(chan error, 1)
	go func() { abortDone <- adapter.Abort(context.Background(), "run-start-failure") }()
	select {
	case err := <-abortDone:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Abort must not hang when process startup fails")
	}
	err := <-runErr
	require.ErrorIs(t, err, context.Canceled)
	requireRunErrorUsageState(t, err, pilotruntime.UsageStateNotStarted)
}

func TestPiRuntime_HandshakeCancellationUnblocksConcurrentAbort(t *testing.T) {
	tests := []struct {
		name   string
		script func(*fakeProcess, chan<- struct{}) error
	}{
		{
			name: "get_state",
			script: func(process *fakeProcess, commandRead chan<- struct{}) error {
				reader := bufio.NewReader(process.stdinReader)
				if _, err := readFakeCommand(reader); err != nil {
					return err
				}
				close(commandRead)
				_, err := reader.ReadByte()
				if !errors.Is(err, io.EOF) {
					return err
				}
				process.exit(nil)
				return nil
			},
		},
		{
			name: "prompt_ack",
			script: func(process *fakeProcess, commandRead chan<- struct{}) error {
				reader := bufio.NewReader(process.stdinReader)
				state, err := readFakeCommand(reader)
				if err != nil {
					return err
				}
				if err := writeFakeResponse(process, state, fakeStateData(process)); err != nil {
					return err
				}
				if err := serveFakeStats(process, reader, fakeStatsData(process, 0, 0, 0, 0, 0)); err != nil {
					return err
				}
				if _, err := readFakeCommand(reader); err != nil {
					return err
				}
				close(commandRead)
				abort, err := readFakeCommand(reader)
				if err != nil {
					return err
				}
				if err := writeFakeResponse(process, abort, nil); err != nil {
					return err
				}
				if err := writeFakeJSON(process, map[string]any{"type": "agent_settled"}); err != nil {
					return err
				}
				if err := serveFakeStats(process, reader, fakeStatsData(process, 0, 0, 0, 0, 0)); err != nil {
					return err
				}
				_, err = reader.ReadByte()
				if !errors.Is(err, io.EOF) {
					return err
				}
				process.exit(nil)
				return nil
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			commandRead := make(chan struct{})
			starter := &fakeStarter{script: func(process *fakeProcess) error {
				return test.script(process, commandRead)
			}}
			adapter := newTestAdapter(t, starter, Config{CommandTimeout: 50 * time.Millisecond})
			req := testRunRequest(t, "run-handshake-"+test.name)
			runErr := make(chan error, 1)
			go func() {
				_, err := adapter.Run(context.Background(), req)
				runErr <- err
			}()
			<-commandRead

			abortDone := make(chan error, 1)
			go func() { abortDone <- adapter.Abort(context.Background(), "run-handshake-"+test.name) }()
			select {
			case err := <-abortDone:
				require.NoError(t, err)
			case <-time.After(time.Second):
				t.Fatal("Abort must not hang during handshake failure")
			}
			require.Error(t, <-runErr)
			require.Equal(t, int32(1), starter.lastProcess().waitCalls.Load())
		})
	}
}

func TestPiRuntime_PromptAckTimeoutIsUnknownAndAborted(t *testing.T) {
	promptRead := make(chan struct{})
	starter := &fakeStarter{script: func(process *fakeProcess) error {
		reader := bufio.NewReader(process.stdinReader)
		state, err := readFakeCommand(reader)
		if err != nil {
			return err
		}
		if err := writeFakeResponse(process, state, fakeStateData(process)); err != nil {
			return err
		}
		if err := serveFakeStats(process, reader, fakeStatsData(process, 0, 0, 0, 0, 0)); err != nil {
			return err
		}
		if _, err := readFakeCommand(reader); err != nil {
			return err
		}
		close(promptRead)
		abort, err := readFakeCommand(reader)
		if err != nil {
			return err
		}
		if abort["type"] != "abort" {
			return fmt.Errorf("expected abort after unknown prompt ACK, got %v", abort["type"])
		}
		if err := writeFakeResponse(process, abort, nil); err != nil {
			return err
		}
		if err := writeFakeJSON(process, map[string]any{"type": "agent_settled"}); err != nil {
			return err
		}
		if err := serveFakeStats(process, reader, fakeStatsData(process, 0, 1, 0, 0, 0)); err != nil {
			return err
		}
		_, err = reader.ReadByte()
		if !errors.Is(err, io.EOF) {
			return err
		}
		process.exit(nil)
		return nil
	}}
	adapter := newTestAdapter(t, starter, Config{CommandTimeout: 30 * time.Millisecond})

	_, err := adapter.Run(context.Background(), testRunRequest(t, "run-prompt-timeout"))
	<-promptRead
	var uncertain *CommandOutcomeUnknownError
	require.ErrorAs(t, err, &uncertain)
	require.Equal(t, "prompt", uncertain.Command)
	requireRunErrorUsageState(t, err, pilotruntime.UsageStateExact)
	process := starter.lastProcess()
	require.Empty(t, process.signalsSnapshot())
	require.Equal(t, int32(0), process.killCalls.Load())
	require.Equal(t, int32(1), process.waitCalls.Load())
}

func TestPiRuntime_PreAckEventBurstDoesNotBlockPromptResponse(t *testing.T) {
	starter := &fakeStarter{script: func(process *fakeProcess) error {
		reader := bufio.NewReader(process.stdinReader)
		state, err := readFakeCommand(reader)
		if err != nil {
			return err
		}
		if err := writeFakeResponse(process, state, fakeStateData(process)); err != nil {
			return err
		}
		if err := serveFakeStats(process, reader, fakeStatsData(process, 0, 0, 0, 0, 0)); err != nil {
			return err
		}
		prompt, err := readFakeCommand(reader)
		if err != nil {
			return err
		}
		for range 2 {
			if err := writeFakeJSON(process, map[string]any{"type": "agent_start"}); err != nil {
				return err
			}
		}
		if err := writeFakeResponse(process, prompt, nil); err != nil {
			return err
		}
		if err := writeFakeJSON(process, map[string]any{"type": "agent_settled"}); err != nil {
			return err
		}
		return serveFakeResult(process, reader)
	}}
	adapter := newTestAdapter(t, starter, Config{
		EventQueueSize:    1,
		StartupEventLimit: 4,
		CommandTimeout:    time.Second,
	})

	stream, err := adapter.Run(context.Background(), testRunRequest(t, "run-pre-ack-burst"))
	require.NoError(t, err)
	for range stream.Events() {
	}
	_, err = stream.Wait()
	require.NoError(t, err)
}

func TestPiRuntime_EventFloodCannotStarveCancellation(t *testing.T) {
	starter := &fakeStarter{script: func(process *fakeProcess) error {
		reader := bufio.NewReader(process.stdinReader)
		if err := serveFakeHandshake(process, reader); err != nil {
			return err
		}
		stopFlood := make(chan struct{})
		floodDone := make(chan struct{})
		go func() {
			defer close(floodDone)
			for {
				select {
				case <-stopFlood:
					return
				default:
					if err := writeFakeJSON(process, map[string]any{"type": "future_event"}); err != nil {
						return
					}
				}
			}
		}()
		abort, err := readFakeCommand(reader)
		close(stopFlood)
		<-floodDone
		if err != nil {
			return err
		}
		if err := writeFakeResponse(process, abort, nil); err != nil {
			return err
		}
		if err := writeFakeJSON(process, map[string]any{"type": "agent_settled"}); err != nil {
			return err
		}
		if err := serveFakeStats(process, reader, fakeStatsData(process, 0, 0, 0, 0, 0)); err != nil {
			return err
		}
		_, err = reader.ReadByte()
		if !errors.Is(err, io.EOF) {
			return err
		}
		process.exit(nil)
		return nil
	}}
	adapter := newTestAdapter(t, starter, Config{EventQueueSize: 4, EventOfferTimeout: 100 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := adapter.Run(ctx, testRunRequest(t, "run-event-flood"))
	require.NoError(t, err)
	cancel()

	done := make(chan error, 1)
	go func() {
		_, waitErr := stream.Wait()
		done <- waitErr
	}()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("event flood must not starve cancellation")
	}
}

func TestPiRuntime_SessionSymlinkEscapeIsRejected(t *testing.T) {
	adapter := newTestAdapter(t, &fakeStarter{}, Config{})

	t.Run("file symlink", func(t *testing.T) {
		req := testRunRequest(t, "run-file-symlink")
		outside := filepath.Join(t.TempDir(), "outside.jsonl")
		require.NoError(t, os.WriteFile(outside, []byte("secret"), 0o600))
		link := filepath.Join(req.Session.Directory, "session.jsonl")
		require.NoError(t, os.Symlink(outside, link))
		req.Session.FilePath = link

		_, err := adapter.Run(context.Background(), req)
		require.ErrorContains(t, err, "symbolic links")
	})

	t.Run("parent symlink", func(t *testing.T) {
		req := testRunRequest(t, "run-parent-symlink")
		outside := t.TempDir()
		link := filepath.Join(req.Session.Directory, "nested")
		require.NoError(t, os.Symlink(outside, link))
		req.Session.FilePath = filepath.Join(link, "session.jsonl")

		_, err := adapter.Run(context.Background(), req)
		require.ErrorContains(t, err, "symbolic links")
	})
}

func TestPiRuntime_ProbeTimeoutAndFloodAreBounded(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		starter := &fakeStarter{script: func(process *fakeProcess) error {
			<-process.done
			return nil
		}}
		adapter := newTestAdapter(t, starter, Config{ProbeTimeout: 30 * time.Millisecond})
		err := adapter.Probe(context.Background())
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Equal(t, int32(1), starter.lastProcess().killCalls.Load())
		require.Equal(t, int32(1), starter.lastProcess().waitCalls.Load())
	})

	t.Run("stdout flood", func(t *testing.T) {
		starter := &fakeStarter{script: func(process *fakeProcess) error {
			for {
				if _, err := process.stdoutWriter.Write([]byte("sensitive-version-output")); err != nil {
					return err
				}
			}
		}}
		adapter := newTestAdapter(t, starter, Config{MaxFrameBytes: 32, ProbeTimeout: time.Second})
		err := adapter.Probe(context.Background())
		require.ErrorIs(t, err, ErrFrameTooLarge)
		require.NotContains(t, err.Error(), "sensitive-version-output")
		require.Equal(t, int32(1), starter.lastProcess().killCalls.Load())
		require.Equal(t, int32(1), starter.lastProcess().waitCalls.Load())
	})
}

func TestMapper_ToolSecretsDoNotCrossRuntimeBoundary(t *testing.T) {
	mapped, disposition, err := mapWireEvent(WireEvent{
		Type: "tool_execution_end",
		Raw: json.RawMessage(`{
			"type":"tool_execution_end",
			"toolCallId":"call-1",
			"toolName":"get_my_profile",
			"args":{"apiKey":"top-secret-arg"},
			"result":{"ssn":"top-secret-result"},
			"isError":false
		}`),
	}, 1)
	require.NoError(t, err)
	require.Equal(t, eventMapped, disposition)
	encoded, err := json.Marshal(mapped)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "top-secret-arg")
	require.NotContains(t, string(encoded), "top-secret-result")
	require.Equal(t, "call-1", mapped.Tool.CallID)
}

func TestMapper_KnownEventsRequireProtocolFields(t *testing.T) {
	tests := []WireEvent{
		{Type: "tool_execution_start", Raw: json.RawMessage(`{"type":"tool_execution_start","toolCallId":"c","toolName":"t"}`)},
		{Type: "tool_execution_update", Raw: json.RawMessage(`{"type":"tool_execution_update","toolCallId":"c","toolName":"t","args":{}}`)},
		{Type: "tool_execution_end", Raw: json.RawMessage(`{"type":"tool_execution_end","toolCallId":"c","toolName":"t","result":{}}`)},
		{Type: "compaction_start", Raw: json.RawMessage(`{"type":"compaction_start"}`)},
		{Type: "compaction_end", Raw: json.RawMessage(`{"type":"compaction_end","reason":"threshold"}`)},
		{Type: "auto_retry_start", Raw: json.RawMessage(`{"type":"auto_retry_start","attempt":1}`)},
		{Type: "auto_retry_end", Raw: json.RawMessage(`{"type":"auto_retry_end","attempt":1}`)},
		{Type: "extension_error", Raw: json.RawMessage(`{"type":"extension_error","error":"secret"}`)},
	}

	for _, event := range tests {
		t.Run(event.Type, func(t *testing.T) {
			_, _, err := mapWireEvent(event, 1)
			require.ErrorIs(t, err, ErrMalformedJSON)
			require.NotContains(t, err.Error(), "secret")
		})
	}
}

func TestPiRuntime_ShutdownAbortsAllRunsAndIsIdempotent(t *testing.T) {
	starter := &fakeStarter{script: gracefulAbortScript}
	adapter := newTestAdapter(t, starter, Config{})
	first, err := adapter.Run(context.Background(), testRunRequest(t, "run-shutdown-1"))
	require.NoError(t, err)
	second, err := adapter.Run(context.Background(), testRunRequest(t, "run-shutdown-2"))
	require.NoError(t, err)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, adapter.Shutdown(shutdownCtx))
	_, err = first.Wait()
	require.ErrorIs(t, err, context.Canceled)
	_, err = second.Wait()
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, adapter.active)
	require.NoError(t, adapter.Shutdown(context.Background()))
	_, err = adapter.Run(context.Background(), testRunRequest(t, "run-after-shutdown"))
	require.ErrorIs(t, err, ErrRuntimeClosed)
	for _, process := range starter.allProcesses() {
		require.Equal(t, int32(1), process.waitCalls.Load())
	}
}

func TestMapper_AgentEndIsNotSettledAndExtensionErrorFailsClosed(t *testing.T) {
	_, disposition, err := mapWireEvent(WireEvent{Type: "agent_end", Raw: json.RawMessage(`{"type":"agent_end"}`)}, 1)
	require.NoError(t, err)
	require.Equal(t, eventIgnored, disposition)

	_, disposition, err = mapWireEvent(WireEvent{Type: "agent_settled", Raw: json.RawMessage(`{"type":"agent_settled"}`)}, 2)
	require.NoError(t, err)
	require.Equal(t, eventSettled, disposition)

	_, _, err = mapWireEvent(WireEvent{Type: "extension_error", Raw: json.RawMessage(
		`{"type":"extension_error","event":"tool_call","error":"bridge failed"}`,
	)}, 3)
	require.Error(t, err)
	require.Contains(t, err.Error(), "sha256=")
	require.NotContains(t, err.Error(), "bridge failed")
}

func TestPiRuntime_FailureEventIsSequencedAndRedacted(t *testing.T) {
	starter := &fakeStarter{script: func(process *fakeProcess) error {
		reader := bufio.NewReader(process.stdinReader)
		if err := serveFakeHandshake(process, reader); err != nil {
			return err
		}
		if err := writeFakeJSON(process, map[string]any{
			"type": "extension_error", "event": "tool_call", "error": "secret-provider-diagnostic",
		}); err != nil {
			return err
		}
		abort, err := readFakeCommand(reader)
		if err != nil {
			return err
		}
		if err := writeFakeResponse(process, abort, nil); err != nil {
			return err
		}
		if err := writeFakeJSON(process, map[string]any{"type": "agent_settled"}); err != nil {
			return err
		}
		if err := serveFakeStats(process, reader, fakeStatsData(process, 0, 0, 0, 0, 0)); err != nil {
			return err
		}
		_, err = reader.ReadByte()
		if !errors.Is(err, io.EOF) {
			return err
		}
		process.exit(nil)
		return nil
	}}
	adapter := newTestAdapter(t, starter, Config{})
	stream, err := adapter.Run(context.Background(), testRunRequest(t, "run-failed-event"))
	require.NoError(t, err)

	events := make([]pilotruntime.RuntimeEvent, 0, 1)
	for event := range stream.Events() {
		events = append(events, event)
	}
	_, err = stream.Wait()
	require.Error(t, err)
	require.NotContains(t, err.Error(), "secret-provider-diagnostic")
	require.Len(t, events, 1)
	require.Equal(t, pilotruntime.EventFailed, events[0].Kind)
	require.Equal(t, uint64(1), events[0].Sequence)
	require.NotNil(t, events[0].Error)
	encoded, marshalErr := json.Marshal(events[0])
	require.NoError(t, marshalErr)
	require.NotContains(t, string(encoded), "secret-provider-diagnostic")
}

func newTestAdapter(t *testing.T, starter ProcessStarter, overrides Config) *Adapter {
	t.Helper()
	cfg := testAdapterConfig(t, overrides)
	adapter, err := newAdapter(cfg, starter)
	require.NoError(t, err)
	// 大多数 Adapter 单测聚焦 Run 生命周期；Probe 门禁有独立测试覆盖。
	adapter.probed.Store(true)
	return adapter
}

func testAdapterConfig(t *testing.T, overrides Config) Config {
	t.Helper()
	agentDir := filepath.Join(t.TempDir(), "agent")
	require.NoError(t, PreparePinnedAgentDirectory(agentDir))
	cfg := Config{
		Binary:            "/usr/local/bin/pi",
		ExpectedVersion:   "pi-test",
		ExtensionPath:     "/opt/resonance/bridge/index.ts",
		WorkDir:           t.TempDir(),
		AgentDir:          agentDir,
		ToolBrokerURL:     "http://127.0.0.1:15094",
		Environment:       []string{"PATH=/usr/local/bin:/usr/bin"},
		MaxFrameBytes:     1 << 20,
		MaxOutputBytes:    8 << 20,
		MaxStderrBytes:    64 << 10,
		EventQueueSize:    32,
		EventOfferTimeout: time.Second,
		CommandTimeout:    time.Second,
		AbortGrace:        100 * time.Millisecond,
		TermGrace:         100 * time.Millisecond,
		KillGrace:         100 * time.Millisecond,
	}
	if overrides.MaxFrameBytes != 0 {
		cfg.MaxFrameBytes = overrides.MaxFrameBytes
	}
	if overrides.MaxOutputBytes != 0 {
		cfg.MaxOutputBytes = overrides.MaxOutputBytes
	}
	if overrides.EventQueueSize != 0 {
		cfg.EventQueueSize = overrides.EventQueueSize
	}
	if overrides.StartupEventLimit != 0 {
		cfg.StartupEventLimit = overrides.StartupEventLimit
	}
	if overrides.EventOfferTimeout != 0 {
		cfg.EventOfferTimeout = overrides.EventOfferTimeout
	}
	if overrides.CommandTimeout != 0 {
		cfg.CommandTimeout = overrides.CommandTimeout
	}
	if overrides.ProbeTimeout != 0 {
		cfg.ProbeTimeout = overrides.ProbeTimeout
	}
	if overrides.AbortGrace != 0 {
		cfg.AbortGrace = overrides.AbortGrace
	}
	if overrides.TermGrace != 0 {
		cfg.TermGrace = overrides.TermGrace
	}
	if overrides.KillGrace != 0 {
		cfg.KillGrace = overrides.KillGrace
	}
	if overrides.MaxStderrBytes != 0 {
		cfg.MaxStderrBytes = overrides.MaxStderrBytes
	}
	return cfg
}

func testRunRequest(t *testing.T, runID string) pilotruntime.RunRequest {
	t.Helper()
	sessionDir := t.TempDir()
	require.NoError(t, os.Chmod(sessionDir, 0o700))
	return pilotruntime.RunRequest{
		RunID:          runID,
		ConversationID: "conversation-1",
		Prompt:         "hello",
		Session:        pilotruntime.SessionSnapshot{Directory: sessionDir},
		Profile: pilotruntime.ProfileSnapshot{
			ID:           "user-assistant",
			Version:      1,
			Provider:     "test-provider",
			Model:        "test-model",
			SystemPrompt: "You are a business assistant.",
		},
		Actor:      pilotruntime.ActorPrincipal{ActorID: "actor-1", Username: "alice"},
		Capability: pilotruntime.NewSecret("capability-for-test"),
		Limits:     pilotruntime.ExecutionLimits{MaxTotalTokens: 20_000, MaxCostMicros: 100_000, MaxProviderCalls: 8},
	}
}

func runAndWait(t *testing.T, adapter *Adapter, req pilotruntime.RunRequest) pilotruntime.RunResult {
	t.Helper()
	stream, err := adapter.Run(context.Background(), req)
	require.NoError(t, err)
	for range stream.Events() {
	}
	result, err := stream.Wait()
	require.NoError(t, err)
	return result
}

func requireRunErrorUsageState(t *testing.T, err error, state pilotruntime.UsageState) {
	t.Helper()
	var runErr *pilotruntime.RunError
	require.ErrorAs(t, err, &runErr)
	require.NotNil(t, runErr.Usage)
	require.Equal(t, state, runErr.Usage.State)
}

func statsRunScript(process *fakeProcess, baseline, final any) error {
	reader := bufio.NewReader(process.stdinReader)
	state, err := readFakeCommand(reader)
	if err != nil {
		return err
	}
	if err := writeFakeResponse(process, state, fakeStateData(process)); err != nil {
		return err
	}
	if err := serveFakeStats(process, reader, baseline); err != nil {
		return err
	}
	prompt, err := readFakeCommand(reader)
	if err != nil {
		return err
	}
	if err := writeFakeResponse(process, prompt, nil); err != nil {
		return err
	}
	if err := writeFakeJSON(process, map[string]any{"type": "agent_settled"}); err != nil {
		return err
	}
	lastText, err := readFakeCommand(reader)
	if err != nil {
		return err
	}
	if err := writeFakeResponse(process, lastText, map[string]any{"text": "final"}); err != nil {
		return err
	}
	if err := serveFakeStats(process, reader, final); err != nil {
		return err
	}
	next, err := readFakeCommand(reader)
	if err != nil {
		if errors.Is(err, io.EOF) {
			process.exit(nil)
			return nil
		}
		return err
	}
	switch next["type"] {
	case "get_session_stats":
		// 终态 stats 校验失败时 Adapter 会做一次有界 best-effort 重查。
		if err := writeFakeResponse(process, next, final); err != nil {
			return err
		}
	case "get_entries":
		if err := writeFakeResponse(process, next, map[string]any{"entries": []any{}, "leafId": "leaf-usage"}); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unexpected command after final stats: %v", next["type"])
	}
	_, err = reader.ReadByte()
	if !errors.Is(err, io.EOF) {
		return err
	}
	process.exit(nil)
	return nil
}

func normalRunScript(process *fakeProcess) error {
	reader := bufio.NewReader(process.stdinReader)
	stateCommand, err := readFakeCommand(reader)
	if err != nil {
		return err
	}
	if err := writeFakeResponse(process, stateCommand, fakeStateData(process)); err != nil {
		return err
	}
	if err := serveFakeStats(process, reader, fakeStatsData(process, 0, 0, 0, 0, 0)); err != nil {
		return err
	}
	promptCommand, err := readFakeCommand(reader)
	if err != nil {
		return err
	}
	if err := writeFakeJSON(process, map[string]any{"type": "agent_start"}); err != nil {
		return err
	}
	if err := writeFakeResponse(process, promptCommand, nil); err != nil {
		return err
	}
	events := []map[string]any{
		{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "text_delta", "delta": "partial only"}},
		{"type": "agent_end", "messages": []any{}, "willRetry": true},
		{"type": "auto_retry_start", "attempt": 1, "maxAttempts": 3, "delayMs": 1},
		{"type": "auto_retry_end", "success": true, "attempt": 1},
		{"type": "compaction_start", "reason": "threshold"},
		{"type": "compaction_end", "reason": "threshold", "aborted": false, "willRetry": false},
		{"type": "agent_settled"},
	}
	for _, event := range events {
		if err := writeFakeJSON(process, event); err != nil {
			return err
		}
	}

	lastText, err := readFakeCommand(reader)
	if err != nil {
		return err
	}
	text := "authoritative final text"
	if err := writeFakeResponse(process, lastText, map[string]any{"text": text}); err != nil {
		return err
	}
	stats, err := readFakeCommand(reader)
	if err != nil {
		return err
	}
	if err := writeFakeResponse(process, stats, fakeStatsData(process, 10, 5, 0, 0, 0.01)); err != nil {
		return err
	}
	entries, err := readFakeCommand(reader)
	if err != nil {
		return err
	}
	if err := writeFakeResponse(process, entries, map[string]any{"entries": []any{}, "leafId": "leaf-9"}); err != nil {
		return err
	}
	_, err = reader.ReadByte()
	if !errors.Is(err, io.EOF) {
		return fmt.Errorf("wait for stdin close: %w", err)
	}
	process.exit(nil)
	return nil
}

func serveFakeResult(process *fakeProcess, reader *bufio.Reader) error {
	lastText, err := readFakeCommand(reader)
	if err != nil {
		return err
	}
	if err := writeFakeResponse(process, lastText, map[string]any{"text": "final"}); err != nil {
		return err
	}
	stats, err := readFakeCommand(reader)
	if err != nil {
		return err
	}
	if err := writeFakeResponse(process, stats, fakeStatsData(process, 0, 1, 0, 0, 0)); err != nil {
		return err
	}
	entries, err := readFakeCommand(reader)
	if err != nil {
		return err
	}
	if err := writeFakeResponse(process, entries, map[string]any{"entries": []any{}, "leafId": "leaf-1"}); err != nil {
		return err
	}
	_, err = reader.ReadByte()
	if !errors.Is(err, io.EOF) {
		return err
	}
	process.exit(nil)
	return nil
}

func gracefulAbortScript(process *fakeProcess) error {
	reader := bufio.NewReader(process.stdinReader)
	if err := serveFakeHandshake(process, reader); err != nil {
		return err
	}
	abort, err := readFakeCommand(reader)
	if err != nil {
		return err
	}
	if abort["type"] != "abort" {
		return fmt.Errorf("expected abort, got %v", abort["type"])
	}
	if abort["id"] == "" {
		return fmt.Errorf("abort command missing request id")
	}
	if err := writeFakeResponse(process, abort, nil); err != nil {
		return err
	}
	if err := writeFakeJSON(process, map[string]any{"type": "agent_settled"}); err != nil {
		return err
	}
	if err := serveFakeStats(process, reader, fakeStatsData(process, 1, 1, 0, 0, 0.001)); err != nil {
		return err
	}
	_, err = reader.ReadByte()
	if !errors.Is(err, io.EOF) {
		return fmt.Errorf("wait for stdin close: %w", err)
	}
	process.exit(nil)
	return nil
}

func ignoreAbortScript(process *fakeProcess) error {
	reader := bufio.NewReader(process.stdinReader)
	if err := serveFakeHandshake(process, reader); err != nil {
		return err
	}
	command, err := readFakeCommand(reader)
	if err != nil {
		return err
	}
	if command["type"] != "abort" {
		return fmt.Errorf("expected abort, got %v", command["type"])
	}
	<-process.done
	return nil
}

func serveFakeHandshake(process *fakeProcess, reader *bufio.Reader) error {
	state, err := readFakeCommand(reader)
	if err != nil {
		return err
	}
	if err := writeFakeResponse(process, state, fakeStateData(process)); err != nil {
		return err
	}
	if err := serveFakeStats(process, reader, fakeStatsData(process, 0, 0, 0, 0, 0)); err != nil {
		return err
	}
	prompt, err := readFakeCommand(reader)
	if err != nil {
		return err
	}
	return writeFakeResponse(process, prompt, nil)
}

func serveFakeStats(process *fakeProcess, reader *bufio.Reader, data any) error {
	command, err := readFakeCommand(reader)
	if err != nil {
		return err
	}
	if command["type"] == "get_commands" {
		readiness, marshalErr := json.Marshal(map[string]any{
			"bridge_protocol": 1,
			"profile_id":      "user-assistant",
			"profile_version": 1,
			"tool_count":      1,
		})
		if marshalErr != nil {
			return marshalErr
		}
		if err := writeFakeResponse(process, command, map[string]any{"commands": []any{map[string]any{
			"name": bridgeReadyCommand, "description": string(readiness), "source": "extension",
		}}}); err != nil {
			return err
		}
		command, err = readFakeCommand(reader)
		if err != nil {
			return err
		}
	}
	if command["type"] != "get_session_stats" {
		return fmt.Errorf("expected get_session_stats, got %v", command["type"])
	}
	return writeFakeResponse(process, command, data)
}

func fakeStatsData(process *fakeProcess, input, output, cacheRead, cacheWrite int64, cost any) map[string]any {
	return map[string]any{
		"sessionFile": fakeSessionFile(process),
		"sessionId":   "session-1",
		"tokens": map[string]any{
			"input": input, "output": output, "cacheRead": cacheRead, "cacheWrite": cacheWrite,
			"total": input + output + cacheRead + cacheWrite,
		},
		"cost": cost,
	}
}

func fakeStateData(process *fakeProcess) map[string]any {
	return map[string]any{
		"model":                 map[string]any{"id": "test-model", "provider": "test-provider"},
		"isStreaming":           false,
		"isCompacting":          false,
		"sessionId":             "session-1",
		"sessionFile":           fakeSessionFile(process),
		"autoCompactionEnabled": true,
		"messageCount":          0,
		"pendingMessageCount":   0,
	}
}

func readFakeCommand(reader *bufio.Reader) (map[string]any, error) {
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var command map[string]any
	if err := json.Unmarshal(line, &command); err != nil {
		return nil, err
	}
	return command, nil
}

func writeFakeResponse(process *fakeProcess, command map[string]any, data any) error {
	response := map[string]any{
		"type": "response", "id": command["id"], "command": command["type"], "success": true,
	}
	if data != nil {
		response["data"] = data
	}
	return writeFakeJSON(process, response)
}

func writeFakeJSON(process *fakeProcess, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = process.stdoutWriter.Write(data)
	return err
}

func hasArg(args []string, wanted string) bool { return slices.Contains(args, wanted) }

func hasArgPair(args []string, key, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == key && args[i+1] == value {
			return true
		}
	}
	return false
}

type fakeStarter struct {
	mu        sync.Mutex
	script    func(*fakeProcess) error
	process   *fakeProcess
	processes []*fakeProcess
	spec      ProcessSpec
}

func (s *fakeStarter) Start(ctx context.Context, spec ProcessSpec) (Process, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	process := newFakeProcess()
	process.spec = spec
	s.mu.Lock()
	s.process = process
	s.processes = append(s.processes, process)
	s.spec = spec
	s.mu.Unlock()
	go func() {
		if err := s.script(process); err != nil && !errors.Is(err, io.ErrClosedPipe) {
			process.exit(err)
		}
	}()
	return process, nil
}

func (s *fakeStarter) allProcesses() []*fakeProcess {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*fakeProcess(nil), s.processes...)
}

type blockingStartFailure struct {
	entered chan struct{}
}

func (s *blockingStartFailure) Start(ctx context.Context, _ ProcessSpec) (Process, error) {
	close(s.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}

func (s *fakeStarter) lastProcess() *fakeProcess {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.process
}

func (s *fakeStarter) lastSpec() ProcessSpec {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.spec
}

type fakeProcess struct {
	stdinReader  *io.PipeReader
	stdinWriter  *io.PipeWriter
	stdoutReader *io.PipeReader
	stdoutWriter *io.PipeWriter
	stderrReader *io.PipeReader
	stderrWriter *io.PipeWriter

	done chan struct{}
	once sync.Once
	mu   sync.Mutex
	err  error

	waitCalls atomic.Int32
	killCalls atomic.Int32
	signals   []os.Signal
	spec      ProcessSpec
}

func newFakeProcess() *fakeProcess {
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	stderrReader, stderrWriter := io.Pipe()
	return &fakeProcess{
		stdinReader: stdinReader, stdinWriter: stdinWriter,
		stdoutReader: stdoutReader, stdoutWriter: stdoutWriter,
		stderrReader: stderrReader, stderrWriter: stderrWriter,
		done: make(chan struct{}),
	}
}

func (p *fakeProcess) Stdin() io.WriteCloser { return p.stdinWriter }
func (p *fakeProcess) Stdout() io.ReadCloser { return p.stdoutReader }
func (p *fakeProcess) Stderr() io.ReadCloser { return p.stderrReader }

func (p *fakeProcess) Signal(signal os.Signal) error {
	p.mu.Lock()
	p.signals = append(p.signals, signal)
	p.mu.Unlock()
	return nil
}

func (p *fakeProcess) Kill() error {
	p.killCalls.Add(1)
	p.exit(errors.New("fake process killed"))
	return nil
}

func (p *fakeProcess) Wait() error {
	p.waitCalls.Add(1)
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

func (p *fakeProcess) exit(err error) {
	p.once.Do(func() {
		p.mu.Lock()
		p.err = err
		p.mu.Unlock()
		_ = p.stdoutWriter.Close()
		_ = p.stderrWriter.Close()
		_ = p.stdinReader.Close()
		close(p.done)
	})
}

func (p *fakeProcess) signalsSnapshot() []os.Signal {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]os.Signal(nil), p.signals...)
}

func fakeSessionFile(process *fakeProcess) string {
	for i := 0; i+1 < len(process.spec.Args); i++ {
		if process.spec.Args[i] == "--session-dir" {
			return filepath.Join(process.spec.Args[i+1], "session.jsonl")
		}
	}
	return ""
}
