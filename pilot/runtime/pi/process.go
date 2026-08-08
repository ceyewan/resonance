package pi

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync/atomic"
	"syscall"
)

// ProcessSpec 是经过安全校验后传给子进程的完整规范。
type ProcessSpec struct {
	Path string
	Args []string
	Env  []string
	Dir  string
}

// ProcessStarter 隔离 os/exec，使协议和生命周期测试不依赖真实 Pi。
type ProcessStarter interface {
	Start(ctx context.Context, spec ProcessSpec) (Process, error)
}

// Process 是一个已经启动的子进程。Wait 必须且只能由 Adapter 的唯一 goroutine 调用一次。
type Process interface {
	Stdin() io.WriteCloser
	Stdout() io.ReadCloser
	Stderr() io.ReadCloser
	Signal(os.Signal) error
	Kill() error
	Wait() error
}

type execProcessStarter struct{}

func (execProcessStarter) Start(ctx context.Context, spec ProcessSpec) (Process, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd := exec.Command(spec.Path, spec.Args...) // #nosec G204 -- Path/Args 由 Adapter 的固定参数构造。
	cmd.Dir = spec.Dir
	cmd.Env = append([]string(nil), spec.Env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("create pi stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("create pi stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("create pi stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, fmt.Errorf("start pi process: %w", err)
	}

	return &execProcess{cmd: cmd, stdin: stdin, stdout: stdout, stderr: stderr}, nil
}

type execProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
	exited atomic.Bool
}

func (p *execProcess) Stdin() io.WriteCloser { return p.stdin }
func (p *execProcess) Stdout() io.ReadCloser { return p.stdout }
func (p *execProcess) Stderr() io.ReadCloser { return p.stderr }

func (p *execProcess) Signal(signal os.Signal) error {
	if p.exited.Load() {
		return nil
	}
	sig, ok := signal.(syscall.Signal)
	if !ok {
		return p.cmd.Process.Signal(signal)
	}
	if err := syscall.Kill(-p.cmd.Process.Pid, sig); err != nil {
		if err == syscall.ESRCH {
			return nil
		}
		return err
	}
	return nil
}

func (p *execProcess) Kill() error {
	if p.exited.Load() {
		return nil
	}
	if err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL); err != nil {
		if err == syscall.ESRCH {
			return nil
		}
		return err
	}
	return nil
}

func (p *execProcess) Wait() error {
	err := p.cmd.Wait()
	p.exited.Store(true)
	return err
}
