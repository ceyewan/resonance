package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ceyewan/resonance/gateway"
	"github.com/ceyewan/resonance/logic"
	"github.com/ceyewan/resonance/task"
	"github.com/ceyewan/resonance/webserver"
)

func main() {
	var module string
	flag.StringVar(&module, "module", "", "assign run module: gateway, logic, task, web")
	flag.Parse()

	if module == "" {
		fmt.Println("error: module param required! Available: gateway, logic, task, web")
		os.Exit(1)
	}

	fmt.Printf("🚀 Starting Resonance %s service...\n", module)

	// 各个组件负责自己的配置加载
	switch module {
	case "gateway":
		g, err := gateway.New()
		if err != nil {
			fmt.Printf("❌ Failed to start gateway: %v\n", err)
			os.Exit(1)
		}
		defer g.Close()
		if err := g.Run(); err != nil {
			fmt.Printf("❌ Gateway error: %v\n", err)
			os.Exit(1)
		}
		waitForSignal()

	case "logic":
		l, err := logic.New()
		if err != nil {
			fmt.Printf("❌ Failed to start logic: %v\n", err)
			os.Exit(1)
		}
		defer l.Close()
		if err := l.Run(); err != nil {
			fmt.Printf("❌ Logic error: %v\n", err)
			os.Exit(1)
		}
		waitForSignal()

	case "task":
		t, err := task.New()
		if err != nil {
			fmt.Printf("❌ Failed to start task: %v\n", err)
			os.Exit(1)
		}
		defer t.Close()
		if err := t.Run(); err != nil {
			fmt.Printf("❌ Task error: %v\n", err)
			os.Exit(1)
		}
		waitForSignal()

	case "web":
		w, err := webserver.New()
		if err != nil {
			fmt.Printf("❌ Failed to start web server: %v\n", err)
			os.Exit(1)
		}
		defer w.Close()
		if err := w.Run(); err != nil {
			fmt.Printf("❌ Web server error: %v\n", err)
			os.Exit(1)
		}
		waitForSignal()

	default:
		fmt.Printf("❌ Unknown module: %s\n", module)
		fmt.Println("Available modules: gateway, logic, task, web")
		os.Exit(1)
	}
}

func waitForSignal() {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	<-quit

	fmt.Println("👋 Service exiting")
}
