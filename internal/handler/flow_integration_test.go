package handler

// Integration tests that validate the complete workshop DDD flow
// defined in the event storming.
//
// Each test maps to a step (or group of steps) in the flow:
//
//   DDD Step                          → API Route
//   ─────────────────────────────────────────────────────────────
//   Consulta Cliente                  → GET  /customers?document=
//   Cadastra Cliente                  → POST /customers
//   Consulta Veículo                  → GET  /vehicles?customerId=
//   Cadastra Veículo                  → POST /vehicles
//   Cria OS (status RECEBIDA)         → POST /work-orders
//   Listagem de OS disponíveis        → GET  /work-orders?status=RECEBIDA
//   Pega OS (EM_DIAGNOSTICO)          → PUT  /work-orders/:id
//   Atualiza OS com serviços          → POST /work-orders/:id/services
//   Adiciona peças/insumos            → POST /work-orders/:id/services/:wosId/supplies
//   Gera orçamento (AGUARDANDO_APROV) → PUT  /work-orders/:id
//   Cliente consulta status           → GET  /public/work-orders/:code?document=
//   Em Execução                       → PUT  /work-orders/:id
//   Finalizada                        → PUT  /work-orders/:id
//   Entregue                          → PUT  /work-orders/:id

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	dbmigrations "github.com/SOAT-15-Oficina/oficina-mecanica-os-service/database"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/auth"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/domain"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/repository"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/saga"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/service"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testUsername = "mechanic_test"

// setupFlowApp creates a Fiber app wired with real repositories and services
// against a real PostgreSQL database, mirroring the full application setup.
// Auth middleware is replaced with a stub that injects test claims.
func setupFlowApp(t *testing.T) (*fiber.App, *pgxpool.Pool) {
	t.Helper()

	pool := connectTestDB(t)
	setupFlowSchema(t, pool)
	seedTestUser(t, pool)

	// Repositories
	customerRepo := repository.NewCustomerRepository(pool)
	vehicleRepo := repository.NewVehicleRepository(pool)
	supplyRepo := repository.NewSupplyRepository(pool)
	wsRepo := repository.NewWorkshopServiceRepository(pool)
	woRepo := repository.NewWorkOrderRepository(pool)
	wosRepo := repository.NewWorkOrderServiceRepository(pool)
	userRepo := repository.NewUserRepository(pool)

	// Services
	customerSvc := service.NewCustomerService(customerRepo)
	vehicleSvc := service.NewVehicleService(vehicleRepo)
	supplySvc := service.NewSupplyService(supplyRepo, wosRepo)
	wsSvc := service.NewWorkshopServiceManager(wsRepo)
	orchestrator := saga.New(pool, saga.Config{ExecutionInbox: "execution-inbox", BillingInbox: "billing-inbox", CommandTimeout: time.Minute}, nil)
	woSvc := service.NewWorkOrderService(woRepo, vehicleRepo, orchestrator)
	creationSvc := service.NewWorkOrderCreationService(woRepo, wosRepo, wsRepo, supplyRepo)
	userSvc := service.NewUserService(userRepo)
	publicSvc := service.NewPublicWorkOrderService(woRepo, customerRepo, wosRepo)

	// Handlers
	customerHandler := NewCustomerHandler(customerSvc)
	vehicleHandler := NewVehicleHandler(vehicleSvc)
	supplyHandler := NewSupplyHandler(supplySvc)
	wsHandler := NewWorkshopServiceHandler(wsSvc)
	woHandler := NewWorkOrderHandler(woSvc, creationSvc, userSvc)
	publicWoHandler := NewPublicWorkOrderHandler(publicSvc)

	app := fiber.New()

	// Inject fake claims for protected routes
	fakeAuth := func(c fiber.Ctx) error {
		c.Locals("token", &auth.AppClaims{
			User: testUsername,
			Role: "employee",
		})
		return c.Next()
	}

	// Customer routes
	customers := app.Group("/customers", fakeAuth)
	customers.Post("/", customerHandler.Create)
	customers.Get("/", customerHandler.GetAll)
	customers.Get("/:id", customerHandler.GetByID)
	customers.Put("/:id", customerHandler.Update)
	customers.Delete("/:id", customerHandler.Delete)

	// Vehicle routes
	vehicles := app.Group("/vehicles", fakeAuth)
	vehicles.Post("/", vehicleHandler.Create)
	vehicles.Get("/", vehicleHandler.GetAll)
	vehicles.Get("/:id", vehicleHandler.GetByID)
	vehicles.Put("/:id", vehicleHandler.Update)
	vehicles.Delete("/:id", vehicleHandler.Delete)

	// Supply routes
	supplies := app.Group("/supplies", fakeAuth)
	supplies.Post("/", supplyHandler.Create)
	supplies.Get("/", supplyHandler.GetAll)
	supplies.Get("/:id", supplyHandler.GetByID)
	supplies.Put("/:id", supplyHandler.Update)
	supplies.Delete("/:id", supplyHandler.Delete)

	// Workshop service routes
	services := app.Group("/services", fakeAuth)
	services.Post("/", wsHandler.Create)
	services.Get("/", wsHandler.GetAll)
	services.Get("/:id", wsHandler.GetByID)
	services.Put("/:id", wsHandler.Update)
	services.Delete("/:id", wsHandler.Delete)

	// Work order routes
	workOrders := app.Group("/work-orders", fakeAuth)
	workOrders.Post("/", woHandler.Create)
	workOrders.Get("/", woHandler.GetAll)
	workOrders.Get("/:id", woHandler.GetByID)
	workOrders.Put("/:id", woHandler.Update)
	workOrders.Post("/:id/services", woHandler.AddServices)
	workOrders.Delete("/:id/services/:wosId", woHandler.RemoveService)
	workOrders.Post("/:id/services/:wosId/supplies", woHandler.AddSupplies)
	workOrders.Delete("/:id/services/:wosId/supplies/:supplyId", woHandler.RemoveSupplyFromService)

	// Public work order status (no auth)
	publicWO := app.Group("/public/work-orders")
	publicWO.Get("/:code", publicWoHandler.GetByCode)

	return app, pool
}

