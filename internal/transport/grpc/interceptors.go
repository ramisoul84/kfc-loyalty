package grpc

import (
	"context"
	"runtime/debug"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ramisoul84/kfc-loyalty/pkg/logger"
	"github.com/ramisoul84/kfc-loyalty/pkg/metrics"
)

// serverOptions is shared by every gRPC server in this service: panic
// recovery first (a bug in one handler must not take the process down),
// then RED metrics.
func serverOptions(log *logger.Logger) []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(recoverInterceptor(log), metrics.UnaryServerInterceptor()),
	}
}

func recoverInterceptor(log *logger.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if p := recover(); p != nil {
				log.Error("grpc handler panic",
					"method", info.FullMethod,
					"panic", p,
					"stack", string(debug.Stack()),
				)
				err = status.Error(codes.Internal, "internal error")
			}
		}()
		return handler(ctx, req)
	}
}
