package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/application"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var workOrderColumnNames = []string{
	"id", "code", "title", "description", "customer_id", "vehicle_id", "opened_by_user_id",
	"assigned_technician_id", "status", "branch", "total_estimated_price_cents", "approved_total_cents",
	"payment_url", "received_at", "quote_sent_at", "approved_at", "started_at", "finished_at",
	"delivered_at", "created_at", "updated_at",
}

const workOrderJoins = `
		JOIN customers c ON wo.customer_id = c.id
		JOIN vehicles v ON wo.vehicle_id = v.id
		LEFT JOIN users t ON wo.assigned_technician_id = t.id`

const workOrderJoinColumns = `c.id, c.name, c.document,
		v.id, v.license_plate, v.brand, v.model, v.year,
		t.id, t.username, t.role`

func workOrderColumns(prefix string) string {
	cols := make([]string, len(workOrderColumnNames))
	for i, name := range workOrderColumnNames {
		cols[i] = prefix + name
	}
	return strings.Join(cols, ", ")
}

func workOrderDest(wo *domain.WorkOrder) []any {
	return []any{
		&wo.ID, &wo.Code, &wo.Title, &wo.Description, &wo.CustomerID, &wo.VehicleID, &wo.OpenedByUserID,
		&wo.AssignedTechnicianID, &wo.Status, &wo.Branch, &wo.TotalEstimatedPriceCents, &wo.ApprovedTotalCents,
		&wo.PaymentURL, &wo.ReceivedAt, &wo.QuoteSentAt, &wo.ApprovedAt, &wo.StartedAt, &wo.FinishedAt,
		&wo.DeliveredAt, &wo.CreatedAt, &wo.UpdatedAt,
	}
}

type joinedWorkOrder struct {
	wo                 domain.WorkOrder
	customer           domain.WorkOrderCustomer
	vehicle            domain.WorkOrderVehicle
	technicianID       *uuid.UUID
	technicianUsername *string
	technicianRole     *domain.UserRole
}

func (j *joinedWorkOrder) dest() []any {
	return append(workOrderDest(&j.wo),
		&j.customer.ID, &j.customer.Name, &j.customer.Document,
		&j.vehicle.ID, &j.vehicle.LicensePlate, &j.vehicle.Brand, &j.vehicle.Model, &j.vehicle.Year,
		&j.technicianID, &j.technicianUsername, &j.technicianRole,
	)
}

func (j *joinedWorkOrder) result() domain.WorkOrder {
	wo := j.wo
	customer := j.customer
	vehicle := j.vehicle
	wo.Customer = &customer
	wo.Vehicle = &vehicle
	if j.technicianID != nil && j.technicianUsername != nil && j.technicianRole != nil {
		wo.AssignedTechnician = &domain.WorkOrderTechnician{
			ID: *j.technicianID, Username: *j.technicianUsername, Role: *j.technicianRole,
		}
	}
	return wo
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return application.ErrNotFound
	}
	return err
}

type workOrderRepository struct {
	db *pgxpool.Pool
}

func NewWorkOrderRepository(db *pgxpool.Pool) application.WorkOrderRepository {
	return &workOrderRepository{db: db}
}

func InsertWorkOrder(ctx context.Context, db DBTX, wo *domain.WorkOrder) (*domain.WorkOrder, error) {
	if wo.ID == uuid.Nil {
		wo.ID = uuid.New()
	}
	now := time.Now()
	if wo.CreatedAt.IsZero() {
		wo.CreatedAt = now
	}
	if wo.UpdatedAt.IsZero() {
		wo.UpdatedAt = now
	}
	if wo.Branch == "" {
		wo.Branch = "MATRIZ"
	}

	placeholders := make([]string, len(workOrderColumnNames))
	for i := range placeholders {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}
	query := fmt.Sprintf(`INSERT INTO work_orders (%s) VALUES (%s) RETURNING %s`,
		workOrderColumns(""), strings.Join(placeholders, ", "), workOrderColumns(""))

	args := []any{
		wo.ID, wo.Code, wo.Title, wo.Description, wo.CustomerID, wo.VehicleID, wo.OpenedByUserID,
		wo.AssignedTechnicianID, wo.Status, wo.Branch, wo.TotalEstimatedPriceCents, wo.ApprovedTotalCents,
		wo.PaymentURL, wo.ReceivedAt, wo.QuoteSentAt, wo.ApprovedAt, wo.StartedAt, wo.FinishedAt,
		wo.DeliveredAt, wo.CreatedAt, wo.UpdatedAt,
	}

	var result domain.WorkOrder
	if err := db.QueryRow(ctx, query, args...).Scan(workOrderDest(&result)...); err != nil {
		return nil, notFound(err)
	}
	return &result, nil
}

