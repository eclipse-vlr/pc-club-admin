package main

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"pc-club-admin/internal/auth"
	"pc-club-admin/internal/config"
	"pc-club-admin/internal/httpapi"
	"pc-club-admin/internal/shifts"
	"pc-club-admin/internal/storage/postgres"
	"syscall"
	"time"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("API stopped; check configuration, database and schema compatibility")
		os.Exit(1)
	}
}
func run(logger *slog.Logger) error {
	c, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := pgxpool.New(ctx, c.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	check := func(ctx context.Context) error { return postgres.CheckSchema(ctx, pool, c.Timezone) }
	startup, cancel := context.WithTimeout(ctx, 5*time.Second)
	err = check(startup)
	cancel()
	if err != nil {
		return err
	}
	if c.ProductionBlocked() {
		logger.Warn("production operations blocked", "reason", "CASH_POLICY_UNVERIFIED")
	}
	access := auth.New(postgres.AuthRepository{Pool: pool}, c.PINPepper)
	work := shifts.New(postgres.ShiftRepository{Pool: pool}, access, c.Timezone, c.ProductionBlocked())
	server := &http.Server{Addr: c.HTTPAddr, Handler: httpapi.NewWithShifts(c, check, logger, access, work), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 16}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	logger.Info("API started", "address", c.HTTPAddr, "environment", c.Environment)
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
			return err
		}
		err := <-done
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
