package pilot

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ceyewan/genesis/clog"
	"github.com/stretchr/testify/require"

	"github.com/ceyewan/resonance/pkg/health"
)

// 后台组件的致命失败必须三件事同时发生：摘 readiness、把错误交给 main、取消根 context。
// 缺任何一件都会退化成"进程还在、/ready 还是 200、但已经收不到消息"。
func TestReportFatalDropsReadinessSignalsMainAndCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	logger := clog.Discard()
	p := &Pilot{
		logger: logger,
		health: health.NewServer("127.0.0.1:0", logger),
		ctx:    ctx, cancel: cancel,
		errors: make(chan error, 1),
	}
	p.health.SetReady(true)

	p.reportFatal(errors.New("agent ingress subscription ended unexpectedly"))

	select {
	case err := <-p.Errors():
		require.ErrorContains(t, err, "ended unexpectedly")
	case <-time.After(time.Second):
		t.Fatal("fatal error was not signalled to main")
	}
	require.ErrorIs(t, ctx.Err(), context.Canceled)

	recorder := httptest.NewRecorder()
	p.health.Probe().ReadinessHandler()(recorder, httptest.NewRequest(http.MethodGet, "/ready", nil))
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
}
