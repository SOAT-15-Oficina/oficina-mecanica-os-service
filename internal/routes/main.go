package routes

import (
	"context"
	"time"

	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/config"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/handler"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/repository"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/routes/middlewares"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/saga"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/service"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/packages/email"
	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5/pgxpool"
)

type databasePinger interface {
	Ping(context.Context) error
}

func NewOrchestrator(db *pgxpool.Pool, cfg *config.Config, emailProv email.Provider) *saga.Orchestrator {
	notifier := service.NewWorkOrderStatusNotifier(repository.NewCustomerRepository(db), email.NewWorkOrderNotificationSender(emailProv))
	return saga.New(db, saga.Config{
		ExecutionInbox:  cfg.Messaging.ExecutionInboxURL,
		BillingInbox:    cfg.Messaging.BillingInboxURL,
		DefaultBranch:   cfg.Saga.DefaultBranch,
		CommandTimeout:  cfg.Saga.CommandTimeout,
		ApprovalTimeout: cfg.Saga.ApprovalTimeout,
		PaymentTimeout:  cfg.Saga.PaymentTimeout,
		MaxAttempts:     cfg.Saga.MaxAttempts,
		Tick:            cfg.Saga.Tick,
	}, notifier)
}

func RegisterRoutes(app *fiber.App, db *pgxpool.Pool, cfg *config.Config, orchestrator *saga.Orchestrator) {
	app.Use(middlewares.Observability())

	if db == nil {
		registerHealthRoutes(app, nil)
	} else {
		registerHealthRoutes(app, db)
	}

	registerSwagger(app)
	registerUsers(app, db, cfg.JWT.SecretKey)
	registerCustomer(app, db, cfg.JWT.SecretKey)
	registerVehicle(app, db, cfg.JWT.SecretKey)
	registerSupply(app, db, cfg.JWT.SecretKey)
	registerPublicWorkOrder(app, db)
	registerWorkOrder(app, db, cfg.JWT.SecretKey, orchestrator)
	registerWorkshopService(app, db, cfg.JWT.SecretKey)
}

