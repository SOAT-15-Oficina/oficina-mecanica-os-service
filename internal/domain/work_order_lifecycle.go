package domain

import (
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
)

var ErrInvalidStatusTransition = errors.New("invalid status transition")

var workOrderTransitions = map[WorkOrderStatus][]WorkOrderStatus{
	WorkOrderStatusReceived:        {WorkOrderStatusInDiagnosis, WorkOrderStatusCanceled},
	WorkOrderStatusInDiagnosis:     {WorkOrderStatusWaitingApproval, WorkOrderStatusCanceled},
	WorkOrderStatusWaitingApproval: {WorkOrderStatusWaitingPayment, WorkOrderStatusCanceled},
	WorkOrderStatusWaitingPayment:  {WorkOrderStatusApproved, WorkOrderStatusCanceled},
	WorkOrderStatusApproved:        {WorkOrderStatusInProgress, WorkOrderStatusCanceled},
	WorkOrderStatusInProgress:      {WorkOrderStatusFinished},
	WorkOrderStatusFinished:        {WorkOrderStatusDelivered},
}

func CanTransition(from, to WorkOrderStatus) bool {
	return slices.Contains(workOrderTransitions[from], to)
}

type WorkOrderStatusChange struct {
	ID          uuid.UUID        `json:"id"`
	WorkOrderID uuid.UUID        `json:"work_order_id"`
	From        *WorkOrderStatus `json:"from,omitempty"`
	To          WorkOrderStatus  `json:"to"`
	Cause       string           `json:"cause"`
	MessageID   *uuid.UUID       `json:"message_id,omitempty"`
	ChangedAt   time.Time        `json:"changed_at"`
}

type SagaStatus string

const (
	SagaStarted      SagaStatus = "STARTED"
	SagaCompensating SagaStatus = "COMPENSATING"
	SagaCompleted    SagaStatus = "COMPLETED"
	SagaCompensated  SagaStatus = "COMPENSATED"
	SagaFailed       SagaStatus = "FAILED"
)

type SagaCommand struct {
	Destination string          `json:"destination"`
	Envelope    json.RawMessage `json:"envelope"`
}

type SagaInstance struct {
	ID                   uuid.UUID    `json:"id"`
	WorkOrderID          uuid.UUID    `json:"work_order_id"`
	Status               SagaStatus   `json:"status"`
	CurrentStep          string       `json:"current_step"`
	CompletedSteps       []string     `json:"completed_steps"`
	PendingCompensations []string     `json:"pending_compensations"`
	DeadlineAt           *time.Time   `json:"deadline_at,omitempty"`
	Attempts             int          `json:"attempts"`
	LastCommand          *SagaCommand `json:"last_command,omitempty"`
	QuoteID              *uuid.UUID   `json:"quote_id,omitempty"`
	FailureReason        *string      `json:"failure_reason,omitempty"`
	Version              int          `json:"version"`
	CreatedAt            time.Time    `json:"created_at"`
	UpdatedAt            time.Time    `json:"updated_at"`
}

func (s *SagaInstance) HasCompleted(step string) bool {
	return slices.Contains(s.CompletedSteps, step)
}

func (s *SagaInstance) Complete(step string) {
	if !s.HasCompleted(step) {
		s.CompletedSteps = append(s.CompletedSteps, step)
	}
}
