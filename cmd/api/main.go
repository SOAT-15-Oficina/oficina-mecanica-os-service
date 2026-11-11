package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/config"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/database"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/observability"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/routes"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/packages/email"
	"github.com/gofiber/fiber/v3"
)

// O binario tem dois modos:
//
//	techchallenge            sobe a API
//	techchallenge migrate    aplica as migrations e sai
//
// As migrations deixaram de rodar no boot porque o Deployment sobe com
// replicas >= 2 e HPA ate 10: N processos disputando o mesmo DDL e corrida.
// O pipeline do repo roda um Job com `migrate` antes do rollout.
func main() {
	observability.Setup()

	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		runMigrations()
		return
	}
	runServer()
}

func runMigrations() {
	cfg := mustLoadConfig()

	db, err := database.NewConnection(cfg.Database)
	if err != nil {
		shutdownApp(err, "failed to connect to database", observability.Integration(observability.IntegrationRDS))
	}
	defer db.Close()

	if err := database.RunMigrations(db); err != nil {
		shutdownApp(err, "failed to run migrations", observability.Integration(observability.IntegrationRDS))
	}
	slog.Info("migrations applied successfully")
}

func runServer() {
	cfg := mustLoadConfig()

	stopTracer := observability.StartTracer(slog.Default())
	defer stopTracer()

	db, err := database.NewConnection(cfg.Database)
	if err != nil {
		shutdownApp(err, "failed to connect to database", observability.Integration(observability.IntegrationRDS))
	}
	defer db.Close()

	emailProv, err := newEmailProvider(cfg)
	if err != nil {
		shutdownApp(err, "failed to create email provider", observability.Integration(observability.IntegrationSES))
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	orchestrator := routes.NewOrchestrator(db, cfg, emailProv)
	workers, err := startWorkers(ctx, cfg, db, orchestrator)
	if err != nil {
		shutdownApp(err, "failed to start saga workers")
	}

	slog.Info("dependencies initialized successfully")

	app := fiber.New(fiber.Config{})
	routes.RegisterRoutes(app, db, cfg, orchestrator)

	listenErr := make(chan error, 1)
	go func() { listenErr <- app.Listen(":" + cfg.Server.Port) }()

	select {
	case err := <-listenErr:
		stop()
		workers.Wait()
		shutdownApp(err, "failed to start server")
	case <-ctx.Done():
	}

	slog.Info("shutting down")
	if err := app.ShutdownWithTimeout(20 * time.Second); err != nil {
		slog.Error("http shutdown failed", observability.Err(err))
	}
	workers.Wait()
}

func mustLoadConfig() *config.Config {
	cfg, err := config.Load()
	if err != nil {
		shutdownApp(err, "failed to load configuration")
	}
	return cfg
}

func newEmailProvider(cfg *config.Config) (email.Provider, error) {
	return email.New(cfg.Email.Provider, email.Config{
		Host:           cfg.Email.Host,
		Port:           cfg.Email.Port,
		From:           cfg.Email.From,
		Region:         cfg.AWS.DefaultRegion,
		SESSenderEmail: cfg.AWS.SESSenderEmail,
		SESReplyTo:     cfg.AWS.SESReplyTo,
		SESConfigSet:   cfg.AWS.SESConfigSet,
	})
}

func shutdownApp(err error, message string, attrs ...slog.Attr) {
	if err == nil {
		return
	}

	slog.LogAttrs(context.Background(), slog.LevelError, message, append(attrs, observability.Err(err))...)
	os.Exit(1)
}
