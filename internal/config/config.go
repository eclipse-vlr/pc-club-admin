package config

import (
	"fmt"
	"net"
	"os"
	"time"
	_ "time/tzdata"
)

// Config is validated once at startup. DATABASE_URL must never be logged.
type Config struct {
	Environment      string
	HTTPAddr         string
	DatabaseURL      string
	Timezone         string
	CashPolicyStatus string
	WebDistDir       string
}

func Load() (Config, error) { return Parse(os.Getenv) }

func Parse(get func(string) string) (Config, error) {
	value := func(key, fallback string) string {
		if v := get(key); v != "" {
			return v
		}
		return fallback
	}
	c := Config{Environment: value("APP_ENV", "test"), HTTPAddr: value("HTTP_ADDR", "127.0.0.1:8080"), DatabaseURL: get("DATABASE_URL"), Timezone: value("CLUB_TIMEZONE", "Europe/Moscow"), CashPolicyStatus: value("CASH_POLICY_STATUS", "unverified"), WebDistDir: value("WEB_DIST_DIR", "web/dist")}
	if c.Environment != "test" && c.Environment != "production" {
		return Config{}, fmt.Errorf("APP_ENV must be test or production")
	}
	if c.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	if _, _, err := net.SplitHostPort(c.HTTPAddr); err != nil {
		return Config{}, fmt.Errorf("invalid HTTP_ADDR")
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		return Config{}, fmt.Errorf("invalid CLUB_TIMEZONE")
	}
	// This release contains no verified cash policy. A config flag cannot certify one.
	if c.CashPolicyStatus != "unverified" {
		return Config{}, fmt.Errorf("only CASH_POLICY_STATUS=unverified is supported by this release")
	}
	return c, nil
}

func (c Config) ProductionBlocked() bool { return c.Environment == "production" }
