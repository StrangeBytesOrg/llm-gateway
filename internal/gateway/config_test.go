package gateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigValidation(t *testing.T) {
	t.Setenv("GATEWAY_ADMIN_TOKEN", strings.Repeat("a", 40))
	t.Setenv("OPENROUTER_API_KEY", "test")
	data, err := os.ReadFile("../../config.example.json")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	c.Providers["openrouter"] = Provider{BaseURL: "http://insecure.example", APIKeyEnv: "OPENROUTER_API_KEY", Accounting: "openrouter"}
	if c.Validate() == nil {
		t.Fatal("accepted insecure upstream")
	}
	if err = os.WriteFile(path, []byte(`{"unknown_setting":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadConfig(path); err == nil {
		t.Fatal("unknown config accepted")
	}
}

func TestConfigRejectsUnusableAdminToken(t *testing.T) {
	t.Setenv("GATEWAY_ADMIN_TOKEN", "this is a bad secret but it is a secret")
	t.Setenv("OPENROUTER_API_KEY", "test")
	if _, err := LoadConfig("../../config.example.json"); err == nil {
		t.Fatal("accepted an admin token containing spaces that cannot be used as a bearer credential")
	}
}
