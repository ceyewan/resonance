/**
 * Created by lock
 * Date: 2019-08-09
 * Time: 10:56
 */
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// Placeholder for module implementations
type Module interface {
	Run()
}

func main() {
	var module string
	flag.StringVar(&module, "module", "", "assign run module")
	flag.Parse()
	fmt.Printf("start run %s module\n", module)

	switch module {
	case "logic":
		fmt.Println("Logic module starting...")
		// logic.New().Run()
	case "gateway":
		fmt.Println("Gateway module starting...")
		// gateway.New().Run()
	case "task":
		fmt.Println("Task module starting...")
		// task.New().Run()
	default:
		fmt.Println("exiting, module param error! Available: gateway, logic, task")
		return
	}

	fmt.Printf("run %s module done!\n", module)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case s := <-quit:
		fmt.Printf("Received signal: %v\n", s)
	case <-ctx.Done():
		fmt.Println("Context cancelled")
	}

	fmt.Println("Server exiting")
}