// setupFlowAppWithBudget is like setupFlowApp but wires up the BudgetService
// with a mock email provider, so budget generation and notification tests work.
func connectTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()

	host := envOrDefault("DATABASE_HOST", "localhost")
	port := envOrDefault("DATABASE_PORT", "5432")
	user := envOrDefault("DATABASE_USER", "techchallenge")
	password := os.Getenv("DATABASE_PASSWORD")
	if password == "" {
		t.Skip("skipping integration test: DATABASE_PASSWORD not set")
	}
	dbName := envOrDefault("DATABASE_NAME", "techchallenge-db")

	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s", user, password, host, port, dbName)
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Skipf("skipping integration test: cannot connect to database: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Skipf("skipping integration test: cannot ping database: %v", err)
	}

	return pool
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func setupFlowSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	resetSchema := func() {
		pool.Exec(ctx, `DROP SCHEMA public CASCADE`)
		pool.Exec(ctx, `CREATE SCHEMA public`)
	}
	resetSchema()

	// Apply all migrations via goose
	goose.SetBaseFS(dbmigrations.Migrations)
	require.NoError(t, goose.SetDialect("postgres"))
	db := stdlib.OpenDBFromPool(pool)
	require.NoError(t, goose.Up(db, "migrations"))

	t.Cleanup(func() {
		resetSchema()
		db.Close()
		pool.Close()
	})
}

// seedTestUser insere direto na tabela o usuario que o middleware falso
// referencia. O cadastro de credencial vive na Lambda do
// oficina-mecanica-serverless, entao o monolito nao tem como criar o usuario
// por HTTP -- e nem precisa: ele so consome `users` para resolver quem abriu a
// OS. O hash abaixo e um argon2id valido e fixo, nunca verificado aqui.
const testUserPasswordHash = "$argon2id$v=19$m=65536,t=1,p=4$" +
	"c29tZS1maXhlZC1zYWx0MDA$" +
	"ZmFrZS1kaWdlc3QtZm9yLXRlc3RzLW9ubHktMzJieXRlcw"

func seedTestUser(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()

	var id uuid.UUID
	err := pool.QueryRow(context.Background(), `
		INSERT INTO "users" ("id", "username", "password_hash", "role", "created_at", "updated_at")
		VALUES ($1, $2, $3, $4, NOW(), NOW())
		ON CONFLICT ("username") DO UPDATE SET "updated_at" = NOW()
		RETURNING "id"`,
		uuid.New(), testUsername, testUserPasswordHash, string(domain.UserRoleEmployee),
	).Scan(&id)
	require.NoError(t, err)

	return id.String()
}

// --- HTTP helpers ---

func flowPostJSON(app *fiber.App, path string, body any) (*http.Response, error) {
	data, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	return app.Test(req)
}

