package ws

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type parentContextKey struct{}

func TestWithParentContextPreservesValuesWithoutRequestCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), parentContextKey{}, "trace-parent"))
	conn := &Conn{ctx: context.Background()}
	WithParentContext(parent)(conn)

	cancel()
	require.Equal(t, "trace-parent", conn.ctx.Value(parentContextKey{}))
	require.NoError(t, conn.ctx.Err())
}
