package middlewares

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceToken(t *testing.T) {
	cases := []struct {
		name     string
		expected string
		provided string
		status   int
	}{
		{"valid token", "secret", "secret", fiber.StatusOK},
		{"wrong token", "secret", "other", fiber.StatusUnauthorized},
		{"missing token", "secret", "", fiber.StatusUnauthorized},
		{"token not configured", "", "", fiber.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := fiber.New()
			app.Post("/internal", ServiceToken(tc.expected), func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) })
			req := httptest.NewRequest(http.MethodPost, "/internal", nil)
			if tc.provided != "" {
				req.Header.Set(ServiceTokenHeader, tc.provided)
			}
			resp, err := app.Test(req)
			require.NoError(t, err)
			assert.Equal(t, tc.status, resp.StatusCode)
		})
	}
}
