package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/lesomnus/xenomai-exporter/cmd"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	c := cmd.NewCmdRoot()
	if err := c.Run(ctx, os.Args[1:]); err != nil {
		fmt.Println("app exited with error:", err)
		os.Exit(1)
	}
}