func registerHealthRoutes(app *fiber.App, db databasePinger) {
	app.Get("/ping", func(c fiber.Ctx) error {
		return c.SendString("Pong")
	})

	app.Get("/ready", func(c fiber.Ctx) error {
		if db == nil {
			return c.SendStatus(fiber.StatusServiceUnavailable)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := db.Ping(ctx); err != nil {
			return c.SendStatus(fiber.StatusServiceUnavailable)
		}

		return c.SendStatus(fiber.StatusOK)
	})
}

// POST /auth/register e POST /auth/login sao servidos pela Lambda no
// oficina-mecanica-serverless, roteados pelo API Gateway. O monolito apenas
// valida o token emitido la e mantem a manutencao administrativa de usuarios.
func registerUsers(app *fiber.App, db *pgxpool.Pool, jwtSecretKey string) {
	userRepo := repository.NewUserRepository(db)
	userSvc := service.NewUserService(userRepo)
	userHandler := handler.NewUserHandler(userSvc)

	users := app.Group("/users", middlewares.Auth(jwtSecretKey), middlewares.RequireRoles(middlewares.RoleAdmin))
	users.Get("/", userHandler.GetAll)
	users.Get("/:id", userHandler.GetByID)
	users.Put("/:id", userHandler.Update)
	users.Delete("/:id", userHandler.Delete)
}

func registerCustomer(app *fiber.App, db *pgxpool.Pool, jwtSecretKey string) {
	customerRepo := repository.NewCustomerRepository(db)
	customerSvc := service.NewCustomerService(customerRepo)
	customerHandler := handler.NewCustomerHandler(customerSvc)

	group := app.Group("/customers", middlewares.Auth(jwtSecretKey), middlewares.RequireRoles(middlewares.RoleAdmin, middlewares.RoleEmployee))
	group.Post("/", customerHandler.Create)
	group.Get("/", customerHandler.GetAll)
	group.Get("/:id", customerHandler.GetByID)
	group.Put("/:id", customerHandler.Update)
	group.Delete("/:id", customerHandler.Delete)

}

func registerVehicle(app *fiber.App, db *pgxpool.Pool, jwtSecretKey string) {
	vehicleRepo := repository.NewVehicleRepository(db)
	vehicleSvc := service.NewVehicleService(vehicleRepo)
	vehicleHandler := handler.NewVehicleHandler(vehicleSvc)

	group := app.Group("/vehicles", middlewares.Auth(jwtSecretKey), middlewares.RequireRoles(middlewares.RoleAdmin, middlewares.RoleEmployee))
	group.Post("/", vehicleHandler.Create)
	group.Get("/", vehicleHandler.GetAll)
	group.Get("/:id", vehicleHandler.GetByID)
	group.Put("/:id", vehicleHandler.Update)
	group.Delete("/:id", vehicleHandler.Delete)
}

func registerWorkshopService(app *fiber.App, db *pgxpool.Pool, jwtSecretKey string) {
	wsRepo := repository.NewWorkshopServiceRepository(db)
	wsSvc := service.NewWorkshopServiceManager(wsRepo)
	wsHandler := handler.NewWorkshopServiceHandler(wsSvc)

	group := app.Group("/services", middlewares.Auth(jwtSecretKey), middlewares.RequireRoles(middlewares.RoleAdmin, middlewares.RoleEmployee))
	group.Post("/", wsHandler.Create)
	group.Get("/avg-execution-time", wsHandler.GetAvgExecutionTime)
	group.Get("/", wsHandler.GetAll)
	group.Get("/:id", wsHandler.GetByID)
	group.Put("/:id", wsHandler.Update)
	group.Delete("/:id", wsHandler.Delete)
}

func registerSupply(app *fiber.App, db *pgxpool.Pool, jwtSecretKey string) {
	supplyRepo := repository.NewSupplyRepository(db)
	wosRepo := repository.NewWorkOrderServiceRepository(db)
	supplySvc := service.NewSupplyService(supplyRepo, wosRepo)
	supplyHandler := handler.NewSupplyHandler(supplySvc)

	group := app.Group("/supplies", middlewares.Auth(jwtSecretKey), middlewares.RequireRoles(middlewares.RoleAdmin, middlewares.RoleEmployee))
	group.Get("/pending-purchases", supplyHandler.PendingPurchases)
	group.Post("/", supplyHandler.Create)
	group.Get("/", supplyHandler.GetAll)
	group.Get("/:id", supplyHandler.GetByID)
	group.Put("/:id", supplyHandler.Update)
	group.Delete("/:id", supplyHandler.Delete)
}

func registerWorkOrder(app *fiber.App, db *pgxpool.Pool, jwtSecretKey string, orchestrator *saga.Orchestrator) {
	workOrderRepo := repository.NewWorkOrderRepository(db)
	wosRepo := repository.NewWorkOrderServiceRepository(db)
	vehicleRepo := repository.NewVehicleRepository(db)
	wsRepo := repository.NewWorkshopServiceRepository(db)
	supplyRepo := repository.NewSupplyRepository(db)
	workOrderSvc := service.NewWorkOrderService(workOrderRepo, vehicleRepo, orchestrator)
	creationSvc := service.NewWorkOrderCreationService(workOrderRepo, wosRepo, wsRepo, supplyRepo)
	userRepo := repository.NewUserRepository(db)
	userSvc := service.NewUserService(userRepo)
	workOrderHandler := handler.NewWorkOrderHandler(workOrderSvc, creationSvc, userSvc)
	lifecycleHandler := handler.NewWorkOrderLifecycleHandler(workOrderSvc, orchestrator)

	group := app.Group("/work-orders", middlewares.Auth(jwtSecretKey), middlewares.RequireRoles(middlewares.RoleAdmin, middlewares.RoleEmployee))
	group.Post("/", workOrderHandler.Create)
	group.Get("/", workOrderHandler.GetAll)
	group.Get("/:id", workOrderHandler.GetByID)
	group.Put("/:id", workOrderHandler.Update)
	group.Post("/:id/deliver", lifecycleHandler.Deliver)
	group.Post("/:id/cancel", lifecycleHandler.Cancel)
	group.Get("/:id/history", lifecycleHandler.History)
	group.Get("/:id/saga", lifecycleHandler.Saga)
	group.Post("/:id/services", workOrderHandler.AddServices)
	group.Delete("/:id/services/:wosId", workOrderHandler.RemoveService)
	group.Post("/:id/services/:wosId/supplies", workOrderHandler.AddSupplies)
	group.Delete("/:id/services/:wosId/supplies/:supplyId", workOrderHandler.RemoveSupplyFromService)
}

func registerPublicWorkOrder(app *fiber.App, db *pgxpool.Pool) {
	woRepo := repository.NewWorkOrderRepository(db)
	customerRepo := repository.NewCustomerRepository(db)
	wosRepo := repository.NewWorkOrderServiceRepository(db)
	publicSvc := service.NewPublicWorkOrderService(woRepo, customerRepo, wosRepo)
	publicHandler := handler.NewPublicWorkOrderHandler(publicSvc)

	public := app.Group("/public/work-orders")
	public.Get("/:code", publicHandler.GetByCode)
}
