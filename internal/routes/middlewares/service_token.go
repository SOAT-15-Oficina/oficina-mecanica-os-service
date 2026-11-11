package middlewares

import (
	"crypto/subtle"

	"github.com/gofiber/fiber/v3"
)

const ServiceTokenHeader = "X-Service-Token"

func ServiceToken(expected string) fiber.Handler {
	return func(c fiber.Ctx) error {
		provided := c.Get(ServiceTokenHeader)
		if expected == "" || provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid service token"})
		}
		return c.Next()
	}
}
