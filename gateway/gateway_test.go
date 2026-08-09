package gateway

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReportFatalPublishesErrorAndCancelsGateway(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	gateway := &Gateway{ctx: ctx, cancel: cancel, errors: make(chan error, 1)}
	failure := errors.New("lease lost")

	gateway.reportFatal(failure)

	require.ErrorIs(t, <-gateway.Errors(), failure)
	require.ErrorIs(t, gateway.ctx.Err(), context.Canceled)
}
