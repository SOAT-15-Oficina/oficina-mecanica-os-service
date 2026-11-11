package service

import (
	"context"
	"log/slog"

	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/application"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/domain"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/observability"
)

type WorkOrderStatusNotifier interface {
	NotifyTransition(ctx context.Context, workOrder *domain.WorkOrder, previousStatus domain.WorkOrderStatus)
}

type workOrderStatusNotifier struct {
	custRepo     application.CustomerRepository
	statusSender application.StatusChangeSender
}

func NewWorkOrderStatusNotifier(
	custRepo application.CustomerRepository,
	statusSender application.StatusChangeSender,
) WorkOrderStatusNotifier {
	return &workOrderStatusNotifier{
		custRepo:     custRepo,
		statusSender: statusSender,
	}
}

func (n *workOrderStatusNotifier) NotifyTransition(
	ctx context.Context,
	workOrder *domain.WorkOrder,
	previousStatus domain.WorkOrderStatus,
) {
	if workOrder == nil {
		return
	}

	newStatus := workOrder.Status

	logger := observability.FromContext(ctx).With(
		slog.String(observability.KeyWorkOrderID, workOrder.ID.String()),
		slog.String(observability.KeyWorkOrderCode, workOrder.Code),
		slog.String(observability.KeyFrom, string(previousStatus)),
		slog.String(observability.KeyTo, string(newStatus)))

	if newStatus == domain.WorkOrderStatusWaitingApproval {
		return
	}

	if n.statusSender == nil {
		logger.WarnContext(ctx, "status change sender is not configured")
		return
	}

	customer, err := n.custRepo.FindByID(ctx, workOrder.CustomerID)
	if err != nil {
		logger.LogAttrs(ctx, slog.LevelError, "find customer for status notification",
			observability.Integration(observability.IntegrationRDS),
			observability.Err(err))
		return
	}
	if customer.Email == "" {
		logger.WarnContext(ctx, "customer has no email, status notification skipped")
		return
	}

	if err := n.statusSender.SendStatusChange(ctx, application.StatusChangeNotification{
		CustomerEmail:       customer.Email,
		CustomerName:        customer.Name,
		WorkOrderCode:       workOrder.Code,
		PreviousStatusLabel: domain.WorkOrderStatusLabel(previousStatus),
		NewStatusLabel:      domain.WorkOrderStatusLabel(newStatus),
		Message:             statusChangeMessage(previousStatus, newStatus),
	}); err != nil {
		logger.LogAttrs(ctx, slog.LevelError, "status change email failed",
			observability.Integration(observability.IntegrationSES),
			observability.Err(err))
	}
}

func statusChangeMessage(previousStatus, newStatus domain.WorkOrderStatus) string {
	switch newStatus {
	case domain.WorkOrderStatusInDiagnosis:
		return "Sua ordem de serviço entrou em diagnóstico. Em breve nossa equipe concluirá a avaliação do veículo."
	case domain.WorkOrderStatusWaitingPayment:
		return "Seu orçamento foi aprovado. Você receberá por e-mail o link para pagamento."
	case domain.WorkOrderStatusApproved:
		return "Pagamento confirmado. Em seguida iniciaremos a execução dos serviços autorizados."
	case domain.WorkOrderStatusCanceled:
		if previousStatus == domain.WorkOrderStatusWaitingApproval {
			return "Seu orçamento foi recusado e a ordem de serviço foi cancelada."
		}
		return "Sua ordem de serviço foi cancelada."
	case domain.WorkOrderStatusInProgress:
		return "A execução dos serviços da sua ordem de serviço foi iniciada."
	case domain.WorkOrderStatusFinished:
		return "Os serviços da sua ordem de serviço foram finalizados."
	case domain.WorkOrderStatusDelivered:
		return "Sua ordem de serviço foi entregue. Obrigado por confiar em nossa oficina."
	default:
		return "O status da sua ordem de serviço foi atualizado."
	}
}
