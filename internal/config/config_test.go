package config

import "testing"

func TestConfigurationGates(t *testing.T) {
	tests := []struct {
		name               string
		values             map[string]string
		wantError, blocked bool
	}{
		{"test defaults", map[string]string{"DATABASE_URL": "postgres://localhost/pcclub_test"}, false, false},
		{"database required", nil, true, false},
		{"production unverified", map[string]string{"DATABASE_URL": "postgres://localhost/pcclub_test", "APP_ENV": "production"}, false, true},
		{"cannot certify policy by flag", map[string]string{"DATABASE_URL": "postgres://localhost/pcclub_test", "CASH_POLICY_STATUS": "verified"}, true, false},
		{"unknown environment", map[string]string{"DATABASE_URL": "postgres://localhost/pcclub_test", "APP_ENV": "prod"}, true, false},
		{"invalid timezone", map[string]string{"DATABASE_URL": "postgres://localhost/pcclub_test", "CLUB_TIMEZONE": "invalid/timezone"}, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := Parse(func(k string) string {
				if k == "PIN_PEPPER" {
					return "test-only-pepper-with-at-least-32-characters"
				}
				if k == "APP_ORIGIN" && tt.values["APP_ENV"] == "production" {
					return "https://club.example"
				}
				return tt.values[k]
			})
			if (err != nil) != tt.wantError {
				t.Fatalf("error=%v; want error=%v", err, tt.wantError)
			}
			if err == nil && c.ProductionBlocked() != tt.blocked {
				t.Fatalf("production gate=%v", c.ProductionBlocked())
			}
		})
	}
}

func TestAuthConfigurationBoundary(t *testing.T) {
	for _, tt := range []struct {
		name, pepper, origin, environment string
		valid                             bool
	}{
		{"missing pepper", "", "http://127.0.0.1:8080", "test", false},
		{"short pepper", "short", "http://127.0.0.1:8080", "test", false},
		{"production http", "test-only-pepper-at-least-32-characters", "http://club.example", "production", false},
		{"origin cannot have path", "test-only-pepper-at-least-32-characters", "https://club.example/login", "production", false},
		{"secure origin", "test-only-pepper-at-least-32-characters", "https://club.example", "production", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			values := map[string]string{"DATABASE_URL": "postgres://localhost/pcclub_test", "PIN_PEPPER": tt.pepper, "APP_ORIGIN": tt.origin, "APP_ENV": tt.environment}
			_, err := Parse(func(k string) string { return values[k] })
			if (err == nil) != tt.valid {
				t.Fatalf("valid=%v error=%v", tt.valid, err)
			}
		})
	}
}
