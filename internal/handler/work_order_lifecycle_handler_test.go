package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/application"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/domain"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type mockLifecycle struct{ mock.Mock }

func (m *mockLifecycle) Deliver(ctx context.Context, id uuid.UUID) error {
	return m.Called(ctx, id).Error(0)
}

func (m *mockLifecycle) Cancel(ctx context.Context, id uuid.UUID) (*domain.SagaInstance, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.SagaInstance), args.Error(1)
}

func (m *mockLifecycle) Get(ctx context.Context, id uuid.UUID) (*domain.SagaInstance, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.SagaInstance), args.Error(1)
}

func setupLifecycleApp() (*fiber.App, *mockWorkOrderService, *mockLifecycle) {
	svc := new(mockWorkOrderService)
	lifecycle := new(mockLifecycle)
	h := NewWorkOrderLifecycleHandler(svc, lifecycle)
	app := fiber.New()
	app.Post("/work-orders/:id/deliver", h.Deliver)
	app.Post("/work-orders/:id/cancel", h.Cancel)
	app.Get("/work-orders/:id/history", h.History)
	app.Get("/work-orders/:id/saga", h.Saga)
	return app, svc, lifecycle
}

func call(t *testing.T, app *fiber.App, method, path string) int {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(method, path, nil))
	require.NoError(t, err)
	return resp.StatusCode
}

func TestLifecycle_InvalidIDs(t *testing.T) {
	app, _, _ := setupLifecycleApp()
	assert.Equal(t, fiber.StatusBadRequest, call(t, app, http.MethodPost, "/work-orders/x/deliver"))
	assert.Equal(t, fiber.StatusBadRequest, call(t, app, http.MethodPost, "/work-orders/x/cancel"))
	assert.Equal(t, fiber.StatusBadRequest, call(t, app, http.MethodGet, "/work-orders/x/history"))
	assert.Equal(t, fiber.StatusBadRequest, call(t, app, http.MethodGet, "/work-orders/x/saga"))
}

func TestLifecycle_Deliver(t *testing.T) {
	app, svc, lifecycle := setupLifecycleApp()
	ok, invalid, missing, broken := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	lifecycle.On("Deliver", mock.Anything, ok).Return(nil)
	lifecycle.On("Deliver", mock.Anything, invalid).Return(domain.ErrInvalidStatusTransition)
	lifecycle.On("Deliver", mock.Anything, missing).Return(application.ErrNotFound)
	lifecycle.On("Deliver", mock.Anything, broken).Return(nil)
	svc.On("GetByID", mock.Anything, ok).Return(&domain.WorkOrder{ID: ok, Status: domain.WorkOrderStatusDelivered}, nil)
	svc.On("GetByID", mock.Anything, broken).Return(nil, errors.New("db"))

	assert.Equal(t, fiber.StatusOK, call(t, app, http.MethodPost, "/work-orders/"+ok.String()+"/deliver"))
	assert.Equal(t, fiber.StatusUnprocessableEntity, call(t, app, http.MethodPost, "/work-orders/"+invalid.String()+"/deliver"))
	assert.Equal(t, fiber.StatusNotFound, call(t, app, http.MethodPost, "/work-orders/"+missing.String()+"/deliver"))
	assert.Equal(t, fiber.StatusInternalServerError, call(t, app, http.MethodPost, "/work-orders/"+broken.String()+"/deliver"))
}

func TestLifecycle_Cancel(t *testing.T) {
	app, _, lifecycle := setupLifecycleApp()
	ok, late := uuid.New(), uuid.New()
	lifecycle.On("Cancel", mock.Anything, ok).Return(&domain.SagaInstance{Status: domain.SagaCompensating}, nil)
	lifecycle.On("Cancel", mock.Anything, late).Return(nil, application.ErrSagaNotCancelable)

	assert.Equal(t, fiber.StatusAccepted, call(t, app, http.MethodPost, "/work-orders/"+ok.String()+"/cancel"))
	assert.Equal(t, fiber.StatusConflict, call(t, app, http.MethodPost, "/work-orders/"+late.String()+"/cancel"))
}

func TestLifecycle_HistoryAndSaga(t *testing.T) {
	app, svc, lifecycle := setupLifecycleApp()
	ok, missing := uuid.New(), uuid.New()
	svc.On("History", mock.Anything, ok).Return([]domain.WorkOrderStatusChange{{To: domain.WorkOrderStatusReceived}}, nil)
	svc.On("History", mock.Anything, missing).Return(nil, application.ErrNotFound)
	lifecycle.On("Get", mock.Anything, ok).Return(&domain.SagaInstance{Status: domain.SagaStarted}, nil)
	lifecycle.On("Get", mock.Anything, missing).Return(nil, application.ErrNotFound)

	assert.Equal(t, fiber.StatusOK, call(t, app, http.MethodGet, "/work-orders/"+ok.String()+"/history"))
	assert.Equal(t, fiber.StatusNotFound, call(t, app, http.MethodGet, "/work-orders/"+missing.String()+"/history"))
	assert.Equal(t, fiber.StatusOK, call(t, app, http.MethodGet, "/work-orders/"+ok.String()+"/saga"))
	assert.Equal(t, fiber.StatusNotFound, call(t, app, http.MethodGet, "/work-orders/"+missing.String()+"/saga"))
}
