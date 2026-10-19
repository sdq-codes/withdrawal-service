package config

import "testing"

func TestValidateForServeRejectsDefaultDemoTokenOutsideDevelopment(t *testing.T) {
	tests := []struct {
		name    string
		env     string
		token   string
		wantErr bool
	}{
		{"development with default token", "development", DefaultDemoBearerToken, false},
		{"production with default token", "production", DefaultDemoBearerToken, true},
		{"production with explicit token", "production", "a-long-random-token", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Config{Env: tt.env, Demo: Demo{BearerToken: tt.token}}
			if err := c.ValidateForServe(); (err != nil) != tt.wantErr {
				t.Fatalf("ValidateForServe() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
