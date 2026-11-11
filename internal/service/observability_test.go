package service

import (
	"testing"
	"time"

	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/domain"
	"github.com/stretchr/testify/assert"
)

func TestStatusEnteredAt_UsesTheStatusColumnAndFallsBackToUpdatedAt(t *testing.T) {
	receivedAt := time.Now().Add(-4 * time.Hour)
	quoteSentAt := time.Now().Add(-3 * time.Hour)
	updatedAt := time.Now().Add(-1 * time.Hour)

	wo := &domain.WorkOrder{
		ReceivedAt:  receivedAt,
		QuoteSentAt: &quoteSentAt,
		UpdatedAt:   updatedAt,
	}

	assert.Equal(t, receivedAt, statusEnteredAt(wo, domain.WorkOrderStatusReceived))
	assert.Equal(t, quoteSentAt, statusEnteredAt(wo, domain.WorkOrderStatusWaitingApproval))
	assert.Equal(t, updatedAt, statusEnteredAt(wo, domain.WorkOrderStatusInDiagnosis))

	assert.Equal(t, updatedAt, statusEnteredAt(wo, domain.WorkOrderStatusApproved))
}

func TestDurationMillis_NeverGoesNegative(t *testing.T) {
	now := time.Now()

	assert.Equal(t, float64(0), durationMillis(now, now.Add(-time.Hour)))
	assert.Equal(t, float64(0), durationMillis(time.Time{}, now))
	assert.InDelta(t, 1000, durationMillis(now, now.Add(time.Second)), 1)
}
