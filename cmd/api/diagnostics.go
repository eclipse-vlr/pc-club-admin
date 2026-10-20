package main

import (
	"errors"
	"log/slog"
	"syscall"

	"pc-club-admin/internal/config"
)

// Keep provider errors, URLs, credentials and OS-specific paths out of logs.
type launchFailure struct{ code, hint string }

func (e *launchFailure) Error() string { return e.code }
func failure(code, hint string) error  { return &launchFailure{code: code, hint: hint} }

func configurationFailure(err error) error {
	hint := "Проверьте локальные настройки приложения. Их значения не выводятся в журнал."
	var invalid *config.ValidationError
	if errors.As(err, &invalid) {
		switch invalid.Field {
		case "PIN_PEPPER":
			hint = "Загрузите прежний PIN_PEPPER из .env. Не генерируйте новый для существующей базы."
		case "DATABASE_URL":
			hint = "Загрузите DATABASE_URL из существующего .env. Файл .env автоматически не читается при go run."
		case "APP_ENV", "HTTP_ADDR", "APP_ORIGIN", "CLUB_TIMEZONE", "CASH_POLICY_STATUS", "DRIVE_EXCEL_SYNC", "DRIVE_EXCEL_FILE_ID":
			hint = "Проверьте настройку " + invalid.Field + " в локальном .env. Production требует проверенной денежной политики."
		}
	}
	return failure("CONFIG_INVALID", hint)
}

func listenFailure(err error) error {
	if errors.Is(err, syscall.EADDRINUSE) {
		return failure("HTTP_PORT_IN_USE", "Порт занят. Остановите прежний API через Ctrl+C в его окне и повторите запуск.")
	}
	return failure("HTTP_BIND_FAILED", "Не удалось открыть HTTP-порт. Проверьте HTTP_ADDR и разрешение на использование порта.")
}

func logFailure(logger *slog.Logger, err error) {
	var report *launchFailure
	if !errors.As(err, &report) {
		report = &launchFailure{code: "API_UNAVAILABLE", hint: "Запуск остановлен. Проверьте локальные настройки и зависимости."}
	}
	logger.Error("API stopped", "code", report.code, "hint", report.hint)
}
