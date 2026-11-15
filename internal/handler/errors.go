package handler

import (
	"errors"

	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/application"
	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	pgErrUniqueViolation     = "23505"
	pgErrForeignKeyViolation = "23503"
	pgErrNotNullViolation    = "23502"
	pgErrCheckViolation      = "23514"
)

var uniqueConstraintMessages = map[string]string{
	"users_username_key":                            "nome de usuário já está em uso",
	"customers_document_key":                        "documento já cadastrado",
	"vehicles_license_plate_key":                    "placa já cadastrada",
	"supplies_code_key":                             "código de insumo já cadastrado",
	"idx_services_title_unique":                     "já existe um serviço com esse título",
	"idx_work_order_service_supplies_wos_supply_id": "insumo já adicionado a este serviço",
}

var foreignKeyMessages = map[string]string{
	"fk_vehicles_customer":      "cliente não encontrado",
	"fk_work_orders_customer":   "cliente não encontrado",
	"fk_work_orders_vehicle":    "veículo não encontrado",
	"fk_work_orders_opened_by":  "usuário não encontrado",
	"fk_work_orders_technician": "usuário não encontrado",
	"fk_wos_work_order":         "ordem de serviço não encontrada",
	"fk_wos_service":            "serviço não encontrado",
	"fk_wosi_work_order_s":      "serviço da ordem não encontrado",
	"fk_wosi_supply":            "insumo vinculado a uma OS e não pode ser removido",
	"fk_wosh_work_order":        "serviço da ordem não encontrado",
}

func dbErrResponse(c fiber.Ctx, err error, notFoundMsg string) (bool, error) {
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, application.ErrNotFound) {
		return true, c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": notFoundMsg})
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case pgErrUniqueViolation:
			msg := uniqueConstraintMessages[pgErr.ConstraintName]
			if msg == "" {
				msg = "registro já existe"
			}
			return true, c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": msg})
		case pgErrForeignKeyViolation:
			msg := foreignKeyMessages[pgErr.ConstraintName]
			if msg == "" {
				msg = "registro conflita com um relacionamento existente"
			}
			return true, c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": msg})
		case pgErrNotNullViolation, pgErrCheckViolation:
			return true, c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid resource data"})
		}
	}

	return false, nil
}

func internalServerError(c fiber.Ctx) error {
	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
}
