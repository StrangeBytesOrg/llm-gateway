package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"
)

type Provider struct {
	BaseURL    string `json:"base_url"`
	APIKeyEnv  string `json:"api_key_env"`
	Accounting string `json:"accounting"` // openrouter, reported, or token_rates
}

type Route struct {
	Provider            string `json:"provider"`
	Model               string `json:"model"`
	InputUSDPerMillion  string `json:"input_usd_per_million,omitempty"`
	OutputUSDPerMillion string `json:"output_usd_per_million,omitempty"`
	PricingVersion      string `json:"pricing_version,omitempty"`
}

type Config struct {
	Listen                string              `json:"listen"`
	AdminListen           string              `json:"admin_listen"`
	Database              string              `json:"database"`
	BackupDirectory       string              `json:"backup_directory"`
	BackupIntervalSeconds int                 `json:"backup_interval_seconds"`
	AdminTokenEnv         string              `json:"admin_token_env"`
	Providers             map[string]Provider `json:"providers"`
	Models                map[string]Route    `json:"models"`
	MaxBodyBytes          int64               `json:"max_body_bytes"`
	MaxResponseBytes      int64               `json:"max_response_bytes"`
	MaxOutputTokens       int                 `json:"max_output_tokens"`
	GlobalConcurrency     int                 `json:"global_concurrency"`
	UserConcurrency       int                 `json:"user_concurrency"`
	UserRequestsPerMinute int                 `json:"user_requests_per_minute"`
	RequestTimeoutSeconds int                 `json:"request_timeout_seconds"`
	HeaderTimeoutSeconds  int                 `json:"header_timeout_seconds"`
	StreamIdleSeconds     int                 `json:"stream_idle_seconds"`
	ShutdownSeconds       int                 `json:"shutdown_seconds"`
	ReconcileMaxAttempts  int                 `json:"reconcile_max_attempts"`
}

func LoadConfig(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer f.Close()
	c := Config{Listen: ":8080", AdminListen: "127.0.0.1:8081", Database: "data/gateway.db", BackupDirectory: "data/backups", BackupIntervalSeconds: 86400, AdminTokenEnv: "GATEWAY_ADMIN_TOKEN", MaxBodyBytes: 4 << 20, MaxResponseBytes: 16 << 20, MaxOutputTokens: 4096, GlobalConcurrency: 100, UserConcurrency: 4, UserRequestsPerMinute: 60, RequestTimeoutSeconds: 300, HeaderTimeoutSeconds: 60, StreamIdleSeconds: 60, ShutdownSeconds: 30, ReconcileMaxAttempts: 12}
	d := json.NewDecoder(io.LimitReader(f, 1<<20))
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return c, err
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return c, errors.New("configuration must contain one JSON object")
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	if c.Listen == "" || c.AdminListen == "" || c.Listen == c.AdminListen || c.Database == "" || c.BackupDirectory == "" {
		return errors.New("distinct inference/admin listeners, database, and backup directory are required")
	}
	adminToken := os.Getenv(c.AdminTokenEnv)
	if len(adminToken) < 32 || len(adminToken) > 256 || strings.ContainsAny(adminToken, " \t\r\n") {
		return errors.New("admin credential must contain 32 to 256 characters without spaces, tabs, or line breaks")
	}
	if len(c.Providers) == 0 || len(c.Models) == 0 {
		return errors.New("providers and models are required")
	}
	if c.MaxBodyBytes <= 0 || c.MaxBodyBytes > 64<<20 || c.MaxResponseBytes <= 0 || c.MaxResponseBytes > 64<<20 || c.MaxOutputTokens <= 0 || c.GlobalConcurrency <= 0 || c.UserConcurrency <= 0 || c.UserRequestsPerMinute <= 0 {
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
