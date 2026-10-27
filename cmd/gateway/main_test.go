package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/StrangeBytesOrg/llm-gateway/internal/gateway"
)

func freeAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := l.Addr().String()
	l.Close()
	return address
}

func TestExecutableLifecycle(t *testing.T) {
	dir := t.TempDir()
	inference, admin := freeAddress(t), freeAddress(t)
	path := filepath.Join(dir, "gateway.db")
	configPath := filepath.Join(dir, "config.json")
	config := map[string]any{"listen": inference, "admin_listen": admin, "database": path, "backup_directory": filepath.Join(dir, "backups"), "providers": map[string]any{"openrouter": map[string]string{"base_url": "https://example.invalid/api/v1", "api_key_env": "OPENROUTER_API_KEY", "accounting": "openrouter"}}, "models": map[string]any{"default": map[string]string{"provider": "openrouter", "model": "test"}}}
	config["model_access_groups"] = map[string]any{"free": map[string]any{"models": []string{"default"}}}
	config["default_model_access_group"] = "free"
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestServerHelper$")
	child.Env = append(os.Environ(), "GATEWAY_PROCESS_CONFIG="+configPath, "GATEWAY_ADMIN_TOKEN="+strings.Repeat("a", 40), "OPENROUTER_API_KEY=test")
	var logs bytes.Buffer
	child.Stdout = &logs
	child.Stderr = &logs
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	stopped := false
	defer func() {
		if !stopped {
			child.Process.Kill()
			<-done
		}
	}()
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(10 * time.Second)
	for {
		response, e := client.Get("http://" + inference + "/health/live")
		if e == nil {
			response.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("gateway never became live")
		}
		time.Sleep(20 * time.Millisecond)
	}
	req, err := http.NewRequest("POST", "http://"+admin+"/admin/v1/users", strings.NewReader(`{"id":"smoke-user","budget_usd":"1","cadence":"monthly","anchor":"2025-01-01T00:00:00Z"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 40))
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 201 {
		t.Fatalf("provisioning status=%d body=%s error=%v", response.StatusCode, body, err)
	}
	var user gateway.User
	if err = json.Unmarshal(body, &user); err != nil {
		t.Fatal(err)
	}
	if err = child.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		stopped = true
		if err != nil {
			t.Fatalf("unclean shutdown: %v logs=%s", err, logs.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown did not finish")
	}
	store, err := gateway.OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err = store.User(context.Background(), user.ID); err != nil {
		t.Fatalf("provisioned user did not survive restart: %v", err)
	}
}

func TestServerHelper(t *testing.T) {
	path := os.Getenv("GATEWAY_PROCESS_CONFIG")
	if path == "" {
		t.Skip("subprocess only")
	}
	// Isolate the service flags from the outer test runner's flags.
	flag.CommandLine = flag.NewFlagSet("gateway", flag.ContinueOnError)
	os.Args = []string{"gateway", "-config", path}
	if err := run(); err != nil {
		t.Fatal(err)
	}
}
