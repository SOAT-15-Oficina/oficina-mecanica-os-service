package database

import (
	"fmt"

	dbmigrations "github.com/SOAT-15-Oficina/oficina-mecanica-os-service/database"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	_ "github.com/lib/pq"
	"github.com/pressly/goose/v3"
)

func RunMigrations(pool *pgxpool.Pool) error {
	goose.SetBaseFS(dbmigrations.Migrations)

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}

	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()

	if err := goose.Up(db, "migrations"); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}
