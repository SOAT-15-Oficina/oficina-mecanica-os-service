package email

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRenderStatusChangeEmail_Success(t *testing.T) {
	data := StatusChangeEmailData{
		CustomerName:        "Maria",
		WorkOrderCode:       "OS-202",
		PreviousStatusLabel: "Aprovada",
		NewStatusLabel:      "Em execução",
		Message:             "A execução dos serviços da sua ordem de serviço foi iniciada.",
	}

	body, err := RenderStatusChangeEmail(data)
	assert.NoError(t, err)
	assert.NotEmpty(t, body)
	assert.Contains(t, body, "Maria")
	assert.Contains(t, body, "OS-202")
	assert.Contains(t, body, "Aprovada")
	assert.Contains(t, body, "Em execução")
	assert.Contains(t, body, "Status anterior:")
	assert.Contains(t, body, "Novo status:")
}
