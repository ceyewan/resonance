package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ceyewan/resonance/bootstrap"
	"github.com/ceyewan/resonance/gateway"
	"github.com/ceyewan/resonance/logic"
	"github.com/ceyewan/resonance/pilot"
	"github.com/ceyewan/resonance/pilot/egressproxy"
	"github.com/ceyewan/resonance/pilot/runtimehost"
	"github.com/ceyewan/resonance/task"
	"github.com/ceyewan/resonance/webserver"
)

func main() {
	os.Exit(run())
}

func run() (exitCode int) {
	var module string
	flag.StringVar(&module, "module", "", "assign run module: gateway, logic, task, pilot, pilot-runtime, egress-proxy, web")
	flag.Parse()

	if module == "" {
		fmt.Println("error: module param required! Available: init, gateway, logic, task, pilot, pilot-runtime, egress-proxy, web")
		return 1
	}

	fmt.Printf("🚀 Starting Resonance %s service...\n", module)

	// 各个组件负责自己的配置加载
	switch module {
	case "init":
		if err := bootstrap.Run(); err != nil {
			fmt.Printf("❌ Init failed: %v\n", err)
			return 1
		}
		fmt.Println("✅ Database initialization completed")
		return 0

	case "gateway":
		g, err := gateway.New()
		if err != nil {
			fmt.Printf("❌ Failed to start gateway: %v\n", err)
			return 1
		}
		defer func() {
			if err := g.Close(); err != nil {
				fmt.Printf("❌ Gateway shutdown error: %v\n", err)
				exitCode = 1
			}
		}()
		if err := g.Run(); err != nil {
			fmt.Printf("❌ Gateway error: %v\n", err)
			return 1
		}
		waitForSignal()

	case "logic":
		l, err := logic.New()
		if err != nil {
			fmt.Printf("❌ Failed to start logic: %v\n", err)
			return 1
		}
		defer func() {
			if err := l.Close(); err != nil {
				fmt.Printf("❌ Logic shutdown error: %v\n", err)
				exitCode = 1
			}
		}()
		if err := l.Run(); err != nil {
			fmt.Printf("❌ Logic error: %v\n", err)
			return 1
		}
		waitForSignal()

	case "task":
		t, err := task.New()
		if err != nil {
			fmt.Printf("❌ Failed to start task: %v\n", err)
			return 1
		}
		defer func() {
			if err := t.Close(); err != nil {
				fmt.Printf("❌ Task shutdown error: %v\n", err)
				exitCode = 1
			}
		}()
		if err := t.Run(); err != nil {
			fmt.Printf("❌ Task error: %v\n", err)
			return 1
		}
		waitForSignal()

	case "pilot":
		p, err := pilot.New()
		if err != nil {
			fmt.Printf("❌ Failed to start pilot: %v\n", err)
			return 1
		}
		defer func() {
			if err := p.Close(); err != nil {
				fmt.Printf("❌ Pilot shutdown error: %v\n", err)
				exitCode = 1
			}
		}()
		if err := p.Run(); err != nil {
			fmt.Printf("❌ Pilot error: %v\n", err)
			return 1
		}
		if err := waitForSignalOrError(p.Errors()); err != nil {
			fmt.Printf("❌ Pilot background failure: %v\n", err)
			return 1
		}

	case "pilot-runtime":
		host, err := runtimehost.New()
		if err != nil {
			fmt.Printf("❌ Failed to create isolated Pilot runtime: %v\n", err)
			return 1
		}
		defer func() {
			if err := host.Close(); err != nil {
				fmt.Printf("❌ Pilot runtime shutdown error: %v\n", err)
				exitCode = 1
			}
		}()
		if err := host.Run(); err != nil {
			fmt.Printf("❌ Pilot runtime error: %v\n", err)
			return 1
		}
		if err := waitForSignalOrErrorOrDone(host.Errors(), host.Done()); err != nil {
			fmt.Printf("❌ Pilot runtime background failure: %v\n", err)
			return 1
		}
	case "egress-proxy":
		cfg, err := egressproxy.Load()
		if err != nil {
			fmt.Printf("❌ Failed to load egress proxy config: %v\n", err)
			return 1
		}
		proxy, err := egressproxy.New(*cfg)
		if err != nil {
			fmt.Printf("❌ Failed to create egress proxy: %v\n", err)
			return 1
		}
		defer func() {
			if err := proxy.Close(); err != nil {
				fmt.Printf("❌ Egress proxy shutdown error: %v\n", err)
				exitCode = 1
			}
		}()
		if err := proxy.Run(); err != nil {
			fmt.Printf("❌ Egress proxy error: %v\n", err)
			return 1
		}
		if err := waitForSignalOrError(proxy.Errors()); err != nil {
			fmt.Printf("❌ Egress proxy background failure: %v\n", err)
			return 1
		}

	case "web":
		w, err := webserver.New()
		if err != nil {
			fmt.Printf("❌ Failed to start web server: %v\n", err)
			return 1
		}
		defer func() {
			if err := w.Close(); err != nil {
				fmt.Printf("❌ Web shutdown error: %v\n", err)
				exitCode = 1
			}
		}()
		if err := w.Run(); err != nil {
			fmt.Printf("❌ Web server error: %v\n", err)
			return 1
		}
		waitForSignal()

	default:
		fmt.Printf("❌ Unknown module: %s\n", module)
		fmt.Println("Available modules: init, gateway, logic, task, pilot, pilot-runtime, egress-proxy, web")
		return 1
	}
	return 0
}

func waitForSignal() {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	<-quit

	fmt.Println("👋 Service exiting")
}

func waitForSignalOrError(errors <-chan error) error {
	return waitForSignalOrErrorOrDone(errors, nil)
}

func waitForSignalOrErrorOrDone(errors <-chan error, done <-chan struct{}) error {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	defer signal.Stop(quit)
	select {
	case <-quit:
		fmt.Println("👋 Service exiting")
		return nil
	case err := <-errors:
		return err
	case <-done:
		return nil
	}
}