func flowPutJSON(app *fiber.App, path string, body any) (*http.Response, error) {
	data, _ := json.Marshal(body)
	req, _ := http.NewRequest("PUT", path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	return app.Test(req)
}

func flowGet(app *fiber.App, path string) (*http.Response, error) {
	req, _ := http.NewRequest("GET", path, nil)
	return app.Test(req)
}

func flowDelete(app *fiber.App, path string) (*http.Response, error) {
	req, _ := http.NewRequest("DELETE", path, nil)
	return app.Test(req)
}

func flowReadBody(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	defer resp.Body.Close()
	var result map[string]any
	require.NoError(t, json.Unmarshal(data, &result))
	return result
}

func flowReadBodyArray(t *testing.T, resp *http.Response) []map[string]any {
	t.Helper()
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	defer resp.Body.Close()
	var result []map[string]any
	require.NoError(t, json.Unmarshal(data, &result))
	return result
}

// =============================================================================
// Test: Full Happy Path — Complete workshop flow from customer creation to
// vehicle delivery, validating every status transition matches the DDD flow.
// =============================================================================

func TestIntegration_Flow_CannotAddServicesInWrongStatus(t *testing.T) {
	app, pool := setupFlowApp(t)

	customerID := createTestCustomer(t, app, "Roberto Lima", "roberto@example.com", "65464216740", "CPF")
	vehicleID := createTestVehicle(t, app, customerID, "MNO7P12", "Ford", "Ka", 2019)
	workOrderID := createTestWorkOrder(t, app, "Status test", customerID, vehicleID)

	svcID := createTestWorkshopService(t, app, "Serviço de Teste", 5000, 15)
	addServicesToWorkOrder(t, app, workOrderID, []string{svcID})

	_, err := pool.Exec(context.Background(), `UPDATE work_orders SET status = 'APROVADO' WHERE id = $1`, workOrderID)
	require.NoError(t, err)

	// Try to add more services — should fail
	t.Run("NaoPodeAdicionarServicosAposAprovacao", func(t *testing.T) {
		svc2ID := createTestWorkshopService(t, app, "Outro Serviço", 3000, 10)
		resp, err := flowPostJSON(app,
			fmt.Sprintf("/work-orders/%s/services", workOrderID),
			[]map[string]any{{"service_id": svc2ID}},
		)
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusUnprocessableEntity, resp.StatusCode,
			"should not allow adding services after approval")
	})
}

// =============================================================================
// Test: Public work order lookup validates customer document.
// =============================================================================

func TestIntegration_Flow_PublicLookupValidatesDocument(t *testing.T) {
	app, _ := setupFlowApp(t)

	customerID := createTestCustomer(t, app, "Fernanda Rocha", "fernanda@example.com", "01079054189", "CPF")
	vehicleID := createTestVehicle(t, app, customerID, "QRS8T34", "Renault", "Kwid", 2023)
	workOrderID := createTestWorkOrder(t, app, "Public lookup test", customerID, vehicleID)
	_ = workOrderID

	// Get the work order code
	resp, err := flowGet(app, "/work-orders/"+workOrderID)
	require.NoError(t, err)
	woBody := flowReadBody(t, resp)
	code := woBody["code"].(string)

	// Correct document
	t.Run("DocumentoCorreto", func(t *testing.T) {
		resp, err := flowGet(app, fmt.Sprintf("/public/work-orders/%s?document=01079054189", code))
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusOK, resp.StatusCode)
	})

	// Wrong document
	t.Run("DocumentoErrado", func(t *testing.T) {
		resp, err := flowGet(app, fmt.Sprintf("/public/work-orders/%s?document=00000000000", code))
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusNotFound, resp.StatusCode)
	})

	// Missing document
	t.Run("DocumentoAusente", func(t *testing.T) {
		resp, err := flowGet(app, fmt.Sprintf("/public/work-orders/%s", code))
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusBadRequest, resp.StatusCode)
	})
}

// =============================================================================
// Test: Remove service and supply from work order
// =============================================================================

func TestIntegration_Flow_RemoveServiceAndSupply(t *testing.T) {
	app, _ := setupFlowApp(t)

	customerID := createTestCustomer(t, app, "Lucia Santos", "lucia@example.com", "13471876421", "CPF")
	vehicleID := createTestVehicle(t, app, customerID, "UVW0X56", "Hyundai", "HB20", 2021)
	workOrderID := createTestWorkOrder(t, app, "Remove test", customerID, vehicleID)

	svcID := createTestWorkshopService(t, app, "Lavagem Completa", 5000, 60)
	wosIDs := addServicesToWorkOrder(t, app, workOrderID, []string{svcID})
	wosID := wosIDs[0]

	// Add a supply
	supplyID := createTestSupply(t, app, "Shampoo Automotivo", "INSUMO", 1500, 50, 5)
	resp, err := flowPostJSON(app,
		fmt.Sprintf("/work-orders/%s/services/%s/supplies", workOrderID, wosID),
		[]map[string]any{{"supply_id": supplyID, "quantity": 2}},
	)
	require.NoError(t, err)
	require.Equal(t, fiber.StatusCreated, resp.StatusCode)
	addedSupplies := flowReadBodyArray(t, resp)
	require.Len(t, addedSupplies, 1)
	wosSupplyID := addedSupplies[0]["id"].(string)

	// Remove the supply
	t.Run("RemoveSupply", func(t *testing.T) {
		resp, err := flowDelete(app, fmt.Sprintf("/work-orders/%s/services/%s/supplies/%s", workOrderID, wosID, wosSupplyID))
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusNoContent, resp.StatusCode)
	})

	// Remove the service
	t.Run("RemoveService", func(t *testing.T) {
		resp, err := flowDelete(app, fmt.Sprintf("/work-orders/%s/services/%s", workOrderID, wosID))
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusNoContent, resp.StatusCode)
	})
}

// =============================================================================
// Test: Idempotent approval — approving an already-approved service is a no-op.
// =============================================================================

