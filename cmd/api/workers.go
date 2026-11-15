package main

import (
	"context"
	"log/slog"
	"sync"

	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/config"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/messaging"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/messaging/pgoutbox"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/saga"
	"github.com/jackc/pgx/v5/pgxpool"
)

func startWorkers(ctx context.Context, cfg *config.Config, db *pgxpool.Pool, orchestrator *saga.Orchestrator) (*sync.WaitGroup, error) {
	sqsClient, snsClient, err := messaging.NewClients(ctx, cfg.Messaging.Region, cfg.Messaging.Endpoint)
	if err != nil {
		return nil, err
	}
	logger := slog.Default()
	publisher := messaging.NewAWSPublisher(sqsClient, snsClient)
	relay := messaging.NewRelay(pgoutbox.NewStore(db), publisher, cfg.Messaging.OutboxPollInterval, logger)

	var wg sync.WaitGroup
	wg.Go(func() { relay.Run(ctx) })
	wg.Go(func() { orchestrator.RunDeadlines(ctx) })

	if cfg.Messaging.InboxQueueURL == "" {
		logger.Warn("MESSAGING_INBOX_QUEUE_URL not set, saga consumer disabled")
		return &wg, nil
	}
	consumer := messaging.NewConsumer(sqsClient, cfg.Messaging.InboxQueueURL, cfg.Messaging.PollWait, orchestrator.Handle, logger)
	wg.Go(func() { consumer.Run(ctx) })
	return &wg, nil
}
