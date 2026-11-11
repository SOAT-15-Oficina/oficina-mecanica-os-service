package messaging

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validEnvelope() Envelope {
	return Envelope{
		MessageID:     uuid.New(),
		Type:          TypeCreateQuote,
		Version:       1,
		SagaID:        uuid.New(),
		WorkOrderID:   uuid.New(),
		CorrelationID: "req-1",
		OccurredAt:    time.Now().UTC(),
		Payload:       json.RawMessage(`{"quote":"x"}`),
	}
}

func TestNewInheritsSagaContextFromCause(t *testing.T) {
	cause := validEnvelope()

	env, err := New(TypeQuoteSent, cause, map[string]int{"total_cents": 4500})
	require.NoError(t, err)

	assert.NotEqual(t, uuid.Nil, env.MessageID)
	assert.NotEqual(t, cause.MessageID, env.MessageID)
	assert.Equal(t, TypeQuoteSent, env.Type)
	assert.Equal(t, 1, env.Version)
	assert.Equal(t, cause.SagaID, env.SagaID)
	assert.Equal(t, cause.WorkOrderID, env.WorkOrderID)
	assert.Equal(t, cause.CorrelationID, env.CorrelationID)
	require.NotNil(t, env.CausationID)
	assert.Equal(t, cause.MessageID, *env.CausationID)
	assert.JSONEq(t, `{"total_cents":4500}`, string(env.Payload))
	assert.Equal(t, cause.WorkOrderID.String(), env.GroupID())
	require.NoError(t, env.Validate())
}

func TestNewRejectsUnmarshalablePayload(t *testing.T) {
	_, err := New(TypeQuoteSent, validEnvelope(), make(chan int))
	assert.Error(t, err)
}

func TestDecodeRoundTrip(t *testing.T) {
	original := validEnvelope()
	raw, err := json.Marshal(original)
	require.NoError(t, err)

	decoded, err := Decode(raw)
	require.NoError(t, err)
	assert.Equal(t, original.MessageID, decoded.MessageID)

	var payload map[string]string
	require.NoError(t, decoded.DecodePayload(&payload))
	assert.Equal(t, "x", payload["quote"])
}

func TestDecodeRejectsMalformedJSON(t *testing.T) {
	_, err := Decode([]byte("{"))
	assert.ErrorIs(t, err, ErrInvalidEnvelope)
}

func TestDecodePayloadRejectsMismatchedShape(t *testing.T) {
	env := validEnvelope()
	var target []int
	assert.Error(t, env.DecodePayload(&target))
}

func TestValidate(t *testing.T) {
	tests := map[string]func(*Envelope){
		"missing message id":   func(e *Envelope) { e.MessageID = uuid.Nil },
		"missing type":         func(e *Envelope) { e.Type = "" },
		"non positive version": func(e *Envelope) { e.Version = 0 },
		"missing saga":         func(e *Envelope) { e.SagaID = uuid.Nil },
		"missing work order":   func(e *Envelope) { e.WorkOrderID = uuid.Nil },
		"missing occurred at":  func(e *Envelope) { e.OccurredAt = time.Time{} },
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			env := validEnvelope()
			mutate(&env)
			assert.ErrorIs(t, env.Validate(), ErrInvalidEnvelope)

			raw, err := json.Marshal(env)
			require.NoError(t, err)
			_, err = Decode(raw)
			assert.ErrorIs(t, err, ErrInvalidEnvelope)
		})
	}
}