func TestIntegration_Flow_DuplicateCustomerDocument(t *testing.T) {
	app, _ := setupFlowApp(t)

	createTestCustomer(t, app, "Primeiro Cliente", "primeiro@example.com", "12345678909", "CPF")

	t.Run("MesmoDocumentoRetorna409", func(t *testing.T) {
		resp, err := flowPostJSON(app, "/customers", map[string]any{
			"name":          "Outro Cliente",
			"email":         "outro@example.com",
			"document":      "12345678909",
			"document_type": "CPF",
		})
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusConflict, resp.StatusCode)
	})
}

func TestIntegration_Flow_DuplicateVehiclePlate(t *testing.T) {
	app, _ := setupFlowApp(t)

	customerID := createTestCustomer(t, app, "Cliente Veículo", "cv@example.com", "12345678909", "CPF")
	createTestVehicle(t, app, customerID, "ABC1D23", "Fiat", "Uno", 2020)

	t.Run("MesmaPlacaRetorna409", func(t *testing.T) {
		resp, err := flowPostJSON(app, "/vehicles", map[string]any{
			"license_plate": "ABC1D23",
			"customer_id":   customerID,
			"brand":         "VW",
			"model":         "Gol",
			"year":          2021,
		})
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusConflict, resp.StatusCode)
	})
}

func TestIntegration_Flow_DuplicateWorkshopServiceTitle(t *testing.T) {
	app, _ := setupFlowApp(t)

	createTestWorkshopService(t, app, "Troca de Óleo Único", 15000, 30)

	t.Run("MesmoTituloRetorna409", func(t *testing.T) {
		resp, err := flowPostJSON(app, "/services", map[string]any{
			"title":                  "Troca de Óleo Único",
			"price_cents":            20000,
			"estimated_time_minutes": 45,
		})
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusConflict, resp.StatusCode)
	})
}

func TestIntegration_Flow_DuplicateSupplyOnService(t *testing.T) {
	app, _ := setupFlowApp(t)

	customerID := createTestCustomer(t, app, "Dup Supply", "ds@example.com", "12345678909", "CPF")
	vehicleID := createTestVehicle(t, app, customerID, "DUP1A23", "Fiat", "Mobi", 2022)
	workOrderID := createTestWorkOrder(t, app, "Dup supply test", customerID, vehicleID)

	svcID := createTestWorkshopService(t, app, "Serviço Dup Supply", 5000, 30)
	wosIDs := addServicesToWorkOrder(t, app, workOrderID, []string{svcID})
	supplyID := createTestSupply(t, app, "Peça Única", "PECA", 1000, 10, 2)

	// First add — OK
	resp, err := flowPostJSON(app,
		fmt.Sprintf("/work-orders/%s/services/%s/supplies", workOrderID, wosIDs[0]),
		[]map[string]any{{"supply_id": supplyID, "quantity": 1}},
	)
	require.NoError(t, err)
	require.Equal(t, fiber.StatusCreated, resp.StatusCode)

	// Second add of same supply — should return 409
	t.Run("MesmoInsumoRetorna409", func(t *testing.T) {
		resp, err := flowPostJSON(app,
			fmt.Sprintf("/work-orders/%s/services/%s/supplies", workOrderID, wosIDs[0]),
			[]map[string]any{{"supply_id": supplyID, "quantity": 2}},
		)
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusConflict, resp.StatusCode)
	})
}

// =============================================================================
// Test: Validation errors — invalid input data must return 400.
// =============================================================================

func TestIntegration_Flow_CustomerValidationErrors(t *testing.T) {
	app, _ := setupFlowApp(t)

	t.Run("NomeFaltando", func(t *testing.T) {
		resp, err := flowPostJSON(app, "/customers", map[string]any{
			"email":         "sem-nome@example.com",
			"document":      "12345678909",
			"document_type": "CPF",
		})
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusBadRequest, resp.StatusCode)
	})

	t.Run("EmailInvalido", func(t *testing.T) {
		resp, err := flowPostJSON(app, "/customers", map[string]any{
			"name":          "Test",
			"email":         "nao-eh-email",
			"document":      "12345678909",
			"document_type": "CPF",
		})
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusBadRequest, resp.StatusCode)
	})

	t.Run("CPFInvalido", func(t *testing.T) {
		resp, err := flowPostJSON(app, "/customers", map[string]any{
			"name":          "Test",
			"email":         "test@example.com",
			"document":      "00000000000",
			"document_type": "CPF",
		})
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusBadRequest, resp.StatusCode)
	})

	t.Run("TipoDocumentoInvalido", func(t *testing.T) {
		resp, err := flowPostJSON(app, "/customers", map[string]any{
			"name":          "Test",
			"email":         "test@example.com",
			"document":      "12345678909",
			"document_type": "RG",
		})
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusBadRequest, resp.StatusCode)
	})
}

