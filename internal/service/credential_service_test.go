package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/application"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestHashPasswordUsesLambdaArgon2idFormat(t *testing.T) {
	hash, err := HashPassword("s3cret")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=1,p=4$"))
	assert.NoError(t, VerifyPassword("s3cret", hash))
	assert.ErrorIs(t, VerifyPassword("wrong", hash), ErrInvalidCredentials)
}

func TestVerifyPasswordRejectsMalformedHashes(t *testing.T) {
	for _, encoded := range []string{
		"plain",
		"$bcrypt$v=19$m=65536,t=1,p=4$c2FsdA$aGFzaA",
		"$argon2id$v=x$m=65536,t=1,p=4$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=x$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=65536,t=1,p=4$!!$aGFzaA",
		"$argon2id$v=19$m=65536,t=1,p=4$c2FsdA$!!",
	} {
		assert.Error(t, VerifyPassword("x", encoded), encoded)
	}
}

func TestCredentialServiceVerify(t *testing.T) {
	repo := new(mockUserRepo)
	svc := NewCredentialService(repo)
	ctx := context.Background()
	hash, err := HashPassword("s3cret")
	require.NoError(t, err)
	user := &domain.User{ID: uuid.New(), Username: "ana", PasswordHash: hash, Role: domain.UserRoleAdmin}

	repo.On("FindByUsername", ctx, "ana").Return(user, nil)
	repo.On("FindByUsername", ctx, "ghost").Return(nil, pgx.ErrNoRows)
	repo.On("FindByUsername", ctx, "broken").Return(nil, errors.New("db down"))

	got, err := svc.Verify(ctx, "ana", "s3cret")
	require.NoError(t, err)
	assert.Equal(t, user.ID, got.ID)

	_, err = svc.Verify(ctx, "ana", "wrong")
	assert.ErrorIs(t, err, ErrInvalidCredentials)
	_, err = svc.Verify(ctx, "ghost", "x")
	assert.ErrorIs(t, err, ErrInvalidCredentials)
	_, err = svc.Verify(ctx, "", "x")
	assert.ErrorIs(t, err, ErrInvalidCredentials)
	_, err = svc.Verify(ctx, "broken", "x")
	assert.EqualError(t, err, "db down")
}

func TestCredentialServiceRegister(t *testing.T) {
	repo := new(mockUserRepo)
	svc := NewCredentialService(repo)
	ctx := context.Background()

	repo.On("Create", ctx, mock.AnythingOfType("*domain.User")).Return(&domain.User{ID: uuid.New(), Username: "ana", Role: domain.UserRoleEmployee}, nil)

	user, err := svc.Register(ctx, " ana ", "s3cret", domain.UserRoleEmployee)
	require.NoError(t, err)
	assert.Equal(t, "ana", user.Username)
	created := repo.Calls[0].Arguments.Get(1).(*domain.User)
	assert.Equal(t, "ana", created.Username)
	assert.NoError(t, VerifyPassword("s3cret", created.PasswordHash))

	var validation *application.ValidationError
	_, err = svc.Register(ctx, "", "x", domain.UserRoleAdmin)
	assert.ErrorAs(t, err, &validation)
	_, err = svc.Register(ctx, "ana", "", domain.UserRoleAdmin)
	assert.ErrorAs(t, err, &validation)
	_, err = svc.Register(ctx, "ana", "x", "root")
	assert.ErrorAs(t, err, &validation)
}
