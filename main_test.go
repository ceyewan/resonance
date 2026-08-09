package main

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWaitForSignalOrErrorReturnsBackgroundFailure(t *testing.T) {
	errorsChannel := make(chan error, 1)
	failure := errors.New("background server stopped")
	errorsChannel <- failure

	require.ErrorIs(t, waitForSignalOrError(errorsChannel), failure)
}
