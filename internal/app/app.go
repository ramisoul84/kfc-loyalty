package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/ramisoul84/kfc-loyalty/internal/config"
	"github.com/ramisoul84/kfc-loyalty/internal/consumer"
	"github.com/ramisoul84/kfc-loyalty/internal/repository"
	"github.com/ramisoul84/kfc-loyalty/internal/service"
	grpcTransport "github.com/ramisoul84/kfc-loyalty/internal/transport/grpc"
	httpTransport "github.com/ramisoul84/kfc-loyalty/internal/transport/http"
	"github.com/ramisoul84/kfc-loyalty/migrations"
	"github.com/ramisoul84/kfc-loyalty/pkg/database"
	"github.com/ramisoul84/kfc-loyalty/pkg/kafka"
	"github.com/ramisoul84/kfc-loyalty/pkg/logger"
	"github.com/ramisoul84/kfc-loyalty/pkg/migrate"
)

// App wires together all application components.
type App struct {
	config        *config.Config
	logger        *logger.Logger
	db            *database.Postgres
	grpcServer    *grpcTransport.Transport
	healthServer  *httpTransport.HealthServer
	orderConsumer *kafka.Consumer
}

func New(cfg *config.Config) (*App, error) {
	log := logger.New(&logger.Config{
		Level:    cfg.Logger.Level,
		Format:   cfg.Logger.Format,
		Output:   cfg.Logger.Output,
		FilePath: cfg.Logger.FilePath,
		Service:  cfg.Logger.Service,
	})

	log.Info("starting application",
		"name", cfg.App.Name,
		"version", cfg.App.Version,
		"environment", cfg.App.Environment,
		"earn_percent", cfg.Program.EarnPercent,
		"max_redeem_percent", cfg.Program.MaxRedeemPercent,
	)

	db, err := database.NewPostgres(&cfg.DB, cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}
	log.Info("postgres connected", "database", cfg.DB.Name)

	if cfg.DB.AutoMigrate {
		applied, err := migrate.Up(context.Background(), db.DB, migrations.FS)
		if err != nil {
			db.Close()
			return nil, fmt.Errorf("migrate: %w", err)
		}
		log.Info("migrations up to date", "applied", applied)
	}

	repo := repository.New(db.DB)
	svc := service.New(repo, cfg.Program, log)

	// Order events drive earning, refunds and hold reconciliation. One
	// shared consumer group: each event must be processed once, not once
	// per replica (handlers are idempotent anyway).
	var orderConsumer *kafka.Consumer
	if len(cfg.Kafka.Brokers) > 0 {
		orderConsumer = kafka.NewConsumer(
			cfg.Kafka.Brokers,
			cfg.Kafka.OrdersTopic,
			cfg.Kafka.GroupID,
			consumer.NewOrderConsumer(svc, log).Handle,
			log,
			kafka.RetryHandler(5),
		)
		log.Info("order event consumer initialized", "topic", cfg.Kafka.OrdersTopic, "group", cfg.Kafka.GroupID)
	} else {
		log.Warn("kafka disabled; points will not be earned or refunded automatically")
	}

	return &App{
		config:        cfg,
		logger:        log,
		db:            db,
		grpcServer:    grpcTransport.NewTransport(cfg, log, grpcTransport.NewLoyaltyServer(svc)),
		healthServer:  httpTransport.NewHealthServer(cfg.HTTP.Port, db.DB),
		orderConsumer: orderConsumer,
	}, nil
}

func (a *App) Start(ctx context.Context) error {
	errCh := make(chan error, 2)

	go func() {
		if err := a.grpcServer.Start(); err != nil {
			errCh <- fmt.Errorf("grpc: %w", err)
		}
	}()
	go func() {
		a.logger.Info("health server listening", "addr", ":"+a.config.HTTP.Port)
		if err := a.healthServer.Start(); err != nil {
			errCh <- fmt.Errorf("http: %w", err)
		}
	}()
	if a.orderConsumer != nil {
		a.orderConsumer.Start(ctx)
	}

	select {
	case <-ctx.Done():
		a.logger.Info("shutdown signal received")
		return nil
	case err := <-errCh:
		a.logger.Error("transport failed", "error", err)
		return err
	}
}

func (a *App) Shutdown(ctx context.Context) error {
	a.logger.Info("shutting down application")

	var errs []error
	if a.orderConsumer != nil {
		if err := a.orderConsumer.Stop(); err != nil {
			errs = append(errs, fmt.Errorf("order consumer: %w", err))
		}
	}
	if err := a.grpcServer.Shutdown(ctx); err != nil {
		errs = append(errs, fmt.Errorf("grpc shutdown: %w", err))
	}
	if err := a.healthServer.Shutdown(ctx); err != nil {
		errs = append(errs, fmt.Errorf("http shutdown: %w", err))
	}
	if a.db != nil {
		_ = a.db.Close()
	}

	a.logger.Info("shutdown complete")
	return errors.Join(errs...)
}
