package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/JR-G/rook/internal/app"
	"github.com/JR-G/rook/internal/config"
	"github.com/JR-G/rook/internal/logging"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	command := "serve"
	if len(os.Args) > 1 && os.Args[1] != "" && os.Args[1][0] != '-' {
		command = os.Args[1]
		os.Args = append([]string{os.Args[0]}, os.Args[2:]...)
	}

	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	configPath := flags.String("config", "config/rook.toml", "path to rook TOML config")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	logger, err := logging.New(cfg.Service.LogLevel)
	if err != nil {
		return err
	}

	service, err := app.New(*configPath, cfg, logger)
	if err != nil {
		return err
	}
	defer func() {
		_ = service.Close()
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	switch command {
	case "serve":
		return service.Run(ctx)
	default:
		return fmt.Errorf("unsupported command %q", command)
	}
}
