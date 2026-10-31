package gateway

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"go.yaml.in/yaml/v3"
)

type Provider struct {
	BaseURL    string `json:"base_url" yaml:"base_url"`
	APIKeyEnv  string `json:"api_key_env" yaml:"api_key_env"`
	Accounting string `json:"accounting" yaml:"accounting"` // openrouter, reported, or token_rates
}

type Route struct {
	Provider            string        `json:"provider" yaml:"provider"`
	Model               string        `json:"model" yaml:"model"`
	InputUSDPerMillion  string        `json:"input_usd_per_million,omitempty" yaml:"input_usd_per_million,omitempty"`
	OutputUSDPerMillion string        `json:"output_usd_per_million,omitempty" yaml:"output_usd_per_million,omitempty"`
	PricingVersion      string        `json:"pricing_version,omitempty" yaml:"pricing_version,omitempty"`
	RequestParams       RequestParams `json:"request_params,omitempty" yaml:"request_params,omitempty"`
}

type ModelAccessGroup struct {
	Models []string `json:"models" yaml:"models" nullable:"false"`
}

var groupIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

func (c Config) AllowsModel(group, model string) bool {
	g, ok := c.ModelAccessGroups[group]
	_, exists := c.Models[model]
	return ok && exists && slices.Contains(g.Models, model)
}

type Config struct {
	ModelAccessGroups       map[string]ModelAccessGroup `json:"model_access_groups" yaml:"model_access_groups"`
	DefaultModelAccessGroup string                      `json:"default_model_access_group" yaml:"default_model_access_group"`
	Listen                  string                      `json:"listen" yaml:"listen"`
	AdminListen             string                      `json:"admin_listen" yaml:"admin_listen"`
	Database                string                      `json:"database" yaml:"database"`
	BackupDirectory         string                      `json:"backup_directory" yaml:"backup_directory"`
	BackupIntervalSeconds   int                         `json:"backup_interval_seconds" yaml:"backup_interval_seconds"`
	AdminTokenEnv           string                      `json:"admin_token_env" yaml:"admin_token_env"`
	AdminUsername           string                      `json:"admin_username,omitempty" yaml:"admin_username,omitempty"`
	AdminPasswordEnv        string                      `json:"admin_password_env,omitempty" yaml:"admin_password_env,omitempty"`
	Providers               map[string]Provider         `json:"providers" yaml:"providers"`
	Models                  map[string]Route            `json:"models" yaml:"models"`
	MaxBodyBytes            int64                       `json:"max_body_bytes" yaml:"max_body_bytes"`
	MaxResponseBytes        int64                       `json:"max_response_bytes" yaml:"max_response_bytes"`
	MaxOutputTokens         int                         `json:"max_output_tokens" yaml:"max_output_tokens"`
	GlobalConcurrency       int                         `json:"global_concurrency" yaml:"global_concurrency"`
	UserConcurrency         int                         `json:"user_concurrency" yaml:"user_concurrency"`
	UserRequestsPerMinute   int                         `json:"user_requests_per_minute" yaml:"user_requests_per_minute"`
	RequestTimeoutSeconds   int                         `json:"request_timeout_seconds" yaml:"request_timeout_seconds"`
	HeaderTimeoutSeconds    int                         `json:"header_timeout_seconds" yaml:"header_timeout_seconds"`
	StreamIdleSeconds       int                         `json:"stream_idle_seconds" yaml:"stream_idle_seconds"`
	ShutdownSeconds         int                         `json:"shutdown_seconds" yaml:"shutdown_seconds"`
	ReconcileMaxAttempts    int                         `json:"reconcile_max_attempts" yaml:"reconcile_max_attempts"`
}

