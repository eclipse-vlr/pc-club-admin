package main

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"syscall"
	"testing"

	"pc-club-admin/internal/config"
)

func TestLaunchDiagnosticsDoNotExposeCauses(t *testing.T) {
	secret := "postgres://person:secret-password@private-db/club"
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{"occupied port", listenFailure(&net.OpError{Op: "listen", Err: fmt.Errorf("%s: %w", secret, syscall.EADDRINUSE)}), "HTTP_PORT_IN_USE"},
		{"other socket error", listenFailure(errors.New(secret)), "HTTP_BIND_FAILED"},
		{"unknown config", configurationFailure(errors.New(secret)), "CONFIG_INVALID"},
		{"unexpected error", errors.New(secret), "API_UNAVAILABLE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			logFailure(slog.New(slog.NewJSONHandler(&output, nil)), tc.err)
			log := output.String()
			if !strings.Contains(log, tc.code) || strings.Contains(log, "secret-password") || strings.Contains(log, "private-db") {
				t.Errorf("logFailure(%s) = %q, want code %s without cause", tc.name, log, tc.code)
			}
		})
	}
}

func TestConfigurationFailureIdentifiesMissingSetting(t *testing.T) {
	for _, field := range []string{"PIN_PEPPER", "DATABASE_URL"} {
		t.Run(field, func(t *testing.T) {
			values := map[string]string{"PIN_PEPPER": "synthetic-pepper-at-least-32-characters", "DATABASE_URL": "postgres://synthetic/db"}
			delete(values, field)
			_, err := config.Parse(func(key string) string { return values[key] })
			if err == nil {
				t.Fatal("config.Parse(missing setting) = nil, want error")
			}
			var output bytes.Buffer
			logFailure(slog.New(slog.NewJSONHandler(&output, nil)), configurationFailure(err))
			if !strings.Contains(output.String(), field) {
				t.Errorf("configurationFailure(missing %s) = %q, want setting name", field, output.String())
			}
		})
	}
}
