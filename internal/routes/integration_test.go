package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/auth"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/config"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/database"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/messaging"
	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type apiClient struct {
	t     *testing.T
	app   *fiber.App
	token string
}

func (c *apiClient) do(method, path string, body any, headers map[string]string) (int, []byte) {
	c.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(c.t, err)
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.app.Test(req, fiber.TestConfig{Timeout: 10 * time.Second})
	require.NoError(c.t, err)
	out, err := io.ReadAll(resp.Body)
	require.NoError(c.t, err)
	return resp.StatusCode, out
}

func (c *apiClient) json(method, path string, body any, want int) map[string]any {
	c.t.Helper()
	status, raw := c.do(method, path, body, nil)
	require.Equal(c.t, want, status, string(raw))
	out := map[string]any{}
	require.NoError(c.t, json.Unmarshal(raw, &out))
	return out
}

func TestIntegration_WorkOrderLifecycleThroughRegisteredRoutes(t *testing.T) {
	if os.Getenv("DATABASE_HOST") == "" {
		t.Skip("DATABASE_HOST nao definido")
	}
	t.Setenv("SAGA_COMMAND_TIMEOUT", "30s")
	cfg, err := config.Load()
	require.NoError(t, err)
	cfg.JWT.SecretKey = "test-secret"
	cfg.Internal.ServiceToken = "internal-token"
	cfg.Messaging.ExecutionInboxURL = "execution-inbox"
	cfg.Messaging.BillingInboxURL = "billing-inbox"

	db, err := database.NewConnection(cfg.Database)
	require.NoError(t, err)
	t.Cleanup(db.Close)
	ctx := context.Background()
	_, err = db.Exec(ctx, `DROP SCHEMA public CASCADE`)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `CREATE SCHEMA public`)
	require.NoError(t, err)
	require.NoError(t, database.RunMigrations(db))

	orchestrator := NewOrchestrator(db, cfg, nil)
	app := fiber.New()
	RegisterRoutes(app, db, cfg, orchestrator)
	c := &apiClient{t: t, app: app}

	internal := map[string]string{"X-Service-Token": "internal-token"}
	status, _ := c.do(http.MethodPost, "/internal/users", map[string]string{"username": "op", "password": "pw", "role": "admin"}, nil)
	assert.Equal(t, fiber.StatusUnauthorized, status)
	status, _ = c.do(http.MethodPost, "/internal/users", map[string]string{"username": "op", "password": "pw", "role": "admin"}, internal)
	assert.Equal(t, fiber.StatusCreated, status)
	status, _ = c.do(http.MethodPost, "/internal/users", map[string]string{"username": "op", "password": "pw", "role": "admin"}, internal)
	assert.Equal(t, fiber.StatusConflict, status)
	status, raw := c.do(http.MethodPost, "/internal/credentials/verify", map[string]string{"username": "op", "password": "pw"}, internal)
	assert.Equal(t, fiber.StatusOK, status)
	assert.Contains(t, string(raw), `"role":"admin"`)
	status, _ = c.do(http.MethodPost, "/internal/credentials/verify", map[string]string{"username": "op", "password": "x"}, internal)
	assert.Equal(t, fiber.StatusUnauthorized, status)

	c.token, err = auth.GenerateToken("op", "admin", "test-secret")
	require.NoError(t, err)

	customer := c.json(http.MethodPost, "/customers", map[string]any{
		"name": "Ana", "document": "12345678909", "document_type": "CPF", "email": "ana@example.com", "phone": "11999999999",
	}, fiber.StatusCreated)
	vehicle := c.json(http.MethodPost, "/vehicles", map[string]any{
		"customer_id": customer["id"], "license_plate": "ABC1D23", "brand": "Fiat", "model": "Uno", "year": 2020,
	}, fiber.StatusCreated)
	wo := c.json(http.MethodPost, "/work-orders", map[string]any{
		"title": "Revisao", "customer_id": customer["id"], "vehicle_id": vehicle["id"],
	}, fiber.StatusCreated)
	assert.Equal(t, "RECEBIDA", wo["status"])
	assert.Equal(t, "MATRIZ", wo["branch"])
	id := wo["id"].(string)

	saga := c.json(http.MethodGet, "/work-orders/"+id+"/saga", nil, fiber.StatusOK)
	assert.Equal(t, "SCHEDULE_DIAGNOSIS", saga["current_step"])

	var outboxType string
	require.NoError(t, db.QueryRow(ctx, `SELECT message_type FROM outbox`).Scan(&outboxType))
	assert.Equal(t, messaging.TypeScheduleDiagnosis, outboxType)

	status, _ = c.do(http.MethodPut, "/work-orders/"+id, map[string]any{"status": "ENTREGUE"}, nil)
	assert.Equal(t, fiber.StatusBadRequest, status)
	status, _ = c.do(http.MethodPost, "/work-orders/"+id+"/deliver", nil, nil)
	assert.Equal(t, fiber.StatusUnprocessableEntity, status)

	canceled := c.json(http.MethodPost, "/work-orders/"+id+"/cancel", nil, fiber.StatusAccepted)
	assert.Equal(t, "COMPENSATING", canceled["status"])
	status, _ = c.do(http.MethodPost, "/work-orders/"+id+"/cancel", nil, nil)
	assert.Equal(t, fiber.StatusConflict, status)

	status, raw = c.do(http.MethodGet, "/work-orders/"+id+"/history", nil, nil)
	assert.Equal(t, fiber.StatusOK, status)
	assert.Contains(t, string(raw), `"cause":"work_order.opened"`)

	status, raw = c.do(http.MethodGet, "/public/work-orders/"+wo["code"].(string)+"?document=123.456.789-09", nil, nil)
	assert.Equal(t, fiber.StatusOK, status)
	assert.Contains(t, string(raw), `"history":[{"status":"RECEBIDA"`)
	assert.NotContains(t, string(raw), "payment_url")
}