func TestIntegration_Flow_VehicleValidationErrors(t *testing.T) {
	app, _ := setupFlowApp(t)

	customerID := createTestCustomer(t, app, "Val Vehicle", "vv@example.com", "12345678909", "CPF")

	t.Run("PlacaInvalida", func(t *testing.T) {
		resp, err := flowPostJSON(app, "/vehicles", map[string]any{
			"license_plate": "INVALIDA",
			"customer_id":   customerID,
			"brand":         "Fiat",
			"model":         "Uno",
			"year":          2020,
		})
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusBadRequest, resp.StatusCode)
	})

	t.Run("AnoInvalido", func(t *testing.T) {
		resp, err := flowPostJSON(app, "/vehicles", map[string]any{
			"license_plate": "XYZ1A23",
			"customer_id":   customerID,
			"brand":         "Fiat",
			"model":         "Uno",
			"year":          1800,
		})
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusBadRequest, resp.StatusCode)
	})

	t.Run("SemPlaca", func(t *testing.T) {
		resp, err := flowPostJSON(app, "/vehicles", map[string]any{
			"customer_id": customerID,
			"brand":       "Fiat",
			"model":       "Uno",
			"year":        2020,
		})
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusBadRequest, resp.StatusCode)
	})
}

func TestIntegration_Flow_WorkshopServiceValidationErrors(t *testing.T) {
	app, _ := setupFlowApp(t)

	t.Run("SemTitulo", func(t *testing.T) {
		resp, err := flowPostJSON(app, "/services", map[string]any{
			"price_cents":            5000,
			"estimated_time_minutes": 30,
		})
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusBadRequest, resp.StatusCode)
	})

	t.Run("PrecoZero", func(t *testing.T) {
		resp, err := flowPostJSON(app, "/services", map[string]any{
			"title":                  "Serviço Grátis",
			"price_cents":            0,
			"estimated_time_minutes": 30,
		})
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusBadRequest, resp.StatusCode)
	})

	t.Run("TempoZero", func(t *testing.T) {
		resp, err := flowPostJSON(app, "/services", map[string]any{
			"title":                  "Serviço Instantâneo",
			"price_cents":            5000,
			"estimated_time_minutes": 0,
		})
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusBadRequest, resp.StatusCode)
	})
}

// =============================================================================
// Test: Referential integrity — work order with non-existent or mismatched refs.
// =============================================================================

func TestIntegration_Flow_WorkOrderReferentialErrors(t *testing.T) {
	app, _ := setupFlowApp(t)

	customerID := createTestCustomer(t, app, "Ref Test", "ref@example.com", "12345678909", "CPF")
	vehicleID := createTestVehicle(t, app, customerID, "REF1A23", "Fiat", "Uno", 2020)

	t.Run("ClienteInexistente", func(t *testing.T) {
		resp, err := flowPostJSON(app, "/work-orders", map[string]any{
			"title":       "OS com cliente fake",
			"customer_id": "00000000-0000-0000-0000-000000000000",
			"vehicle_id":  vehicleID,
		})
		require.NoError(t, err)
		assert.NotEqual(t, fiber.StatusCreated, resp.StatusCode)
	})

	t.Run("VeiculoInexistente", func(t *testing.T) {
		resp, err := flowPostJSON(app, "/work-orders", map[string]any{
			"title":       "OS com veículo fake",
			"customer_id": customerID,
			"vehicle_id":  "00000000-0000-0000-0000-000000000000",
		})
		require.NoError(t, err)
		assert.NotEqual(t, fiber.StatusCreated, resp.StatusCode)
	})

	t.Run("VeiculoNaoPertenceAoCliente", func(t *testing.T) {
		otherCustomerID := createTestCustomer(t, app, "Outro Dono", "outro@example.com", "98765432100", "CPF")
		resp, err := flowPostJSON(app, "/work-orders", map[string]any{
			"title":       "OS com veículo de outro cliente",
			"customer_id": otherCustomerID,
			"vehicle_id":  vehicleID,
		})
		require.NoError(t, err)
		assert.NotEqual(t, fiber.StatusCreated, resp.StatusCode,
			"should not allow creating work order with vehicle belonging to another customer")
	})

	t.Run("SemTitulo", func(t *testing.T) {
		resp, err := flowPostJSON(app, "/work-orders", map[string]any{
			"customer_id": customerID,
			"vehicle_id":  vehicleID,
		})
		require.NoError(t, err)
		assert.NotEqual(t, fiber.StatusCreated, resp.StatusCode)
	})
}

// =============================================================================
// Test: Adding inactive service to work order must fail.
// =============================================================================

func TestIntegration_Flow_CannotAddInactiveService(t *testing.T) {
	app, _ := setupFlowApp(t)

	customerID := createTestCustomer(t, app, "Inactive Svc", "inactive@example.com", "52998224725", "CPF")
	vehicleID := createTestVehicle(t, app, customerID, "INA1B23", "Honda", "Fit", 2021)
	workOrderID := createTestWorkOrder(t, app, "Inactive service test", customerID, vehicleID)

	// Create service then deactivate it
	svcID := createTestWorkshopService(t, app, "Serviço Desativado", 7000, 40)
	resp, err := flowPutJSON(app, "/services/"+svcID, map[string]any{"active": false})
	require.NoError(t, err)
	require.Equal(t, fiber.StatusOK, resp.StatusCode)

	// Try to add inactive service to work order — should fail
	resp, err = flowPostJSON(app,
		fmt.Sprintf("/work-orders/%s/services", workOrderID),
		[]map[string]any{{"service_id": svcID}},
	)
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusUnprocessableEntity, resp.StatusCode,
		"should not allow adding inactive service to work order")
}

