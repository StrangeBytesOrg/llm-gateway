package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (g *Gateway) RunReconciler(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			g.Reconcile(ctx)
		}
	}
}
func (g *Gateway) Reconcile(ctx context.Context) {
	batch, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := g.Store.RecoverStale(batch, time.Now().Add(-seconds(g.Config.RequestTimeoutSeconds+30)).Unix()); err != nil {
		g.Metrics.AccountingErrors.Add(1)
		return
	}
	requests, err := g.Store.Pending(batch, 25)
	if err != nil {
		g.Metrics.AccountingErrors.Add(1)
		g.Log.Error("pending accounting lookup failed")
		return
	}
	for _, r := range requests {
		if batch.Err() != nil {
			return
		}
		err = g.reconcileOne(batch, r)
		if err != nil {
			if e := g.Store.Retry(batch, r, g.Config.ReconcileMaxAttempts); e != nil {
				g.Metrics.AccountingErrors.Add(1)
			}
			if r.Attempts+1 >= g.Config.ReconcileMaxAttempts {
				g.Log.Error("request accounting unresolved", "request_id", r.ID, "provider", r.Provider)
			}
		}
	}
}
func (g *Gateway) reconcileOne(ctx context.Context, r Request) error {
	p := r.ProviderConfig
	if amount, raw, err := costFromUsage(r.Usage, p.Accounting, r.Route); err == nil {
		source := "persisted_usage"
		if p.Accounting == "token_rates" {
			source = "token_rates:" + r.Route.PricingVersion
		}
		return g.Store.Settle(ctx, r.ID, amount, raw, r.Usage, source, "", false)
	}
	if p.Accounting != "openrouter" || r.GenerationID == "" {
		return errors.New("generation lookup unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(p.BaseURL, "/")+"/generation?id="+url.QueryEscape(r.GenerationID), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+os.Getenv(p.APIKeyEnv))
	resp, err := g.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.New("generation lookup failed")
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
	if err != nil || len(b) > 1<<20 {
		return errors.New("invalid generation response")
	}
	var payload struct {
		Data struct {
			ID             string      `json:"id"`
			Cost           json.Number `json:"total_cost"`
			Input          *int64      `json:"native_tokens_prompt"`
			Output         *int64      `json:"native_tokens_completion"`
			GenerationTime *int64      `json:"generation_time"`
			FinishReason   string      `json:"finish_reason"`
		} `json:"data"`
	}
	if err = json.Unmarshal(b, &payload); err != nil {
		return err
	}
	if payload.Data.ID != r.GenerationID {
		return errors.New("generation ID mismatch")
	}
	if payload.Data.GenerationTime == nil || payload.Data.FinishReason == "" {
		return errors.New("generation is not finalized")
	}
	amount, err := ParseUSD(payload.Data.Cost.String())
	if err != nil {
		return err
	}
	usage, _ := json.Marshal(map[string]any{"prompt_tokens": payload.Data.Input, "completion_tokens": payload.Data.Output, "cost": payload.Data.Cost})
	return g.Store.Settle(ctx, r.ID, amount, payload.Data.Cost.String(), usage, "generation_lookup", "", false)
}

func (g *Gateway) CreateBackup(ctx context.Context) (string, error) {
	dir := g.Config.BackupDirectory
	if dir == "" {
		return "", errors.New("backup directory is not configured")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	name := time.Now().UTC().Format("20060102T150405Z") + "-" + ID("") + ".db"
	dest := filepath.Join(dir, name)
	if err := g.Store.Backup(ctx, dest); err != nil {
		return "", err
	}
	if err := VerifyBackup(ctx, dest); err != nil {
		return "", err
	}
	g.Log.Info("database backup verified", "filename", name)
	return name, nil
}
func (g *Gateway) RunBackups(ctx context.Context) {
	ticker := time.NewTicker(seconds(g.Config.BackupIntervalSeconds))
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			backupCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			_, err := g.CreateBackup(backupCtx)
			cancel()
			if err != nil {
				g.Log.Error("scheduled database backup failed")
			}
		}
	}
}
