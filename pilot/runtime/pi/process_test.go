package pi

import (
	"bufio"
	"context"
	"io"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExecProcessStarter_RealPipesAndReap(t *testing.T) {
	process, err := (execProcessStarter{}).Start(context.Background(), ProcessSpec{
		Path: os.Args[0],
		Args: []string{"-test.run=TestPiFakeHelperProcess", "--", "pipes"},
		Env:  []string{"GO_WANT_PI_HELPER=1"},
		Dir:  t.TempDir(),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = process.Kill()
		_ = process.Stdin().Close()
	})

	stderrDone := make(chan []byte, 1)
	go func() {
		data, _ := io.ReadAll(process.Stderr())
		stderrDone <- data
	}()
	_, err = process.Stdin().Write([]byte("hello\n"))
	require.NoError(t, err)
	require.NoError(t, process.Stdin().Close())

	line, err := bufio.NewReader(process.Stdout()).ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "ack:hello\n", line)
	// StdoutPipe/StderrPipe must be fully consumed before Wait; Wait is allowed
	// to close the descriptors as soon as the child exits.
	require.Equal(t, []byte("helper-stderr"), <-stderrDone)
	require.NoError(t, process.Wait())
	require.NoError(t, process.Signal(syscall.SIGTERM), "signal after reap must be harmless")
}

func TestExecProcessStarter_SignalTerminatesProcessGroup(t *testing.T) {
	process, err := (execProcessStarter{}).Start(context.Background(), ProcessSpec{
		Path: os.Args[0],
		Args: []string{"-test.run=TestPiFakeHelperProcess", "--", "wait-signal"},
		Env:  []string{"GO_WANT_PI_HELPER=1"},
		Dir:  t.TempDir(),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = process.Kill()
		_ = process.Stdin().Close()
	})
	require.NoError(t, process.Stdin().Close())
	ready, err := bufio.NewReader(process.Stdout()).ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "ready\n", ready)

	waitDone := make(chan error, 1)
	go func() { waitDone <- process.Wait() }()
	require.NoError(t, process.Signal(syscall.SIGTERM))
	select {
	case waitErr := <-waitDone:
		require.Error(t, waitErr)
	case <-time.After(2 * time.Second):
		_ = process.Kill()
		t.Fatal("SIGTERM did not terminate helper process group")
	}
}

func TestPiFakeHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_PI_HELPER") != "1" {
		return
	}
	args := os.Args
	separator := -1
	for index, arg := range args {
		if arg == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(args) {
		os.Exit(2)
	}

	switch args[separator+1] {
	case "pipes":
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil {
			os.Exit(3)
		}
		_, _ = io.WriteString(os.Stdout, "ack:"+line)
		_, _ = io.WriteString(os.Stderr, "helper-stderr")
		os.Exit(0)
	case "wait-signal":
		_, _ = io.WriteString(os.Stdout, "ready\n")
		select {}
	default:
		os.Exit(4)
	}
}
