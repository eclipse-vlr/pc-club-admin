package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"pc-club-admin/internal/auth"
	"pc-club-admin/internal/config"
	"pc-club-admin/internal/excelsync"
	"pc-club-admin/internal/httpapi"
	"pc-club-admin/internal/shifts"
	"pc-club-admin/internal/storage/postgres"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logFailure(logger, err)
		os.Exit(1)
	}
}
func run(logger *slog.Logger) error {
	c, err := config.Load()
	if err != nil {
		return configurationFailure(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := pgxpool.New(ctx, c.DatabaseURL)
	if err != nil {
		return failure("DATABASE_CONFIG_INVALID", "Проверьте DATABASE_URL в локальных настройках; значение не выводится в журнал.")
	}
	defer pool.Close()
	check := func(ctx context.Context) error { return postgres.CheckSchema(ctx, pool, c.Timezone) }
	startup, cancel := context.WithTimeout(ctx, 5*time.Second)
	err = pool.Ping(startup)
	cancel()
	if err != nil {
		return failure("DATABASE_UNAVAILABLE", "База недоступна. Проверьте Docker Desktop, контейнер db и настройки подключения.")
	}
	startup, cancel = context.WithTimeout(ctx, 5*time.Second)
	err = check(startup)
	cancel()
	if err != nil {
		return failure("DATABASE_SCHEMA_INVALID", "Проверьте миграции и часовой пояс клуба. Не удаляйте базу для исправления этой ошибки.")
	}
	listener, err := net.Listen("tcp", c.HTTPAddr)
	if err != nil {
		return listenFailure(err)
	}
	defer func() { _ = listener.Close() }()
	if c.ProductionBlocked() {
		logger.Warn("production operations blocked", "reason", "CASH_POLICY_UNVERIFIED")
	}
	access := auth.New(postgres.AuthRepository{Pool: pool}, c.PINPepper)
	work := shifts.New(postgres.ShiftRepository{Pool: pool}, access, c.Timezone, c.ProductionBlocked())
	workerContext, stopWorker := context.WithCancel(ctx)
	var workerDone chan struct{}
	defer func() {
		stopWorker()
		if workerDone != nil {
			<-workerDone
		}
	}()
	if c.ExcelSyncMode == "test" && c.Environment == "test" {
		remote, err := excelsync.NewDriveClient(workerContext, c.ExcelCredentialsFile, c.ExcelFileID)
		if err != nil {
			return failure("EXCEL_CONFIG_INVALID", "Проверьте путь и формат локального ключа Drive. Содержимое ключа не отправляйте в чат.")
		}
		repository := postgres.ExcelRepository{Pool: pool}
		startup, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = repository.PrepareTarget(startup, excelsync.TargetHash(c.ExcelFileID))
		cancel()
		if err != nil {
			remote.Close()
			return failure("EXCEL_QUEUE_UNAVAILABLE", "Не удалось подготовить очередь Excel. Проверьте доступность БД и миграции.")
		}
		workerDone = make(chan struct{})
		go func() {
			defer close(workerDone)
			defer remote.Close()
			excelsync.Run(workerContext, repository, excelsync.TargetHash(c.ExcelFileID), remote.Deliver, logger)
		}()
	}
	server := &http.Server{Addr: c.HTTPAddr, Handler: httpapi.NewWithShifts(c, check, logger, access, work), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 16}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	logger.Info("API started", "address", c.HTTPAddr, "environment", c.Environment)
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return failure("HTTP_SERVER_FAILED", "HTTP-сервер остановился с ошибкой. Проверьте локальные сетевые настройки.")
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
			return failure("HTTP_SHUTDOWN_TIMEOUT", "Не удалось завершить активные запросы за отведённое время.")
		}
		err := <-done
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return failure("HTTP_SERVER_FAILED", "HTTP-сервер остановился с ошибкой.")
	}
}
