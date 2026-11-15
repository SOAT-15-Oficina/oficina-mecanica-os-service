package messaging

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClientsHonoursEndpoint(t *testing.T) {
	t.Setenv("AWS_ENDPOINT_URL", "")
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")

	q, s, err := NewClients(context.Background(), "sa-east-1", "http://localstack:4566")
	require.NoError(t, err)
	assert.Equal(t, "http://localstack:4566", aws.ToString(q.Options().BaseEndpoint))
	assert.Equal(t, "http://localstack:4566", aws.ToString(s.Options().BaseEndpoint))

	q, s, err = NewClients(context.Background(), "sa-east-1", "")
	require.NoError(t, err)
	assert.Nil(t, q.Options().BaseEndpoint)
	assert.Nil(t, s.Options().BaseEndpoint)
}
