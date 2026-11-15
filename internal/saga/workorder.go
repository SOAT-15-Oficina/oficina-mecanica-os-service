package saga

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/application"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/domain"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/messaging"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type transition struct {
	workOrder domain.WorkOrder
	from      domain.WorkOrderStatus
	inStatus  time.Duration
}

func insertHistory(ctx context.Context, tx pgx.Tx, workOrderID uuid.UUID, from *domain.WorkOrderStatus, to domain.WorkOrderStatus, cause string, messageID *uuid.UUID, now time.Time) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO work_order_status_history (id, work_order_id, from_status, to_status, cause, message_id, changed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		uuid.New(), workOrderID, from, to, cause, messageID, now)
	if err != nil {
		return fmt.Errorf("insert status history: %w", err)
	}
	return nil
}

func recordTransition(ctx context.Context, tx pgx.Tx, workOrderID uuid.UUID, to domain.WorkOrderStatus, cause string, messageID *uuid.UUID, now time.Time) (*transition, error) {
	var t transition
	err := tx.QueryRow(ctx, `SELECT id, code, customer_id, status FROM work_orders WHERE id = $1 FOR UPDATE`, workOrderID).
		Scan(&t.workOrder.ID, &t.workOrder.Code, &t.workOrder.CustomerID, &t.from)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, application.ErrNotFound
		}
		return nil, fmt.Errorf("lock work order: %w", err)
	}
	if t.from == to {
		return nil, nil
	}
	if !domain.CanTransition(t.from, to) {
		return nil, fmt.Errorf("%w: %s -> %s", domain.ErrInvalidStatusTransition, t.from, to)
	}

	var since *time.Time
	if err := tx.QueryRow(ctx, `SELECT max(changed_at) FROM work_order_status_history WHERE work_order_id = $1`, workOrderID).Scan(&since); err != nil {
		return nil, fmt.Errorf("read last transition: %w", err)
	}
	if since != nil && now.After(*since) {
		t.inStatus = now.Sub(*since)
	}

	_, err = tx.Exec(ctx, `
		UPDATE work_orders SET
			status = $2::varchar,
			quote_sent_at = CASE WHEN $2::varchar = 'AGUARDANDO_APROVACAO' THEN $3 ELSE quote_sent_at END,
			approved_at = CASE WHEN $2::varchar = 'APROVADO' THEN $3 ELSE approved_at END,
			started_at = CASE WHEN $2::varchar = 'EM_EXECUCAO' THEN $3 ELSE started_at END,
			finished_at = CASE WHEN $2::varchar = 'FINALIZADA' THEN $3 ELSE finished_at END,
			delivered_at = CASE WHEN $2::varchar = 'ENTREGUE' THEN $3 ELSE delivered_at END,
			updated_at = $3
		WHERE id = $1`, workOrderID, string(to), now)
	if err != nil {
		return nil, fmt.Errorf("update work order status: %w", err)
	}
	from := t.from
	if err := insertHistory(ctx, tx, workOrderID, &from, to, cause, messageID, now); err != nil {
		return nil, err
	}
	t.workOrder.Status = to
	return &t, nil
}

type quoteContext struct {
	code          string
	branch        string
	customerName  string
	customerEmail string
	items         []messaging.QuoteItem
	shortage      bool
}

