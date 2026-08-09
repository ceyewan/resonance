package logic

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReportFatalPublishesErrorAndCancelsLogic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	logic := &Logic{ctx: ctx, cancel: cancel, errors: make(chan error, 1)}
	failure := errors.New("grpc stopped")

	logic.reportFatal(failure)

	require.ErrorIs(t, <-logic.Errors(), failure)
	require.ErrorIs(t, logic.ctx.Err(), context.Canceled)
}
