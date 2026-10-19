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
			c, err := Parse(func(k string) string { return tt.values[k] })
			if (err != nil) != tt.wantError {
				t.Fatalf("error=%v; want error=%v", err, tt.wantError)
			}
			if err == nil && c.ProductionBlocked() != tt.blocked {
				t.Fatalf("production gate=%v", c.ProductionBlocked())
			}
		})
	}
}
