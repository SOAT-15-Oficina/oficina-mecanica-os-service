package routes

import (
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/docs"
	"github.com/gofiber/fiber/v3"
)

func registerSwagger(app *fiber.App) {
	app.Get("/docs/swagger.yaml", func(c fiber.Ctx) error {
		c.Set("Content-Type", "text/yaml")
		return c.Send(docs.Swagger)
	})

	app.Get("/swagger", func(c fiber.Ctx) error {
		c.Set("Content-Type", "text/html")
		return c.SendString(swaggerHTML)
	})
}

const swaggerHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>Swagger UI - Oficina Mecanica API</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
  <style>html{box-sizing:border-box;overflow-y:scroll}*,*:before,*:after{box-sizing:inherit}body{margin:0;background:#fafafa}</style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    SwaggerUIBundle({
      url: "/docs/swagger.yaml",
      dom_id: "#swagger-ui",
      deepLinking: true,
      presets: [SwaggerUIBundle.presets.apis, SwaggerUIBundle.SwaggerUIStandalonePreset],
      layout: "BaseLayout",
    });
  </script>
</body>
</html>`