// =============================================================================
// Test: Cannot start service when supplies exceed stock.
// Reproduces: add service with 1000 units of a supply that has stock=10,
// approve, transition to EM_EXECUCAO, then try to start → must fail 422.
// =============================================================================

func TestIntegration_Flow_NotFoundErrors(t *testing.T) {
	app, _ := setupFlowApp(t)

	fakeID := "00000000-0000-0000-0000-000000000001"

	t.Run("CustomerNotFound", func(t *testing.T) {
		resp, err := flowGet(app, "/customers/"+fakeID)
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusNotFound, resp.StatusCode)
	})

	t.Run("VehicleNotFound", func(t *testing.T) {
		resp, err := flowGet(app, "/vehicles/"+fakeID)
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusNotFound, resp.StatusCode)
	})

	t.Run("WorkOrderNotFound", func(t *testing.T) {
		resp, err := flowGet(app, "/work-orders/"+fakeID)
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusNotFound, resp.StatusCode)
	})

	t.Run("WorkshopServiceNotFound", func(t *testing.T) {
		resp, err := flowGet(app, "/services/"+fakeID)
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusNotFound, resp.StatusCode)
	})

	t.Run("SupplyNotFound", func(t *testing.T) {
		resp, err := flowGet(app, "/supplies/"+fakeID)
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusNotFound, resp.StatusCode)
	})

	t.Run("PublicWorkOrderNotFound", func(t *testing.T) {
		resp, err := flowGet(app, "/public/work-orders/OS-FAKE-CODE?document=12345678909")
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusNotFound, resp.StatusCode)
	})

}

// =============================================================================
// Test: Ownership validation — service/supply must belong to the work order.
// =============================================================================

func TestIntegration_Flow_OwnershipValidation(t *testing.T) {
	app, _ := setupFlowApp(t)

	customerID := createTestCustomer(t, app, "Owner Test", "owner@example.com", "12345678909", "CPF")
	vehicleID := createTestVehicle(t, app, customerID, "OWN1A23", "Fiat", "Toro", 2023)

	wo1ID := createTestWorkOrder(t, app, "WO 1", customerID, vehicleID)
	wo2ID := createTestWorkOrder(t, app, "WO 2", customerID, vehicleID)

	svcID := createTestWorkshopService(t, app, "Serviço Ownership", 5000, 30)
	wos1IDs := addServicesToWorkOrder(t, app, wo1ID, []string{svcID})

	t.Run("RemoverServicoDeOutraOS", func(t *testing.T) {
		// Try to remove wo1's service via wo2's route
		resp, err := flowDelete(app, fmt.Sprintf("/work-orders/%s/services/%s", wo2ID, wos1IDs[0]))
		require.NoError(t, err)
		assert.Equal(t, fiber.StatusUnprocessableEntity, resp.StatusCode,
			"should not allow removing service that belongs to another work order")
	})
}

// =============================================================================
// Test: Budget generation — transitioning to AGUARDANDO_APROVACAO triggers
// budget calculation, sets quote_sent_at, calculates total, and sends email.
// Cobre os passos do fluxo DDD: "Gera orçamento com tempo estimado",
// "Envia notificação para o cliente", "Atualiza OS para aguardando aprovação".
// =============================================================================

func createTestCustomer(t *testing.T, app *fiber.App, name, email, document, docType string) string {
	t.Helper()
	resp, err := flowPostJSON(app, "/customers", map[string]any{
		"name":          name,
		"email":         email,
		"document":      document,
		"document_type": docType,
	})
	require.NoError(t, err)
	require.Equal(t, fiber.StatusCreated, resp.StatusCode)
	body := flowReadBody(t, resp)
	return body["id"].(string)
}

func createTestVehicle(t *testing.T, app *fiber.App, customerID, plate, brand, model string, year int) string {
	t.Helper()
	resp, err := flowPostJSON(app, "/vehicles", map[string]any{
		"license_plate": plate,
		"customer_id":   customerID,
		"brand":         brand,
		"model":         model,
		"year":          year,
	})
	require.NoError(t, err)
	require.Equal(t, fiber.StatusCreated, resp.StatusCode)
	body := flowReadBody(t, resp)
	return body["id"].(string)
}

func createTestWorkOrder(t *testing.T, app *fiber.App, title, customerID, vehicleID string) string {
	t.Helper()
	resp, err := flowPostJSON(app, "/work-orders", map[string]any{
		"title":       title,
		"customer_id": customerID,
		"vehicle_id":  vehicleID,
	})
	require.NoError(t, err)
	require.Equal(t, fiber.StatusCreated, resp.StatusCode)
	body := flowReadBody(t, resp)
	return body["id"].(string)
}

