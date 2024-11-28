package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"github.com/ramisoul84/kfc-loyalty/internal/app"
	"github.com/ramisoul84/kfc-loyalty/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	application, err := app.New(cfg)
	if err != nil {
		log.Fatalf("failed to create application: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := application.Start(ctx); err != nil {
		log.Printf("application error: %v", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.GRPC.ShutdownTimeout)
	defer cancel()

	if err := application.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("forced shutdown: %v", err)
	}
}