func (r *workOrderRepository) FindByID(ctx context.Context, id uuid.UUID) (*domain.WorkOrder, error) {
	query := fmt.Sprintf(`SELECT %s, %s FROM work_orders wo %s WHERE wo.id = $1`,
		workOrderColumns("wo."), workOrderJoinColumns, workOrderJoins)

	var j joinedWorkOrder
	if err := r.db.QueryRow(ctx, query, id).Scan(j.dest()...); err != nil {
		return nil, notFound(err)
	}
	result := j.result()

	services, err := r.fetchServicesForWorkOrder(ctx, result.ID)
	if err != nil {
		return nil, notFound(err)
	}
	result.Services = services
	return &result, nil
}

func (r *workOrderRepository) FindByCode(ctx context.Context, code string) (*domain.WorkOrder, error) {
	query := fmt.Sprintf(`SELECT %s FROM work_orders wo WHERE wo.code = $1`, workOrderColumns("wo."))

	var result domain.WorkOrder
	if err := r.db.QueryRow(ctx, query, code).Scan(workOrderDest(&result)...); err != nil {
		return nil, notFound(err)
	}
	return &result, nil
}

func (r *workOrderRepository) FindAll(ctx context.Context) ([]domain.WorkOrder, error) {
	query := fmt.Sprintf(`SELECT %s FROM work_orders wo ORDER BY wo.created_at DESC`, workOrderColumns("wo."))

	rows, err := r.db.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var workOrders []domain.WorkOrder
	for rows.Next() {
		var wo domain.WorkOrder
		if err := rows.Scan(workOrderDest(&wo)...); err != nil {
			return nil, err
		}
		workOrders = append(workOrders, wo)
	}
	return workOrders, rows.Err()
}

func (r *workOrderRepository) Update(ctx context.Context, wo *domain.WorkOrder) (*domain.WorkOrder, error) {
	query := fmt.Sprintf(`
		UPDATE work_orders wo
		SET title = $1, description = $2, assigned_technician_id = $3, updated_at = $4
		WHERE wo.id = $5
		RETURNING %s`, workOrderColumns("wo."))

	wo.UpdatedAt = time.Now()

	var result domain.WorkOrder
	err := r.db.QueryRow(ctx, query, wo.Title, wo.Description, wo.AssignedTechnicianID, wo.UpdatedAt, wo.ID).
		Scan(workOrderDest(&result)...)
	if err != nil {
		return nil, notFound(err)
	}
	return &result, nil
}

