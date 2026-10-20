package config

import (
	"net"
	"net/url"
	"os"
	"strings"
	"time"
	_ "time/tzdata"
)

// Config is validated once at startup. DATABASE_URL must never be logged.
type Config struct {
	Environment          string
	HTTPAddr             string
	DatabaseURL          string
	Timezone             string
	CashPolicyStatus     string
	WebDistDir           string
	Origin               string
	PINPepper            string
	ExcelSyncMode        string
	ExcelFileID          string
	ExcelCredentialsFile string
}

// ValidationError identifies a setting without retaining its value.
type ValidationError struct {
	Field   string
	message string
}

func (e *ValidationError) Error() string  { return e.message }
func invalid(field, message string) error { return &ValidationError{Field: field, message: message} }

func Load() (Config, error) { return Parse(os.Getenv) }

func Parse(get func(string) string) (Config, error) {
	value := func(key, fallback string) string {
		if v := get(key); v != "" {
			return v
		}
		return fallback
	}
	c := Config{Environment: value("APP_ENV", "test"), HTTPAddr: value("HTTP_ADDR", "127.0.0.1:8080"), DatabaseURL: get("DATABASE_URL"), Timezone: value("CLUB_TIMEZONE", "Europe/Moscow"), CashPolicyStatus: value("CASH_POLICY_STATUS", "unverified"), WebDistDir: value("WEB_DIST_DIR", "web/dist")}
	c.Origin = value("APP_ORIGIN", "http://127.0.0.1:8080")
	c.PINPepper = get("PIN_PEPPER")
	c.ExcelSyncMode = value("DRIVE_EXCEL_SYNC", "disabled")
	c.ExcelFileID = get("DRIVE_EXCEL_FILE_ID")
	c.ExcelCredentialsFile = get("DRIVE_EXCEL_CREDENTIALS_FILE")
	if c.ExcelSyncMode != "disabled" && c.ExcelSyncMode != "test" {
		return Config{}, invalid("DRIVE_EXCEL_SYNC", "DRIVE_EXCEL_SYNC must be disabled or test")
	}
	if c.ExcelSyncMode == "test" {
		if c.Environment != "test" || len(c.ExcelFileID) < 10 || len(c.ExcelFileID) > 200 || c.ExcelCredentialsFile == "" {
			return Config{}, invalid("DRIVE_EXCEL_SYNC", "excel synchronization requires test mode, file ID and server credential path")
		}
		for _, ch := range c.ExcelFileID {
			if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-", ch) {
				return Config{}, invalid("DRIVE_EXCEL_FILE_ID", "invalid DRIVE_EXCEL_FILE_ID")
			}
		}
	}
	if len(c.PINPepper) < 32 {
		return Config{}, invalid("PIN_PEPPER", "PIN_PEPPER requires at least 32 characters")
	}
	origin, err := url.Parse(c.Origin)
	if err != nil || origin.Host == "" || (origin.Scheme != "http" && origin.Scheme != "https") || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || origin.Path != "" {
		return Config{}, invalid("APP_ORIGIN", "invalid APP_ORIGIN")
	}
	if c.Environment == "production" && !strings.HasPrefix(c.Origin, "https://") {
		return Config{}, invalid("APP_ORIGIN", "production requires HTTPS APP_ORIGIN")
	}
	if c.Environment != "test" && c.Environment != "production" {
		return Config{}, invalid("APP_ENV", "APP_ENV must be test or production")
	}
	if c.DatabaseURL == "" {
		return Config{}, invalid("DATABASE_URL", "DATABASE_URL is required")
	}
	if _, _, err := net.SplitHostPort(c.HTTPAddr); err != nil {
		return Config{}, invalid("HTTP_ADDR", "invalid HTTP_ADDR")
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		return Config{}, invalid("CLUB_TIMEZONE", "invalid CLUB_TIMEZONE")
	}
	// This release contains no verified cash policy. A config flag cannot certify one.
	if c.CashPolicyStatus != "unverified" {
		return Config{}, invalid("CASH_POLICY_STATUS", "only CASH_POLICY_STATUS=unverified is supported by this release")
	}
	return c, nil
}

func (c Config) ProductionBlocked() bool { return c.Environment == "production" }
