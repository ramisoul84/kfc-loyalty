// Package http exposes a minimal HTTP listener for health checks, so
// Docker and load balancers can probe the service without speaking gRPC.
package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jmoiron/sqlx"
)

type HealthServer struct {
	srv *http.Server
}

func NewHealthServer(port string, db *sqlx.DB) *HealthServer {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		status, code := "ok", http.StatusOK
		if err := db.PingContext(ctx); err != nil {
			status, code = "db_unavailable", http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": status,
			"time":   time.Now().Format(time.RFC3339),
		})
	})

	return &HealthServer{srv: &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}}
}

func (h *HealthServer) Start() error {
	if err := h.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (h *HealthServer) Shutdown(ctx context.Context) error {
	return h.srv.Shutdown(ctx)
}