func (r *workOrderRepository) FindStatusHistory(ctx context.Context, workOrderID uuid.UUID) ([]domain.WorkOrderStatusChange, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, work_order_id, from_status, to_status, cause, message_id, changed_at
		FROM work_order_status_history
		WHERE work_order_id = $1
		ORDER BY seq`, workOrderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	history := make([]domain.WorkOrderStatusChange, 0)
	for rows.Next() {
		var h domain.WorkOrderStatusChange
		if err := rows.Scan(&h.ID, &h.WorkOrderID, &h.From, &h.To, &h.Cause, &h.MessageID, &h.ChangedAt); err != nil {
			return nil, err
		}
		history = append(history, h)
	}
	return history, rows.Err()
}

func workOrderStatusPriorityCase() string {
	var b strings.Builder
	b.WriteString("CASE wo.status")
	for i, status := range domain.WorkOrderListingStatusPriorityOrder {
		fmt.Fprintf(&b, " WHEN '%s' THEN %d", status, i+1)
	}
	fmt.Fprintf(&b, " ELSE %d END", domain.WorkOrderStatusDefaultSortPriority)
	return b.String()
}

func (r *workOrderRepository) FindAllWithFilters(ctx context.Context, filters application.WorkOrderListFilters) (*application.WorkOrderListResponse, error) {
	whereConditions := []string{}
	args := []any{}
	add := func(condition string, value any) {
		args = append(args, value)
		whereConditions = append(whereConditions, fmt.Sprintf(condition, len(args)))
	}

	for _, excluded := range domain.WorkOrderListingAlwaysExcludedStatuses {
		add("wo.status <> $%d", string(excluded))
	}
	if filters.Status != "" {
		add("wo.status = $%d", filters.Status)
	} else {
		for _, hidden := range domain.WorkOrderListingDefaultHiddenStatuses {
			add("wo.status <> $%d", string(hidden))
		}
	}
	if filters.CustomerID != uuid.Nil {
		add("wo.customer_id = $%d", filters.CustomerID)
	}
	if filters.VehicleID != uuid.Nil {
		add("wo.vehicle_id = $%d", filters.VehicleID)
	}
	if filters.FromDate != nil {
		add("wo.received_at >= $%d", filters.FromDate)
	}
	if filters.ToDate != nil {
		add("wo.received_at <= $%d", filters.ToDate)
	}

	whereClause := "1 = 1"
	if len(whereConditions) > 0 {
		whereClause = strings.Join(whereConditions, " AND ")
	}

	var total int
	if err := r.db.QueryRow(ctx, fmt.Sprintf("SELECT COUNT(*) FROM work_orders wo WHERE %s", whereClause), args...).Scan(&total); err != nil {
		return nil, err
	}

	offset := (filters.Page - 1) * filters.Limit
	args = append(args, filters.Limit, offset)

	query := fmt.Sprintf(`
		SELECT %s, %s
		FROM work_orders wo %s
		WHERE %s
		ORDER BY %s, wo.received_at ASC
		LIMIT $%d OFFSET $%d`,
		workOrderColumns("wo."), workOrderJoinColumns, workOrderJoins,
		whereClause, workOrderStatusPriorityCase(), len(args)-1, len(args))

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var workOrders []domain.WorkOrder
	for rows.Next() {
		var j joinedWorkOrder
		if err := rows.Scan(j.dest()...); err != nil {
			return nil, err
		}
		workOrders = append(workOrders, j.result())
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &application.WorkOrderListResponse{
		Data:       workOrders,
		Total:      total,
		Page:       filters.Page,
		Limit:      filters.Limit,
		TotalPages: (total + filters.Limit - 1) / filters.Limit,
	}, nil
}

func (r *workOrderRepository) fetchServicesForWorkOrder(ctx context.Context, workOrderID uuid.UUID) ([]domain.WorkOrderService, error) {
	query := `SELECT id, work_order_id, service_id, service_title_snapshot, service_description_snapshot, service_price_cents_snapshot, service_estimated_time_minutes_snapshot, approval_status, status, started_at, finished_at, created_at, updated_at FROM work_order_services WHERE work_order_id = $1 ORDER BY created_at, id`
	rows, err := r.db.Query(ctx, query, workOrderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	services := make([]domain.WorkOrderService, 0)
	for rows.Next() {
		var svc domain.WorkOrderService
		if err := rows.Scan(
			&svc.ID, &svc.WorkOrderID, &svc.ServiceID,
			&svc.ServiceTitleSnapshot, &svc.ServiceDescriptionSnapshot,
			&svc.ServicePriceCentsSnapshot, &svc.ServiceEstimatedTimeMinutesSnapshot,
			&svc.ApprovalStatus, &svc.Status, &svc.StartedAt, &svc.FinishedAt, &svc.CreatedAt, &svc.UpdatedAt,
		); err != nil {
			return nil, err
		}
		services = append(services, svc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range services {
		supplies, err := r.fetchSuppliesForService(ctx, services[i].ID)
		if err != nil {
			return nil, err
		}
		services[i].Supplies = supplies
	}
	return services, nil
}

func (r *workOrderRepository) fetchSuppliesForService(ctx context.Context, serviceID uuid.UUID) ([]domain.WorkOrderServiceSupply, error) {
	query := `SELECT id, work_order_service_id, supply_id, supply_title_snapshot, supply_price_cents_snapshot, supply_quantity, created_at, updated_at FROM work_order_service_supplies WHERE work_order_service_id = $1`
	rows, err := r.db.Query(ctx, query, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	supplies := make([]domain.WorkOrderServiceSupply, 0)
	for rows.Next() {
		var sup domain.WorkOrderServiceSupply
		if err := rows.Scan(
			&sup.ID, &sup.WorkOrderServiceID, &sup.SupplyID,
			&sup.SupplyTitleSnapshot, &sup.SupplyPriceCentsSnapshot,
			&sup.SupplyQuantity, &sup.CreatedAt, &sup.UpdatedAt,
		); err != nil {
			return nil, err
		}
		supplies = append(supplies, sup)
	}
	return supplies, rows.Err()
}
