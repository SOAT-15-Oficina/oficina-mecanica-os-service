package saga

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/application"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/domain"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrStaleSaga = errors.New("saga instance was changed concurrently")

const sagaColumns = `id, work_order_id, status, current_step, completed_steps, pending_compensations,
	deadline_at, attempts, last_command, quote_id, failure_reason, version, created_at, updated_at`

func scanSaga(row pgx.Row) (*domain.SagaInstance, error) {
	var (
		inst                        domain.SagaInstance
		completed, pending, command []byte
	)
	err := row.Scan(&inst.ID, &inst.WorkOrderID, &inst.Status, &inst.CurrentStep, &completed, &pending,
		&inst.DeadlineAt, &inst.Attempts, &command, &inst.QuoteID, &inst.FailureReason, &inst.Version,
		&inst.CreatedAt, &inst.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, application.ErrNotFound
		}
		return nil, err
	}
	if err := json.Unmarshal(completed, &inst.CompletedSteps); err != nil {
		return nil, fmt.Errorf("decode completed_steps: %w", err)
	}
	if err := json.Unmarshal(pending, &inst.PendingCompensations); err != nil {
		return nil, fmt.Errorf("decode pending_compensations: %w", err)
	}
	if len(command) > 0 {
		inst.LastCommand = &domain.SagaCommand{}
		if err := json.Unmarshal(command, inst.LastCommand); err != nil {
			return nil, fmt.Errorf("decode last_command: %w", err)
		}
	}
	return &inst, nil
}

func sagaArgs(inst *domain.SagaInstance) ([]byte, []byte, []byte, error) {
	if inst.CompletedSteps == nil {
		inst.CompletedSteps = []string{}
	}
	if inst.PendingCompensations == nil {
		inst.PendingCompensations = []string{}
	}
	completed, err := json.Marshal(inst.CompletedSteps)
	if err != nil {
		return nil, nil, nil, err
	}
	pending, err := json.Marshal(inst.PendingCompensations)
	if err != nil {
		return nil, nil, nil, err
	}
	var command []byte
	if inst.LastCommand != nil {
		if command, err = json.Marshal(inst.LastCommand); err != nil {
			return nil, nil, nil, err
		}
	}
	return completed, pending, command, nil
}

func insertSaga(ctx context.Context, tx pgx.Tx, inst *domain.SagaInstance, now time.Time) error {
	completed, pending, command, err := sagaArgs(inst)
	if err != nil {
		return err
	}
	inst.Version = 1
	inst.CreatedAt, inst.UpdatedAt = now, now
	_, err = tx.Exec(ctx, `
		INSERT INTO saga_instances (`+sagaColumns+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $13)`,
		inst.ID, inst.WorkOrderID, inst.Status, inst.CurrentStep, completed, pending,
		inst.DeadlineAt, inst.Attempts, command, inst.QuoteID, inst.FailureReason, inst.Version, now)
	if err != nil {
		return fmt.Errorf("insert saga: %w", err)
	}
	return nil
}

func saveSaga(ctx context.Context, tx pgx.Tx, inst *domain.SagaInstance, now time.Time) error {
	completed, pending, command, err := sagaArgs(inst)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE saga_instances
		SET status = $3, current_step = $4, completed_steps = $5, pending_compensations = $6,
			deadline_at = $7, attempts = $8, last_command = $9, quote_id = $10, failure_reason = $11,
			version = version + 1, updated_at = $12
		WHERE id = $1 AND version = $2`,
		inst.ID, inst.Version, inst.Status, inst.CurrentStep, completed, pending,
		inst.DeadlineAt, inst.Attempts, command, inst.QuoteID, inst.FailureReason, now)
	if err != nil {
		return fmt.Errorf("update saga: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrStaleSaga
	}
	inst.Version++
	inst.UpdatedAt = now
	return nil
}

func loadSaga(ctx context.Context, db repository.DBTX, workOrderID uuid.UUID) (*domain.SagaInstance, error) {
	return scanSaga(db.QueryRow(ctx, `SELECT `+sagaColumns+` FROM saga_instances WHERE work_order_id = $1`, workOrderID))
}

func dueSagas(ctx context.Context, tx pgx.Tx, now time.Time, limit int) ([]*domain.SagaInstance, error) {
	rows, err := tx.Query(ctx, `
		SELECT `+sagaColumns+` FROM saga_instances
		WHERE status IN ('STARTED', 'COMPENSATING') AND deadline_at IS NOT NULL AND deadline_at <= $1
		ORDER BY deadline_at
		LIMIT $2
		FOR UPDATE SKIP LOCKED`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("select due sagas: %w", err)
	}
	defer rows.Close()

	var out []*domain.SagaInstance
	for rows.Next() {
		inst, err := scanSaga(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inst)
	}
	return out, rows.Err()
}
