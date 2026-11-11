package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/application"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/domain"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestGetPublicStatus_Success(t *testing.T) {
	woRepo := new(mockWorkOrderRepo)
	custRepo := new(mockCustomerRepo)
	wosRepo := new(mockWorkOrderServiceRepo)
	svc := NewPublicWorkOrderService(woRepo, custRepo, wosRepo)

	ctx := context.Background()
	customerID := uuid.New()
	woID := uuid.New()
	now := time.Now()

	wo := &domain.WorkOrder{
		ID:         woID,
		Code:       "OS-20260504-A1B2",
		Status:     domain.WorkOrderStatusInProgress,
		CustomerID: customerID,
		ReceivedAt: now,
	}

	customer := &domain.Customer{
		ID:       customerID,
		Document: "12345678901",
	}

	services := []domain.WorkOrderService{
		{
			ServiceTitleSnapshot: "Troca de óleo",
			Status:               domain.WorkOrderServiceStatusInProgress,
			ApprovalStatus:       domain.WorkOrderServiceApprovalApproved,
		},
	}

	woRepo.On("FindByCode", ctx, "OS-20260504-A1B2").Return(wo, nil)
	custRepo.On("FindByID", ctx, customerID).Return(customer, nil)
	wosRepo.On("FindByWorkOrderID", ctx, woID).Return(services, nil)
	woRepo.On("FindStatusHistory", ctx, woID).Return([]domain.WorkOrderStatusChange{{To: domain.WorkOrderStatusReceived, ChangedAt: now}}, nil)

	view, err := svc.GetPublicStatus(ctx, "OS-20260504-A1B2", "123.456.789-01")
	assert.NoError(t, err)
	assert.Nil(t, view.PaymentURL)
	assert.Len(t, view.History, 1)
	assert.Equal(t, "OS-20260504-A1B2", view.Code)
	assert.Equal(t, domain.WorkOrderStatusInProgress, view.Status)
	assert.Len(t, view.Services, 1)
	assert.Equal(t, "Troca de óleo", view.Services[0].Title)
}

func TestGetPublicStatus_WorkOrderNotFound(t *testing.T) {
	woRepo := new(mockWorkOrderRepo)
	custRepo := new(mockCustomerRepo)
	wosRepo := new(mockWorkOrderServiceRepo)
	svc := NewPublicWorkOrderService(woRepo, custRepo, wosRepo)

	ctx := context.Background()

	woRepo.On("FindByCode", ctx, "OS-INVALID").Return(nil, application.ErrNotFound)

	view, err := svc.GetPublicStatus(ctx, "OS-INVALID", "12345678901")
	assert.ErrorIs(t, err, ErrWorkOrderNotFound)
	assert.Nil(t, view)
}

func TestGetPublicStatus_DocumentMismatch(t *testing.T) {
	woRepo := new(mockWorkOrderRepo)
	custRepo := new(mockCustomerRepo)
	wosRepo := new(mockWorkOrderServiceRepo)
	svc := NewPublicWorkOrderService(woRepo, custRepo, wosRepo)

	ctx := context.Background()
	customerID := uuid.New()

	wo := &domain.WorkOrder{
		ID:         uuid.New(),
		Code:       "OS-20260504-A1B2",
		CustomerID: customerID,
	}

	customer := &domain.Customer{
		ID:       customerID,
		Document: "12345678901",
	}

	woRepo.On("FindByCode", ctx, "OS-20260504-A1B2").Return(wo, nil)
	custRepo.On("FindByID", ctx, customerID).Return(customer, nil)

	view, err := svc.GetPublicStatus(ctx, "OS-20260504-A1B2", "99999999999")
	assert.ErrorIs(t, err, ErrWorkOrderNotFound)
	assert.Nil(t, view)
}

func TestGetPublicStatus_ShowsPaymentURLWhileWaitingPayment(t *testing.T) {
	woRepo := new(mockWorkOrderRepo)
	custRepo := new(mockCustomerRepo)
	wosRepo := new(mockWorkOrderServiceRepo)
	svc := NewPublicWorkOrderService(woRepo, custRepo, wosRepo)
	ctx := context.Background()
	customerID, woID := uuid.New(), uuid.New()
	url := "http://mp/checkout/1"

	woRepo.On("FindByCode", ctx, "OS-1").Return(&domain.WorkOrder{ID: woID, Code: "OS-1", Status: domain.WorkOrderStatusWaitingPayment, CustomerID: customerID, PaymentURL: &url}, nil)
	custRepo.On("FindByID", ctx, customerID).Return(&domain.Customer{ID: customerID, Document: "12345678901"}, nil)
	wosRepo.On("FindByWorkOrderID", ctx, woID).Return([]domain.WorkOrderService{}, nil)
	woRepo.On("FindStatusHistory", ctx, woID).Return(nil, errors.New("db down")).Once()

	_, err := svc.GetPublicStatus(ctx, "OS-1", "12345678901")
	assert.Error(t, err)

	woRepo.On("FindStatusHistory", ctx, woID).Return([]domain.WorkOrderStatusChange{}, nil)
	view, err := svc.GetPublicStatus(ctx, "OS-1", "12345678901")
	assert.NoError(t, err)
	assert.Equal(t, &url, view.PaymentURL)
}

func TestWorkOrderHistory(t *testing.T) {
	woRepo := new(mockWorkOrderRepo)
	svc := NewWorkOrderService(woRepo, new(mockVehicleRepo), new(mockWorkOrderOpener))
	ctx := context.Background()
	id, missing := uuid.New(), uuid.New()

	woRepo.On("FindByID", ctx, id).Return(&domain.WorkOrder{ID: id}, nil)
	woRepo.On("FindByID", ctx, missing).Return(nil, application.ErrNotFound)
	woRepo.On("FindStatusHistory", ctx, id).Return([]domain.WorkOrderStatusChange{{To: domain.WorkOrderStatusReceived}}, nil)

	history, err := svc.History(ctx, id)
	assert.NoError(t, err)
	assert.Len(t, history, 1)

	_, err = svc.History(ctx, missing)
	assert.ErrorIs(t, err, application.ErrNotFound)
}
