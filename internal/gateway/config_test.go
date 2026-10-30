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
	data, err := os.ReadFile("../../config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
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
	if err = os.WriteFile(path, []byte("unknown_setting: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadConfig(path); err == nil {
		t.Fatal("unknown config accepted")
	}
}

func TestYAMLConfigDefaultsAndPricing(t *testing.T) {
	t.Setenv("GATEWAY_ADMIN_TOKEN", strings.Repeat("a", 40))
	t.Setenv("TEST_PROVIDER_KEY", "test")
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := `# A minimal configuration with comments and decimal strings.
providers:
  test:
    base_url: https://example.invalid/v1
    api_key_env: TEST_PROVIDER_KEY
    accounting: token_rates
models:
  test.model:
    provider: test
    model: upstream/model
    input_usd_per_million: "0.123456789123"
    output_usd_per_million: "2.50"
    pricing_version: "2026-10"
model_access_groups:
  free:
    models: [test.model] # Gateway aliases
  none:
    models: []
default_model_access_group: free
`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != ":42080" || c.MaxBodyBytes != 4<<20 || c.MaxOutputTokens != 0 || c.UserConcurrency != 4 || c.RequestTimeoutSeconds != 300 {
		t.Fatalf("defaults were not preserved: %+v", c)
	}
	if c.Models["test.model"].InputUSDPerMillion != "0.123456789123" || c.Models["test.model"].OutputUSDPerMillion != "2.50" {
		t.Fatal("decimal price strings were changed")
	}
	if c.ModelAccessGroups["none"].Models == nil || c.AllowsModel("none", "test.model") {
		t.Fatal("empty model access group was not preserved")
	}
}

func TestYAMLConfigRejectsInvalidDocuments(t *testing.T) {
	t.Setenv("GATEWAY_ADMIN_TOKEN", strings.Repeat("a", 40))
	t.Setenv("OPENROUTER_API_KEY", "test")
	data, err := os.ReadFile("../../config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	valid := string(data)
	cases := map[string]struct{ data, message string }{
		"unknown root field":         {valid + "typo: 1\n", "field typo"},
		"unknown nested field":       {strings.Replace(valid, "accounting: openrouter", "accountng: openrouter", 1), "field accountng"},
		"duplicate key":              {valid + "listen: ':9090'\n", "already defined"},
		"duplicate nested key":       {strings.Replace(valid, "accounting: openrouter", "accounting: openrouter\n    accounting: reported", 1), "already defined"},
		"multiple documents":         {valid + "---\n{}\n", "one YAML document"},
		"empty extra document":       {valid + "---\n", "one YAML document"},
		"oversized trailing comment": {valid + "#" + strings.Repeat("x", 1<<20), "exceeds 1 MiB"},
		"wrong field type":           {strings.Replace(valid, "global_concurrency: 100", "global_concurrency: many", 1), "cannot unmarshal"},
		"empty":                      {"", "EOF"},
		"non mapping":                {"- item\n", "cannot unmarshal"},
		"malformed":                  {"models: [\n", ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tc.data), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("expected error containing %q, got %v", tc.message, err)
			}
		})
	}
}

func TestConfigRejectsUnusableAdminToken(t *testing.T) {
	t.Setenv("GATEWAY_ADMIN_TOKEN", "this is a bad secret but it is a secret")
	t.Setenv("OPENROUTER_API_KEY", "test")
	if _, err := LoadConfig("../../config.example.yaml"); err == nil {
		t.Fatal("accepted an admin token containing spaces that cannot be used as a bearer credential")
	}
}

func TestConfigOutputTokenLimits(t *testing.T) {
	t.Setenv("GATEWAY_ADMIN_TOKEN", strings.Repeat("a", 40))
	t.Setenv("OPENROUTER_API_KEY", "test")
	data, err := os.ReadFile("../../config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, setting string
		want          int
		wantError     bool
	}{
		{"omitted", "", 0, false},
		{"zero", "max_output_tokens: 0\n", 0, false},
		{"positive", "max_output_tokens: 32768\n", 32768, false},
		{"negative", "max_output_tokens: -1\n", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			content := strings.Replace(string(data), "max_output_tokens: 0\n", tc.setting, 1)
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			c, err := LoadConfig(path)
			if tc.wantError {
				if err == nil {
					t.Fatal("accepted negative output cap")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.MaxOutputTokens != tc.want {
				t.Fatalf("got %d, want %d", c.MaxOutputTokens, tc.want)
			}
		})
	}
}