func createTestWorkshopService(t *testing.T, app *fiber.App, title string, priceCents, estMinutes int) string {
	t.Helper()
	resp, err := flowPostJSON(app, "/services", map[string]any{
		"title":                  title,
		"price_cents":            priceCents,
		"estimated_time_minutes": estMinutes,
	})
	require.NoError(t, err)
	require.Equal(t, fiber.StatusCreated, resp.StatusCode)
	body := flowReadBody(t, resp)
	return body["id"].(string)
}

func createTestSupply(t *testing.T, app *fiber.App, title, supplyType string, priceCents, stock, minStock int) string {
	t.Helper()
	resp, err := flowPostJSON(app, "/supplies", map[string]any{
		"title":          title,
		"type":           supplyType,
		"price_cents":    priceCents,
		"stock_quantity": stock,
		"minimum_stock":  minStock,
	})
	require.NoError(t, err)
	require.Equal(t, fiber.StatusCreated, resp.StatusCode)
	body := flowReadBody(t, resp)
	return body["id"].(string)
}

func addServicesToWorkOrder(t *testing.T, app *fiber.App, workOrderID string, serviceIDs []string) []string {
	t.Helper()
	items := make([]map[string]any, len(serviceIDs))
	for i, id := range serviceIDs {
		items[i] = map[string]any{"service_id": id}
	}

	resp, err := flowPostJSON(app, fmt.Sprintf("/work-orders/%s/services", workOrderID), items)
	require.NoError(t, err)
	require.Equal(t, fiber.StatusCreated, resp.StatusCode)

	body := flowReadBodyArray(t, resp)
	ids := make([]string, len(body))
	for i, item := range body {
		ids[i] = item["id"].(string)
	}
	return ids
}

func insertWorkOrderRow(t *testing.T, pool *pgxpool.Pool, code, status string, receivedAt time.Time, customerID, vehicleID, openedBy string) string {
	t.Helper()
	id := uuid.New()
	now := time.Now()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO work_orders (
			id, code, title, description, customer_id, vehicle_id, opened_by_user_id,
			assigned_technician_id, status, total_estimated_price_cents, received_at,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		id, code, "OS "+code, nil,
		uuid.MustParse(customerID), uuid.MustParse(vehicleID), uuid.MustParse(openedBy),
		nil, status, 0, receivedAt, now, now,
	)
	require.NoError(t, err)
	return id.String()
}

func listWorkOrderIDs(t *testing.T, app *fiber.App, query string) (ids []string, statuses []string, total int, totalPages int) {
	t.Helper()
	resp, err := flowGet(app, "/work-orders"+query)
	require.NoError(t, err)
	require.Equal(t, fiber.StatusOK, resp.StatusCode)

	body := flowReadBody(t, resp)
	total = int(body["total"].(float64))
	totalPages = int(body["total_pages"].(float64))

	data, _ := body["data"].([]any)
	for _, item := range data {
		wo := item.(map[string]any)
		ids = append(ids, wo["id"].(string))
		statuses = append(statuses, wo["status"].(string))
	}
	return ids, statuses, total, totalPages
}

