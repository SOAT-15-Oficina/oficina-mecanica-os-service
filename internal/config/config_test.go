package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLoad(t *testing.T) {
	cfg, err := Load()
	assert.NoError(t, err)
	assert.NotNil(t, cfg)
	assert.NotNil(t, cfg.Server)
	assert.NotNil(t, cfg.Database)
	assert.Equal(t, int32(5), cfg.Database.MaxConnections)
	assert.NotNil(t, cfg.JWT)
	assert.NotNil(t, cfg.Email)
	assert.Equal(t, 30*time.Second, cfg.Saga.CommandTimeout)
	assert.Equal(t, 5, cfg.Saga.MaxAttempts)
	assert.Equal(t, 168*time.Hour, cfg.Saga.ApprovalTimeout)
	assert.Equal(t, 48*time.Hour, cfg.Saga.PaymentTimeout)
	assert.Equal(t, "MATRIZ", cfg.Saga.DefaultBranch)
	assert.Equal(t, 500*time.Millisecond, cfg.Messaging.OutboxPollInterval)
}

func TestLoadReadsSagaOverrides(t *testing.T) {
	t.Setenv("SAGA_COMMAND_TIMEOUT", "2s")
	t.Setenv("BILLING_INBOX_QUEUE_URL", "http://localstack:4566/000000000000/billing-inbox.fifo")
	t.Setenv("INTERNAL_SERVICE_TOKEN", "secret")

	cfg, err := Load()
	assert.NoError(t, err)
	assert.Equal(t, 2*time.Second, cfg.Saga.CommandTimeout)
	assert.Equal(t, "http://localstack:4566/000000000000/billing-inbox.fifo", cfg.Messaging.BillingInboxURL)
	assert.Equal(t, "secret", cfg.Internal.ServiceToken)
}
