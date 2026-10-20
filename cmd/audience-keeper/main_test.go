package main

import (
	"strings"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	const good = "0123456789abcdef"
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{"defaults", map[string]string{"AK_ADMIN_TOKEN": good}, ""},
		{"missing admin token", map[string]string{}, "AK_ADMIN_TOKEN"},
		{"short admin token", map[string]string{"AK_ADMIN_TOKEN": "short"}, "AK_ADMIN_TOKEN"},
		{"short mcp token", map[string]string{"AK_ADMIN_TOKEN": good, "AK_MCP_TOKEN": "short"}, "AK_MCP_TOKEN"},
		{"same tokens", map[string]string{"AK_ADMIN_TOKEN": good, "AK_MCP_TOKEN": good}, "différent"},
		{"bad public url", map[string]string{"AK_ADMIN_TOKEN": good, "AK_PUBLIC_URL": "quiz.example.org"}, "AK_PUBLIC_URL"},
		{"full", map[string]string{"AK_ADMIN_TOKEN": good, "AK_MCP_TOKEN": good + "-mcp", "AK_PUBLIC_URL": "https://quiz.example.org",
			"AK_ADDR": ":9000", "AK_MCP_SHOW_NAMES": "true", "AK_ALLOWED_ORIGINS": "a.example.org, b.example.org"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := loadConfig(func(k string) string { return tt.env[k] })
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tt.name == "defaults" && (cfg.addr != ":8080" || cfg.mcpToken != "" || cfg.showNames) {
				t.Fatalf("%+v", cfg)
			}
			if tt.name == "full" && (cfg.addr != ":9000" || !cfg.showNames || len(cfg.allowedOrigins) != 2) {
				t.Fatalf("%+v", cfg)
			}
		})
	}
}
