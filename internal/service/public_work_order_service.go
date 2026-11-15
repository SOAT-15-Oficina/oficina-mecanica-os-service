package service

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/application"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/domain"
)

var ErrWorkOrderNotFound = errors.New("work order not found")

type PublicServiceView struct {
	Title          string                                `json:"title"`
	Status         domain.WorkOrderServiceStatus         `json:"status"`
	ApprovalStatus domain.WorkOrderServiceApprovalStatus `json:"approval_status"`
}

type PublicStatusChange struct {
	Status    domain.WorkOrderStatus `json:"status"`
	ChangedAt time.Time              `json:"changed_at"`
}

type PublicWorkOrderView struct {
	Code                     string                 `json:"code"`
	Status                   domain.WorkOrderStatus `json:"status"`
	TotalEstimatedPriceCents int                    `json:"total_estimated_price_cents"`
	ReceivedAt               time.Time              `json:"received_at"`
	QuoteSentAt              *time.Time             `json:"quote_sent_at,omitempty"`
	ApprovedAt               *time.Time             `json:"approved_at,omitempty"`
	StartedAt                *time.Time             `json:"started_at,omitempty"`
	FinishedAt               *time.Time             `json:"finished_at,omitempty"`
	DeliveredAt              *time.Time             `json:"delivered_at,omitempty"`
	Services                 []PublicServiceView    `json:"services"`
	History                  []PublicStatusChange   `json:"history"`
	PaymentURL               *string                `json:"payment_url,omitempty"`
}

type PublicWorkOrderService interface {
	GetPublicStatus(ctx context.Context, code, document string) (*PublicWorkOrderView, error)
}

type publicWorkOrderService struct {
	woRepo       application.WorkOrderRepository
	customerRepo application.CustomerRepository
	wosRepo      application.WorkOrderServiceRepository
}

func NewPublicWorkOrderService(
	woRepo application.WorkOrderRepository,
	customerRepo application.CustomerRepository,
	wosRepo application.WorkOrderServiceRepository,
) PublicWorkOrderService {
	return &publicWorkOrderService{
		woRepo:       woRepo,
		customerRepo: customerRepo,
		wosRepo:      wosRepo,
	}
}

var onlyDigits = regexp.MustCompile(`\d+`)

func normalizeDocument(doc string) string {
	return strings.Join(onlyDigits.FindAllString(doc, -1), "")
}

func (s *publicWorkOrderService) GetPublicStatus(ctx context.Context, code, document string) (*PublicWorkOrderView, error) {
	wo, err := s.woRepo.FindByCode(ctx, code)
	if err != nil {
		if errors.Is(err, application.ErrNotFound) {
			return nil, ErrWorkOrderNotFound
		}
		return nil, err
	}

	customer, err := s.customerRepo.FindByID(ctx, wo.CustomerID)
	if err != nil {
		return nil, ErrWorkOrderNotFound
	}

	if normalizeDocument(document) != customer.Document {
		return nil, ErrWorkOrderNotFound
	}

	services, err := s.wosRepo.FindByWorkOrderID(ctx, wo.ID)
	if err != nil {
		return nil, err
	}

	svcViews := make([]PublicServiceView, len(services))
	for i, svc := range services {
		svcViews[i] = PublicServiceView{
			Title:          svc.ServiceTitleSnapshot,
			Status:         svc.Status,
			ApprovalStatus: svc.ApprovalStatus,
		}
	}

	changes, err := s.woRepo.FindStatusHistory(ctx, wo.ID)
	if err != nil {
		return nil, err
	}
	history := make([]PublicStatusChange, len(changes))
	for i, change := range changes {
		history[i] = PublicStatusChange{Status: change.To, ChangedAt: change.ChangedAt}
	}

	var paymentURL *string
	if wo.Status == domain.WorkOrderStatusWaitingPayment {
		paymentURL = wo.PaymentURL
	}

	return &PublicWorkOrderView{
		Code:                     wo.Code,
		Status:                   wo.Status,
		TotalEstimatedPriceCents: wo.TotalEstimatedPriceCents,
		ReceivedAt:               wo.ReceivedAt,
		QuoteSentAt:              wo.QuoteSentAt,
		ApprovedAt:               wo.ApprovedAt,
		StartedAt:                wo.StartedAt,
		FinishedAt:               wo.FinishedAt,
		DeliveredAt:              wo.DeliveredAt,
		Services:                 svcViews,
		History:                  history,
		PaymentURL:               paymentURL,
	}, nil
}
