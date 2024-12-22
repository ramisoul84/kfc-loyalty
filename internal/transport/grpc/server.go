package grpc

import (
	"context"
	"fmt"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	loyaltyv1 "github.com/ramisoul84/kfc-loyalty/gen/loyalty/v1"
	"github.com/ramisoul84/kfc-loyalty/internal/config"
	"github.com/ramisoul84/kfc-loyalty/pkg/logger"
)

// Transport wraps the gRPC server with lifecycle methods.
type Transport struct {
	srv    *grpc.Server
	cfg    *config.Config
	logger *logger.Logger
}

// NewTransport creates a gRPC server and registers all gRPC services.
func NewTransport(cfg *config.Config, log *logger.Logger, loyaltyServer *LoyaltyServer) *Transport {
	srv := grpc.NewServer(serverOptions(log)...)

	loyaltyv1.RegisterLoyaltyServiceServer(srv, loyaltyServer)

	hs := health.NewServer()
	hs.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(srv, hs)

	return &Transport{srv: srv, cfg: cfg, logger: log}
}

// Start begins serving gRPC. Blocks until Serve returns.
func (t *Transport) Start() error {
	addr := ":" + t.cfg.GRPC.Port
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("grpc listen %s: %w", addr, err)
	}

	t.logger.Info("grpc server listening", "addr", addr)
	if err := t.srv.Serve(lis); err != nil {
		return fmt.Errorf("grpc serve: %w", err)
	}
	return nil
}

// Shutdown gracefully stops the gRPC server, respecting ctx.
func (t *Transport) Shutdown(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		t.srv.GracefulStop()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		t.srv.Stop()
		return ctx.Err()
	}
}
