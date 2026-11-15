package pgoutbox

import (
	"context"
	"os"
	"testing"

	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/config"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/database"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/messaging"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("DATABASE_HOST") == "" {
		t.Skip("DATABASE_HOST nao definido")
	}
	p, err := database.NewConnection(&config.DatabaseConfig{
		User: os.Getenv("DATABASE_USER"), Password: os.Getenv("DATABASE_PASSWORD"),
		Host: os.Getenv("DATABASE_HOST"), Port: os.Getenv("DATABASE_PORT"), Name: os.Getenv("DATABASE_NAME"),
	})
	require.NoError(t, err)
	require.NoError(t, database.RunMigrations(p))
	t.Cleanup(p.Close)
	return p
}

func TestOutboxRoundTrip(t *testing.T) {
	db := pool(t)
	ctx := context.Background()

	env, err := messaging.NewSagaStart(messaging.TypeQuoteSent, uuid.New(), uuid.New(), "req-1", messaging.QuoteSent{TotalCents: 100})
	require.NoError(t, err)

	tx, err := db.Begin(ctx)
	require.NoError(t, err)
	require.NoError(t, Enqueue(ctx, tx, "arn:aws:sns:sa-east-1:000:billing-events.fifo", env))
	require.NoError(t, tx.Commit(ctx))

	store := NewStore(db)
	pending, err := store.FetchPending(ctx, 1000)
	require.NoError(t, err)

	var found *messaging.OutboxMessage
	for i := range pending {
		if pending[i].ID == env.MessageID {
			found = &pending[i]
		}
	}
	require.NotNil(t, found)
	assert.Equal(t, "arn:aws:sns:sa-east-1:000:billing-events.fifo", found.Destination)
	assert.Equal(t, env.Type, found.Envelope.Type)
	assert.NotNil(t, found.Carrier)

	require.NoError(t, store.MarkPublished(ctx, env.MessageID))
	pending, err = store.FetchPending(ctx, 1000)
	require.NoError(t, err)
	for _, p := range pending {
		assert.NotEqual(t, env.MessageID, p.ID)
	}
}

func TestMarkProcessedIsIdempotent(t *testing.T) {
	db := pool(t)
	ctx := context.Background()
	env, err := messaging.NewSagaStart(messaging.TypeCreateQuote, uuid.New(), uuid.New(), "req-1", messaging.CreateQuote{})
	require.NoError(t, err)

	first, err := MarkProcessed(ctx, db, env)
	require.NoError(t, err)
	assert.True(t, first)

	again, err := MarkProcessed(ctx, db, env)
	require.NoError(t, err)
	assert.False(t, again)
}

func TestErrorsAreWrapped(t *testing.T) {
	db := pool(t)
	ctx := context.Background()
	tx, err := db.Begin(ctx)
	require.NoError(t, err)
	require.NoError(t, tx.Rollback(ctx))

	env, _ := messaging.NewSagaStart(messaging.TypeQuoteSent, uuid.New(), uuid.New(), "r", nil)
	assert.Error(t, Enqueue(ctx, tx, "q", env))
	_, err = MarkProcessed(ctx, tx, env)
	assert.Error(t, err)
	_, err = NewStore(tx).FetchPending(ctx, 1)
	assert.Error(t, err)
	assert.Error(t, NewStore(tx).MarkPublished(ctx, env.MessageID))
	assert.Error(t, Enqueue(ctx, tx, "q", messaging.Envelope{Payload: []byte("{")}))
}
