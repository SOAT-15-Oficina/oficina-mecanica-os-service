package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/application"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/domain"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/observability"
	"github.com/jackc/pgx/v5"
)

var ErrInvalidCredentials = errors.New("invalid credentials")

type CredentialService interface {
	Verify(ctx context.Context, username, password string) (*domain.User, error)
	Register(ctx context.Context, username, password string, role domain.UserRole) (*domain.User, error)
}

type credentialService struct {
	repo application.UserRepository
}

func NewCredentialService(repo application.UserRepository) CredentialService {
	return &credentialService{repo: repo}
}

func (s *credentialService) Verify(ctx context.Context, username, password string) (*domain.User, error) {
	if strings.TrimSpace(username) == "" || password == "" {
		return nil, ErrInvalidCredentials
	}
	user, err := s.repo.FindByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, application.ErrNotFound) {
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	if err := VerifyPassword(password, user.PasswordHash); err != nil {
		observability.FromContext(ctx).LogAttrs(ctx, slog.LevelWarn, "credential verification failed",
			slog.String(observability.KeyUser, username))
		return nil, ErrInvalidCredentials
	}
	return user, nil
}

func (s *credentialService) Register(ctx context.Context, username, password string, role domain.UserRole) (*domain.User, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, application.NewValidationError("username is required")
	}
	if password == "" {
		return nil, application.NewValidationError("password is required")
	}
	if role != domain.UserRoleAdmin && role != domain.UserRoleEmployee {
		return nil, application.NewValidationError("invalid role: must be 'admin' or 'employee'")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	return s.repo.Create(ctx, &domain.User{Username: username, PasswordHash: hash, Role: role})
}