func LoadConfig(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer f.Close()
	c := Config{Listen: ":42080", AdminListen: "127.0.0.1:42081", Database: "data/gateway.db", BackupDirectory: "data/backups", BackupIntervalSeconds: 86400, AdminTokenEnv: "GATEWAY_ADMIN_TOKEN", AdminPasswordEnv: "GATEWAY_ADMIN_PASSWORD", MaxBodyBytes: 4 << 20, MaxResponseBytes: 16 << 20, GlobalConcurrency: 100, UserConcurrency: 4, UserRequestsPerMinute: 60, RequestTimeoutSeconds: 300, HeaderTimeoutSeconds: 60, StreamIdleSeconds: 60, ShutdownSeconds: 30, ReconcileMaxAttempts: 12}
	const maxConfigBytes = 1 << 20
	data, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if err != nil {
		return c, err
	}
	if len(data) > maxConfigBytes {
		return c, errors.New("configuration exceeds 1 MiB")
	}
	// Inspect the node as well: YAML null skips UnmarshalYAML methods, but an
	// explicitly supplied request settings field must be an object.
	var document yaml.Node
	if err = yaml.Unmarshal(data, &document); err != nil {
		return c, err
	}
	if err = validateRequestParamsNodes(&document); err != nil {
		return c, err
	}
	d := yaml.NewDecoder(bytes.NewReader(data))
	d.KnownFields(true)
	if err = d.Decode(&c); err != nil {
		return c, err
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return c, errors.New("configuration must contain one YAML document")
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	if _, ok := c.ModelAccessGroups[c.DefaultModelAccessGroup]; !ok {
		return errors.New("default_model_access_group must reference a defined group")
	}
	for name, group := range c.ModelAccessGroups {
		if !groupIDPattern.MatchString(name) {
			return fmt.Errorf("invalid model access group %q", name)
		}
		if group.Models == nil {
			return fmt.Errorf("group %s requires a models array (may be empty)", name)
		}
		seen := map[string]bool{}
		for _, model := range group.Models {
			if _, ok := c.Models[model]; !ok {
				return fmt.Errorf("group %s references unknown model %q", name, model)
			}
			if seen[model] {
				return fmt.Errorf("group %s repeats model %q", name, model)
			}
			seen[model] = true
		}
	}

	if c.Listen == "" || c.AdminListen == "" || c.Listen == c.AdminListen || c.Database == "" || c.BackupDirectory == "" {
		return errors.New("distinct inference/admin listeners, database, and backup directory are required")
	}
	adminToken := os.Getenv(c.AdminTokenEnv)
	if len(adminToken) < 32 || len(adminToken) > 256 || strings.ContainsAny(adminToken, " \t\r\n") {
		return errors.New("admin credential must contain 32 to 256 characters without spaces, tabs, or line breaks")
	}
	if c.AdminUsername != "" {
		if !validAdminUsername(c.AdminUsername) {
			return errors.New("admin_username must be 1 to 64 characters without surrounding whitespace or control characters")
		}
		if c.AdminPasswordEnv == "" {
			return errors.New("admin_password_env is required when admin_username is set")
		}
		if !validAdminPassword(os.Getenv(c.AdminPasswordEnv)) {
			return errors.New("admin UI password must be 12 to 256 characters without surrounding whitespace or control characters")
		}
	}
	if len(c.Providers) == 0 || len(c.Models) == 0 {
		return errors.New("providers and models are required")
	}
	if c.MaxBodyBytes <= 0 || c.MaxBodyBytes > 64<<20 || c.MaxResponseBytes <= 0 || c.MaxResponseBytes > 64<<20 || c.MaxOutputTokens < 0 || c.GlobalConcurrency <= 0 || c.UserConcurrency <= 0 || c.UserRequestsPerMinute <= 0 {
		return errors.New("invalid size, token, concurrency, or rate limit")
	}
	for _, v := range []int{c.RequestTimeoutSeconds, c.HeaderTimeoutSeconds, c.StreamIdleSeconds, c.ShutdownSeconds, c.ReconcileMaxAttempts, c.BackupIntervalSeconds} {
		if v <= 0 || v > 86400 {
			return errors.New("timeouts, backup interval, and retry count must be between 1 and 86400")
		}
	}
	for name, p := range c.Providers {
		u, err := url.Parse(p.BaseURL)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Scheme != "https" {
			return fmt.Errorf("provider %s requires an HTTPS base URL without userinfo, query or fragment", name)
		}
		if name == "" || os.Getenv(p.APIKeyEnv) == "" {
			return fmt.Errorf("provider %s credential is missing", name)
		}
		if p.Accounting != "openrouter" && p.Accounting != "reported" && p.Accounting != "token_rates" {
			return fmt.Errorf("provider %s has invalid accounting mode", name)
		}
	}
	for name, r := range c.Models {
		if err := r.RequestParams.Validate(); err != nil {
			return fmt.Errorf("model %s request_params: %w", name, err)
		}
		p, ok := c.Providers[r.Provider]
		if !ok || name == "" || r.Model == "" || strings.ContainsAny(name, "\r\n") {
			return fmt.Errorf("invalid model route %s", name)
		}
		if p.Accounting == "token_rates" {
			if r.PricingVersion == "" {
				return fmt.Errorf("model %s requires pricing_version", name)
			}
			if _, err := ParseUSD(r.InputUSDPerMillion); err != nil {
				return fmt.Errorf("model %s input pricing: %w", name, err)
			}
			if _, err := ParseUSD(r.OutputUSDPerMillion); err != nil {
				return fmt.Errorf("model %s output pricing: %w", name, err)
			}
		}
	}
	return nil
}

func seconds(n int) time.Duration { return time.Duration(n) * time.Second }

func validAdminUsername(s string) bool {
	return len(s) >= 1 && len(s) <= 64 && strings.TrimSpace(s) == s && strings.IndexFunc(s, unicode.IsControl) < 0
}

func validAdminPassword(s string) bool {
	return len(s) >= 12 && len(s) <= 256 && strings.TrimSpace(s) == s && strings.IndexFunc(s, unicode.IsControl) < 0
}
