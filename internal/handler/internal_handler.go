package handler

import (
	"errors"

	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/domain"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/service"
	"github.com/gofiber/fiber/v3"
)

type InternalHandler struct {
	svc service.CredentialService
}

func NewInternalHandler(svc service.CredentialService) *InternalHandler {
	return &InternalHandler{svc: svc}
}

type credentialRequest struct {
	Username string          `json:"username"`
	Password string          `json:"password"`
	Role     domain.UserRole `json:"role"`
}

func (h *InternalHandler) VerifyCredentials(c fiber.Ctx) error {
	var req credentialRequest
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	user, err := h.svc.Verify(c.Context(), req.Username, req.Password)
	if err != nil {
		if errors.Is(err, service.ErrInvalidCredentials) {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid credentials"})
		}
		return internalServerError(c)
	}
	return c.JSON(user)
}

func (h *InternalHandler) CreateUser(c fiber.Ctx) error {
	var req credentialRequest
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	user, err := h.svc.Register(c.Context(), req.Username, req.Password, req.Role)
	if err != nil {
		if handled, resp := mapErrorResponse(c, err, "user not found"); handled {
			return resp
		}
		return internalServerError(c)
	}
	return c.Status(fiber.StatusCreated).JSON(user)
}
