package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/scuba-plaza/arabic-vocab/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(cli.Execute(ctx))
}
