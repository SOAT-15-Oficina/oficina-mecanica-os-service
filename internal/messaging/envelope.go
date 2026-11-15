package messaging

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var ErrInvalidEnvelope = errors.New("invalid envelope")

type Envelope struct {
	MessageID     uuid.UUID       `json:"message_id"`
	Type          string          `json:"type"`
	Version       int             `json:"version"`
	SagaID        uuid.UUID       `json:"saga_id"`
	WorkOrderID   uuid.UUID       `json:"work_order_id"`
	CorrelationID string          `json:"correlation_id"`
	CausationID   *uuid.UUID      `json:"causation_id,omitempty"`
	OccurredAt    time.Time       `json:"occurred_at"`
	Payload       json.RawMessage `json:"payload"`
}

func New(messageType string, cause Envelope, payload any) (Envelope, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, fmt.Errorf("marshal payload of %s: %w", messageType, err)
	}

	id, err := uuid.NewV7()
	if err != nil {
		return Envelope{}, fmt.Errorf("generate message id: %w", err)
	}

	env := Envelope{
		MessageID:     id,
		Type:          messageType,
		Version:       1,
		SagaID:        cause.SagaID,
		WorkOrderID:   cause.WorkOrderID,
		CorrelationID: cause.CorrelationID,
		OccurredAt:    time.Now().UTC(),
		Payload:       body,
	}
	if cause.MessageID != uuid.Nil {
		causation := cause.MessageID
		env.CausationID = &causation
	}
	return env, nil
}

func Decode(raw []byte) (Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return Envelope{}, fmt.Errorf("%w: %v", ErrInvalidEnvelope, err)
	}
	if err := env.Validate(); err != nil {
		return Envelope{}, err
	}
	return env, nil
}

func (e Envelope) Validate() error {
	switch {
	case e.MessageID == uuid.Nil:
		return fmt.Errorf("%w: message_id is required", ErrInvalidEnvelope)
	case e.Type == "":
		return fmt.Errorf("%w: type is required", ErrInvalidEnvelope)
	case e.Version < 1:
		return fmt.Errorf("%w: version must be positive", ErrInvalidEnvelope)
	case e.SagaID == uuid.Nil:
		return fmt.Errorf("%w: saga_id is required", ErrInvalidEnvelope)
	case e.WorkOrderID == uuid.Nil:
		return fmt.Errorf("%w: work_order_id is required", ErrInvalidEnvelope)
	case e.OccurredAt.IsZero():
		return fmt.Errorf("%w: occurred_at is required", ErrInvalidEnvelope)
	}
	return nil
}

func (e Envelope) GroupID() string { return e.WorkOrderID.String() }

func (e Envelope) DecodePayload(target any) error {
	if err := json.Unmarshal(e.Payload, target); err != nil {
		return fmt.Errorf("decode payload of %s: %w", e.Type, err)
	}
	return nil
}
