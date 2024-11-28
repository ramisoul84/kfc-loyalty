package grpc

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ramisoul84/kfc-loyalty/internal/domain"
)

// mapErrorToGRPC converts domain.AppError (and plain errors) to gRPC codes.
// Non-AppError errors are treated as internal to avoid leaking internals.
func mapErrorToGRPC(err error) error {
	if err == nil {
		return nil
	}

	// Already a gRPC status? Don't re-wrap.
	if _, ok := err.(interface {
		GRPCStatus() *status.Status
	}); ok {
		return err
	}

	var appErr *domain.AppError
	if errors.As(err, &appErr) {
		switch appErr.Type {
		case domain.ErrorTypeValidation:
			return status.Error(codes.InvalidArgument, appErr.Message)
		case domain.ErrorTypeAuthentication:
			return status.Error(codes.Unauthenticated, appErr.Message)
		case domain.ErrorTypeAuthorization:
			return status.Error(codes.PermissionDenied, appErr.Message)
		case domain.ErrorTypeNotFound:
			return status.Error(codes.NotFound, appErr.Message)
		case domain.ErrorTypeConflict:
			return status.Error(codes.FailedPrecondition, appErr.Message)
		case domain.ErrorTypeInternal:
			return status.Error(codes.Internal, "internal error")
		}
	}

	return status.Error(codes.Internal, "internal error")
}
