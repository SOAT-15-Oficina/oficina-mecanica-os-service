package handler

import (
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/application"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/service"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

type WorkOrderLifecycleHandler struct {
	svc       service.WorkOrderService
	lifecycle application.WorkOrderLifecycle
}

func NewWorkOrderLifecycleHandler(svc service.WorkOrderService, lifecycle application.WorkOrderLifecycle) *WorkOrderLifecycleHandler {
	return &WorkOrderLifecycleHandler{svc: svc, lifecycle: lifecycle}
}

func (h *WorkOrderLifecycleHandler) respondError(c fiber.Ctx, err error) error {
	if handled, resp := mapErrorResponse(c, err, "work order not found"); handled {
		return resp
	}
	return internalServerError(c)
}

func (h *WorkOrderLifecycleHandler) Deliver(c fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid id"})
	}
	if err := h.lifecycle.Deliver(c.Context(), id); err != nil {
		return h.respondError(c, err)
	}
	wo, err := h.svc.GetByID(c.Context(), id)
	if err != nil {
		return h.respondError(c, err)
	}
	return c.JSON(wo)
}

func (h *WorkOrderLifecycleHandler) Cancel(c fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid id"})
	}
	saga, err := h.lifecycle.Cancel(c.Context(), id)
	if err != nil {
		return h.respondError(c, err)
	}
	return c.Status(fiber.StatusAccepted).JSON(saga)
}

func (h *WorkOrderLifecycleHandler) History(c fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid id"})
	}
	history, err := h.svc.History(c.Context(), id)
	if err != nil {
		return h.respondError(c, err)
	}
	return c.JSON(history)
}

func (h *WorkOrderLifecycleHandler) Saga(c fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid id"})
	}
	saga, err := h.lifecycle.Get(c.Context(), id)
	if err != nil {
		return h.respondError(c, err)
	}
	return c.JSON(saga)
}
