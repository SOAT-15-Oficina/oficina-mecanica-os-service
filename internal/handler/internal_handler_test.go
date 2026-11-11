package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/application"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/domain"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/service"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type mockCredentialService struct{ mock.Mock }

func (m *mockCredentialService) Verify(ctx context.Context, username, password string) (*domain.User, error) {
	args := m.Called(ctx, username, password)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}

func (m *mockCredentialService) Register(ctx context.Context, username, password string, role domain.UserRole) (*domain.User, error) {
	args := m.Called(ctx, username, password, role)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}

func internalApp(svc *mockCredentialService) *fiber.App {
	h := NewInternalHandler(svc)
	app := fiber.New()
	app.Post("/internal/credentials/verify", h.VerifyCredentials)
	app.Post("/internal/users", h.CreateUser)
	return app
}

func postInternal(t *testing.T, app *fiber.App, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func TestInternal_VerifyCredentials(t *testing.T) {
	svc := new(mockCredentialService)
	app := internalApp(svc)
	id := uuid.New()
	svc.On("Verify", mock.Anything, "ana", "ok").Return(&domain.User{ID: id, Username: "ana", PasswordHash: "h", Role: domain.UserRoleAdmin}, nil)
	svc.On("Verify", mock.Anything, "ana", "bad").Return(nil, service.ErrInvalidCredentials)
	svc.On("Verify", mock.Anything, "ana", "boom").Return(nil, errors.New("db"))

	status, body := postInternal(t, app, "/internal/credentials/verify", `{"username":"ana","password":"ok"}`)
	assert.Equal(t, fiber.StatusOK, status)
	assert.Equal(t, map[string]any{"id": id.String(), "username": "ana", "role": "admin"}, body)

	status, _ = postInternal(t, app, "/internal/credentials/verify", `{"username":"ana","password":"bad"}`)
	assert.Equal(t, fiber.StatusUnauthorized, status)
	status, _ = postInternal(t, app, "/internal/credentials/verify", `{"username":"ana","password":"boom"}`)
	assert.Equal(t, fiber.StatusInternalServerError, status)
	status, _ = postInternal(t, app, "/internal/credentials/verify", `{`)
	assert.Equal(t, fiber.StatusBadRequest, status)
}

func TestInternal_CreateUser(t *testing.T) {
	svc := new(mockCredentialService)
	app := internalApp(svc)
	svc.On("Register", mock.Anything, "ana", "x", domain.UserRoleEmployee).Return(&domain.User{ID: uuid.New(), Username: "ana", Role: domain.UserRoleEmployee}, nil)
	svc.On("Register", mock.Anything, "dup", "x", domain.UserRoleEmployee).Return(nil, &pgconn.PgError{Code: "23505", ConstraintName: "users_username_key"})
	svc.On("Register", mock.Anything, "", "x", domain.UserRoleEmployee).Return(nil, application.NewValidationError("username is required"))
	svc.On("Register", mock.Anything, "err", "x", domain.UserRoleEmployee).Return(nil, errors.New("db"))

	status, body := postInternal(t, app, "/internal/users", `{"username":"ana","password":"x","role":"employee"}`)
	assert.Equal(t, fiber.StatusCreated, status)
	assert.Equal(t, "ana", body["username"])
	assert.NotContains(t, body, "password_hash")

	status, _ = postInternal(t, app, "/internal/users", `{"username":"dup","password":"x","role":"employee"}`)
	assert.Equal(t, fiber.StatusConflict, status)
	status, body = postInternal(t, app, "/internal/users", `{"username":"","password":"x","role":"employee"}`)
	assert.Equal(t, fiber.StatusBadRequest, status)
	assert.Equal(t, "username is required", body["error"])
	status, _ = postInternal(t, app, "/internal/users", `{"username":"err","password":"x","role":"employee"}`)
	assert.Equal(t, fiber.StatusInternalServerError, status)
	status, _ = postInternal(t, app, "/internal/users", `nope`)
	assert.Equal(t, fiber.StatusBadRequest, status)
}
