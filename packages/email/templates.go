package email

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
)

//go:embed templates/*.html
var templatesFS embed.FS

var templates = template.Must(template.ParseFS(templatesFS, "templates/*.html"))

type StatusChangeEmailData struct {
	CustomerName        string
	WorkOrderCode       string
	PreviousStatusLabel string
	NewStatusLabel      string
	Message             string
}

func RenderStatusChangeEmail(data StatusChangeEmailData) (string, error) {
	var buf bytes.Buffer
	if err := templates.ExecuteTemplate(&buf, "status-change.html", data); err != nil {
		return "", fmt.Errorf("email: render status-change template: %w", err)
	}
	return buf.String(), nil
}