func TestIntegration_Flow_OperationalListingRules(t *testing.T) {
	app, pool := setupFlowApp(t)
	openedBy := seedTestUser(t, pool)

	customerA := createTestCustomer(t, app, "Listagem A", "listagem.a@example.com", "12345678909", "CPF")
	vehicleA := createTestVehicle(t, app, customerA, "LST1A23", "Fiat", "Uno", 2020)

	customerB := createTestCustomer(t, app, "Listagem B", "listagem.b@example.com", "52998224725", "CPF")
	vehicleB := createTestVehicle(t, app, customerB, "LST1B23", "Honda", "Fit", 2021)

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	h := func(n int) time.Time { return base.Add(time.Duration(n) * time.Hour) }

	e2 := insertWorkOrderRow(t, pool, "OS-LST-E2", "EM_EXECUCAO", h(2), customerA, vehicleA, openedBy)
	e1 := insertWorkOrderRow(t, pool, "OS-LST-E1", "EM_EXECUCAO", h(4), customerA, vehicleA, openedBy)
	a1 := insertWorkOrderRow(t, pool, "OS-LST-A1", "AGUARDANDO_APROVACAO", h(1), customerA, vehicleA, openedBy)
	d1 := insertWorkOrderRow(t, pool, "OS-LST-D1", "EM_DIAGNOSTICO", h(5), customerA, vehicleA, openedBy)
	r2 := insertWorkOrderRow(t, pool, "OS-LST-R2", "RECEBIDA", h(1), customerA, vehicleA, openedBy)
	r1 := insertWorkOrderRow(t, pool, "OS-LST-R1", "RECEBIDA", h(3), customerA, vehicleA, openedBy)
	ap1 := insertWorkOrderRow(t, pool, "OS-LST-AP1", "APROVADO", h(0), customerA, vehicleA, openedBy)
	cancA := insertWorkOrderRow(t, pool, "OS-LST-C1", "CANCELADA", h(1), customerA, vehicleA, openedBy)
	insertWorkOrderRow(t, pool, "OS-LST-F1", "FINALIZADA", h(1), customerA, vehicleA, openedBy)
	insertWorkOrderRow(t, pool, "OS-LST-EN1", "ENTREGUE", h(1), customerA, vehicleA, openedBy)

	t.Run("DefaultListing_ExcludesTerminalAndCancelled_OrdersByPriorityThenReceivedAt", func(t *testing.T) {
		ids, statuses, total, _ := listWorkOrderIDs(t, app, "?customerId="+customerA+"&limit=50")

		assert.Equal(t, 7, total, "total deve considerar as exclusões")
		assert.NotContains(t, statuses, "FINALIZADA")
		assert.NotContains(t, statuses, "ENTREGUE")
		assert.NotContains(t, statuses, "CANCELADA")

		assert.Equal(t, []string{e2, e1, a1, d1, r2, r1, ap1}, ids,
			"ordem esperada: EM_EXECUCAO(received asc) > AGUARDANDO_APROVACAO > EM_DIAGNOSTICO > RECEBIDA(received asc) > APROVADO")
	})

	t.Run("StatusFilterCannotBypassExclusion_Finished", func(t *testing.T) {
		ids, _, total, totalPages := listWorkOrderIDs(t, app, "?customerId="+customerA+"&status=FINALIZADA")
		assert.Equal(t, 0, total, "FINALIZADA nunca aparece, mesmo com filtro explícito")
		assert.Equal(t, 0, totalPages)
		assert.Empty(t, ids)
	})

	t.Run("StatusFilterCannotBypassExclusion_Delivered", func(t *testing.T) {
		ids, _, total, _ := listWorkOrderIDs(t, app, "?customerId="+customerA+"&status=ENTREGUE")
		assert.Equal(t, 0, total, "ENTREGUE nunca aparece, mesmo com filtro explícito")
		assert.Empty(t, ids)
	})

	t.Run("CancelledIsHiddenByDefaultButReachableViaExplicitFilter", func(t *testing.T) {
		ids, statuses, total, _ := listWorkOrderIDs(t, app, "?customerId="+customerA+"&status=CANCELADA")
		assert.Equal(t, 1, total)
		assert.Equal(t, []string{cancA}, ids)
		assert.Equal(t, []string{"CANCELADA"}, statuses)
	})

	t.Run("ExplicitStatusFilter_ReturnsOnlyThatStatus_OrderedByReceivedAt", func(t *testing.T) {
		ids, statuses, total, _ := listWorkOrderIDs(t, app, "?customerId="+customerA+"&status=EM_EXECUCAO")
		assert.Equal(t, 2, total)
		assert.Equal(t, []string{e2, e1}, ids, "received_at ASC dentro do status filtrado")
		assert.Equal(t, []string{"EM_EXECUCAO", "EM_EXECUCAO"}, statuses)
	})

	t.Run("ApprovedRemainsListable", func(t *testing.T) {
		ids, _, total, _ := listWorkOrderIDs(t, app, "?customerId="+customerA+"&status=APROVADO")
		assert.Equal(t, 1, total)
		assert.Equal(t, []string{ap1}, ids)
	})

	t.Run("PaginationConsidersExclusions", func(t *testing.T) {
		idsP1, _, total, totalPages := listWorkOrderIDs(t, app, "?customerId="+customerA+"&limit=3&page=1")
		assert.Equal(t, 7, total)
		assert.Equal(t, 3, totalPages)
		assert.Equal(t, []string{e2, e1, a1}, idsP1)

		idsP3, _, _, _ := listWorkOrderIDs(t, app, "?customerId="+customerA+"&limit=3&page=3")
		assert.Equal(t, []string{ap1}, idsP3, "última página traz apenas a OS de menor prioridade")
	})

	t.Run("DateFilterOnReceivedAt_ComposesWithExclusion", func(t *testing.T) {
		bMid := insertWorkOrderRow(t, pool, "OS-LST-BMID", "RECEBIDA", time.Date(2026, 1, 15, 9, 0, 0, 0, time.UTC), customerB, vehicleB, openedBy)
		insertWorkOrderRow(t, pool, "OS-LST-BOLD", "RECEBIDA", time.Date(2025, 12, 1, 9, 0, 0, 0, time.UTC), customerB, vehicleB, openedBy)
		insertWorkOrderRow(t, pool, "OS-LST-BNEW", "RECEBIDA", time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC), customerB, vehicleB, openedBy)
		insertWorkOrderRow(t, pool, "OS-LST-BCAN", "CANCELADA", time.Date(2026, 1, 16, 9, 0, 0, 0, time.UTC), customerB, vehicleB, openedBy)
		insertWorkOrderRow(t, pool, "OS-LST-BFIN", "FINALIZADA", time.Date(2026, 1, 17, 9, 0, 0, 0, time.UTC), customerB, vehicleB, openedBy)

		ids, statuses, total, _ := listWorkOrderIDs(t, app, "?customerId="+customerB+"&from=2026-01-01&to=2026-01-31&limit=50")
		assert.Equal(t, 1, total, "apenas a OS dentro do intervalo e não excluída")
		assert.Equal(t, []string{bMid}, ids)
		assert.Equal(t, []string{"RECEBIDA"}, statuses)
	})
}
