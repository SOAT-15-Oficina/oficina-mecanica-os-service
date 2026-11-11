package email

import (
	"context"
	"fmt"

	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/application"
)

type WorkOrderNotificationSender struct {
	provider Provider
}

func NewWorkOrderNotificationSender(provider Provider) *WorkOrderNotificationSender {
	return &WorkOrderNotificationSender{provider: provider}
}

func (s *WorkOrderNotificationSender) SendStatusChange(ctx context.Context, notification application.StatusChangeNotification) error {
	if s == nil || s.provider == nil {
		return nil
	}

	body, err := RenderStatusChangeEmail(StatusChangeEmailData{
		CustomerName:        notification.CustomerName,
		WorkOrderCode:       notification.WorkOrderCode,
		PreviousStatusLabel: notification.PreviousStatusLabel,
		NewStatusLabel:      notification.NewStatusLabel,
		Message:             notification.Message,
	})
	if err != nil {
		return err
	}

	return s.provider.Send(ctx, Message{
		To:      []string{notification.CustomerEmail},
		Subject: fmt.Sprintf("Atualização da OS %s - %s", notification.WorkOrderCode, notification.NewStatusLabel),
		Body:    body,
		HTML:    true,
	})
}
