package main

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"log/slog"
	"os"
	"pc-club-admin/internal/config"
	"pc-club-admin/internal/storage/postgres"
	"time"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if len(os.Args) != 2 || os.Args[1] != "up" {
		slog.Error("usage: migrate up")
		os.Exit(1)
	}
	if err := run(); err != nil {
		slog.Error("migration failed; check configuration, database and schema compatibility")
		os.Exit(1)
	}
	slog.Info("migrations applied")
}
func run() error {
	c, err := config.Load()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, c.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	return postgres.Migrate(ctx, pool, c.Timezone)
}
