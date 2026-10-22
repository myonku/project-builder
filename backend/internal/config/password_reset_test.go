package config

import "testing"

func TestPasswordResetConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Config)
	}{
		{"ttl", func(c *Config) { c.Security.PasswordResetTokenTTLSeconds = 0 }},
		{"cooldown", func(c *Config) { c.Security.PasswordResetCooldownSeconds = 0 }},
		{"window", func(c *Config) { c.Security.PasswordResetWindowSeconds = -1 }},
		{"quota", func(c *Config) { c.Security.PasswordResetMaxRequests = 0 }},
		{"ip", func(c *Config) { c.Security.PasswordResetIPRequestsPerHour = -1 }},
		{"url", func(c *Config) { c.Mail.Enabled = true; c.Mail.PasswordResetURLTemplate = "javascript:{token}" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaultConfig()
			tc.change(&cfg)
			if cfg.validate() == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
}

func TestMailpitDevelopmentConfigUsesPlainSMTP(t *testing.T) {
	clearConfigEnv(t)
	cfg, err := Load("../../docker/config.test.toml", "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mail.UseTLS || cfg.Mail.ImplicitTLS {
		t.Fatal("Mailpit config inherited provider TLS defaults")
	}
}
