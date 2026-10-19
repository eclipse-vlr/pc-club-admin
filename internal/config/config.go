package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
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
	Origin           string
	PINPepper        string
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
	c.Origin = value("APP_ORIGIN", "http://127.0.0.1:8080")
	c.PINPepper = get("PIN_PEPPER")
	if len(c.PINPepper) < 32 {
		return Config{}, fmt.Errorf("PIN_PEPPER requires at least 32 characters")
	}
	origin, err := url.Parse(c.Origin)
	if err != nil || origin.Host == "" || (origin.Scheme != "http" && origin.Scheme != "https") || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || origin.Path != "" {
		return Config{}, fmt.Errorf("invalid APP_ORIGIN")
	}
	if c.Environment == "production" && !strings.HasPrefix(c.Origin, "https://") {
		return Config{}, fmt.Errorf("production requires HTTPS APP_ORIGIN")
	}
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
