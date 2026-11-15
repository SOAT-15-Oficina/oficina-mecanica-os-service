package pgoutbox

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/messaging"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func Enqueue(ctx context.Context, tx DBTX, destination string, env messaging.Envelope) error {
	body, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	carrier, err := json.Marshal(messaging.InjectTrace(ctx))
	if err != nil {
		return fmt.Errorf("marshal trace carrier: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO outbox (id, destination, message_type, group_id, body, trace_carrier)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		env.MessageID, destination, env.Type, env.GroupID(), body, carrier,
	); err != nil {
		return fmt.Errorf("insert outbox %s: %w", env.Type, err)
	}
	return nil
}

func MarkProcessed(ctx context.Context, tx DBTX, env messaging.Envelope) (bool, error) {
	tag, err := tx.Exec(ctx,
		`INSERT INTO processed_messages (message_id, message_type) VALUES ($1, $2)
		 ON CONFLICT (message_id) DO NOTHING`,
		env.MessageID, env.Type,
	)
	if err != nil {
		return false, fmt.Errorf("mark processed %s: %w", env.MessageID, err)
	}
	return tag.RowsAffected() == 1, nil
}

type Store struct {
	db DBTX
}

func NewStore(db DBTX) *Store { return &Store{db: db} }

func (s *Store) FetchPending(ctx context.Context, limit int) ([]messaging.OutboxMessage, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, destination, body, trace_carrier FROM outbox
		 WHERE published_at IS NULL ORDER BY created_at, id LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("fetch outbox: %w", err)
	}
	defer rows.Close()

	var out []messaging.OutboxMessage
	for rows.Next() {
		var (
			msg          messaging.OutboxMessage
			body, traces []byte
		)
		if err := rows.Scan(&msg.ID, &msg.Destination, &body, &traces); err != nil {
			return nil, fmt.Errorf("scan outbox: %w", err)
		}
		if err := json.Unmarshal(body, &msg.Envelope); err != nil {
			return nil, fmt.Errorf("decode outbox %s: %w", msg.ID, err)
		}
		if err := json.Unmarshal(traces, &msg.Carrier); err != nil {
			return nil, fmt.Errorf("decode outbox carrier %s: %w", msg.ID, err)
		}
		out = append(out, msg)
	}
	return out, rows.Err()
}

func (s *Store) MarkPublished(ctx context.Context, id uuid.UUID) error {
	if _, err := s.db.Exec(ctx, `UPDATE outbox SET published_at = now() WHERE id = $1`, id); err != nil {
		return fmt.Errorf("mark outbox %s published: %w", id, err)
	}
	return nil
}