func loadQuoteContext(ctx context.Context, tx pgx.Tx, workOrderID uuid.UUID) (*quoteContext, error) {
	var q quoteContext
	err := tx.QueryRow(ctx, `
		SELECT wo.code, wo.branch, c.name, COALESCE(c.email, '')
		FROM work_orders wo JOIN customers c ON c.id = wo.customer_id
		WHERE wo.id = $1`, workOrderID).Scan(&q.code, &q.branch, &q.customerName, &q.customerEmail)
	if err != nil {
		return nil, fmt.Errorf("load customer: %w", err)
	}

	rows, err := tx.Query(ctx, `
		SELECT wos.id, wos.service_title_snapshot,
			wos.service_price_cents_snapshot + COALESCE((
				SELECT SUM(woss.supply_price_cents_snapshot * woss.supply_quantity)
				FROM work_order_service_supplies woss WHERE woss.work_order_service_id = wos.id), 0),
			wos.service_estimated_time_minutes_snapshot
		FROM work_order_services wos
		WHERE wos.work_order_id = $1
		ORDER BY wos.created_at, wos.id`, workOrderID)
	if err != nil {
		return nil, fmt.Errorf("load items: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item messaging.QuoteItem
		if err := rows.Scan(&item.WorkOrderServiceID, &item.Title, &item.PriceCents, &item.EstimatedTimeMinutes); err != nil {
			return nil, fmt.Errorf("scan item: %w", err)
		}
		q.items = append(q.items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	err = tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM work_order_service_supplies woss
			JOIN work_order_services wos ON wos.id = woss.work_order_service_id
			JOIN supplies s ON s.id = woss.supply_id
			WHERE wos.work_order_id = $1
			  AND woss.supply_quantity > s.stock_quantity - COALESCE((
				SELECT SUM(r.quantity) FROM supply_reservations r
				WHERE r.supply_id = s.id AND r.status = 'RESERVADA'), 0)
		)`, workOrderID).Scan(&q.shortage)
	if err != nil {
		return nil, fmt.Errorf("check supply shortage: %w", err)
	}
	return &q, nil
}

func quoteTotal(items []messaging.QuoteItem) int {
	total := 0
	for _, item := range items {
		total += item.PriceCents
	}
	return total
}

func setEstimatedTotal(ctx context.Context, tx pgx.Tx, workOrderID uuid.UUID, total int, now time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE work_orders SET total_estimated_price_cents = $2, updated_at = $3 WHERE id = $1`, workOrderID, total, now)
	return err
}

func applyApproval(ctx context.Context, tx pgx.Tx, workOrderID uuid.UUID, approved []uuid.UUID, approvedTotal int, now time.Time) error {
	if approved == nil {
		approved = []uuid.UUID{}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE work_order_services
		SET approval_status = CASE WHEN id = ANY($2) THEN 'APROVADO' ELSE 'REPROVADO' END, updated_at = $3
		WHERE work_order_id = $1`, workOrderID, approved, now); err != nil {
		return fmt.Errorf("apply approval: %w", err)
	}
	_, err := tx.Exec(ctx, `UPDATE work_orders SET approved_total_cents = $2, updated_at = $3 WHERE id = $1`, workOrderID, approvedTotal, now)
	return err
}

func rejectPendingItems(ctx context.Context, tx pgx.Tx, workOrderID uuid.UUID, now time.Time) error {
	_, err := tx.Exec(ctx, `
		UPDATE work_order_services SET approval_status = 'REPROVADO', updated_at = $2
		WHERE work_order_id = $1 AND approval_status = 'PENDENTE'`, workOrderID, now)
	return err
}

func setPaymentURL(ctx context.Context, tx pgx.Tx, workOrderID uuid.UUID, url string, now time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE work_orders SET payment_url = $2, updated_at = $3 WHERE id = $1`, workOrderID, url, now)
	return err
}

func approvedRepairItems(ctx context.Context, tx pgx.Tx, workOrderID uuid.UUID) ([]messaging.RepairItem, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, service_title_snapshot, service_estimated_time_minutes_snapshot
		FROM work_order_services
		WHERE work_order_id = $1 AND approval_status = 'APROVADO'
		ORDER BY created_at, id`, workOrderID)
	if err != nil {
		return nil, fmt.Errorf("load approved items: %w", err)
	}
	defer rows.Close()
	items := []messaging.RepairItem{}
	for rows.Next() {
		var item messaging.RepairItem
		if err := rows.Scan(&item.WorkOrderServiceID, &item.Title, &item.EstimatedTimeMinutes); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func reserveSupplies(ctx context.Context, tx pgx.Tx, workOrderID uuid.UUID, now time.Time) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO supply_reservations (id, work_order_id, work_order_service_id, supply_id, quantity, status, created_at, updated_at)
		SELECT gen_random_uuid(), wos.work_order_id, woss.work_order_service_id, woss.supply_id, woss.supply_quantity, 'RESERVADA', $2, $2
		FROM work_order_service_supplies woss
		JOIN work_order_services wos ON wos.id = woss.work_order_service_id
		WHERE wos.work_order_id = $1 AND wos.approval_status = 'APROVADO'
		ON CONFLICT (work_order_service_id, supply_id) DO NOTHING`, workOrderID, now)
	if err != nil {
		return fmt.Errorf("reserve supplies: %w", err)
	}
	return nil
}

func releaseSupplies(ctx context.Context, tx pgx.Tx, workOrderID uuid.UUID, now time.Time) error {
	_, err := tx.Exec(ctx, `
		UPDATE supply_reservations SET status = 'LIBERADA', updated_at = $2
		WHERE work_order_id = $1 AND status = 'RESERVADA'`, workOrderID, now)
	if err != nil {
		return fmt.Errorf("release supplies: %w", err)
	}
	return nil
}

func consumeSupplies(ctx context.Context, tx pgx.Tx, workOrderID uuid.UUID, item *uuid.UUID, now time.Time) error {
	_, err := tx.Exec(ctx, `
		WITH consumed AS (
			UPDATE supply_reservations SET status = 'CONSUMIDA', updated_at = $3
			WHERE work_order_id = $1 AND ($2::uuid IS NULL OR work_order_service_id = $2) AND status = 'RESERVADA'
			RETURNING supply_id, quantity
		)
		UPDATE supplies s SET stock_quantity = s.stock_quantity - c.total, updated_at = $3
		FROM (SELECT supply_id, SUM(quantity) AS total FROM consumed GROUP BY supply_id) c
		WHERE s.id = c.supply_id`, workOrderID, item, now)
	if err != nil {
		return fmt.Errorf("consume supplies: %w", err)
	}
	return nil
}

func markItemStarted(ctx context.Context, tx pgx.Tx, workOrderID, itemID uuid.UUID, at time.Time) error {
	_, err := tx.Exec(ctx, `
		UPDATE work_order_services SET status = 'EM_EXECUCAO', started_at = $3, updated_at = $3
		WHERE id = $2 AND work_order_id = $1 AND started_at IS NULL`, workOrderID, itemID, at)
	return err
}

func markItemFinished(ctx context.Context, tx pgx.Tx, workOrderID, itemID uuid.UUID, at time.Time) error {
	_, err := tx.Exec(ctx, `
		UPDATE work_order_services
		SET status = 'FINALIZADO', finished_at = $3, started_at = COALESCE(started_at, $3), updated_at = $3
		WHERE id = $2 AND work_order_id = $1 AND finished_at IS NULL`, workOrderID, itemID, at)
	return err
}

func finishApprovedItems(ctx context.Context, tx pgx.Tx, workOrderID uuid.UUID, at time.Time) error {
	_, err := tx.Exec(ctx, `
		UPDATE work_order_services
		SET status = 'FINALIZADO', finished_at = $2, started_at = COALESCE(started_at, $2), updated_at = $2
		WHERE work_order_id = $1 AND approval_status = 'APROVADO' AND finished_at IS NULL`, workOrderID, at)
	if err != nil {
		return fmt.Errorf("finish approved items: %w", err)
	}
	return nil
}
