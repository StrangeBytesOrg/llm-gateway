package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/StrangeBytesOrg/llm-gateway/internal/gateway"
)

func main() {
	if err := run(); err != nil {
		slog.Error("gateway stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	configPath := flag.String("config", "config.yaml", "YAML configuration file")
	check := flag.Bool("check", false, "validate configuration and exit")
	verify := flag.String("verify-backup", "", "verify a backup database and exit (no configuration required)")
	exportSpec := flag.Bool("export-admin-openapi", false, "print generated admin OpenAPI JSON and exit (no configuration required)")
	flag.Parse()
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)
	if *exportSpec {
		spec, err := gateway.AdminOpenAPI()
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(append(spec, '\n'))
		return err
	}
	if *verify != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := gateway.VerifyBackup(ctx, *verify); err != nil {
			return err
		}
		log.Info("backup integrity verified")
		return nil
	}
	c, err := gateway.LoadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	if *check {
		log.Info("configuration valid")
		return nil
	}
	store, err := gateway.OpenStore(c.Database)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	defer store.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	err = store.ValidateUserGroups(ctx, c)
	if err == nil {
		err = store.Recover(ctx)
	}
	cancel()
	if err != nil {
		return fmt.Errorf("restart recovery: %w", err)
	}
	g := gateway.New(c, store, log)
	inference := newInferenceServer(c.Listen, g.InferenceHandler())
	admin := newAdminServer(c.AdminListen, g.AdminHandler())
	li, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return err
	}
	defer li.Close()
	la, err := net.Listen("tcp", c.AdminListen)
	if err != nil {
		return err
	}
	defer la.Close()
	workers, stopWorkers := context.WithCancel(context.Background())
	defer stopWorkers()
	var wg sync.WaitGroup
	for _, worker := range []func(context.Context){g.RunReconciler, g.RunBackups} {
		wg.Add(1)
		go func(work func(context.Context)) { defer wg.Done(); work(workers) }(worker)
	}
	failures := make(chan error, 2)
	go func() { failures <- inference.Serve(li) }()
	go func() { failures <- admin.Serve(la) }()
	log.Info("gateway listening", "inference", c.Listen, "admin", c.AdminListen)
	signals, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	var serveErr error
	select {
	case <-signals.Done():
	case serveErr = <-failures:
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
	}
	g.StopAdmission()
	drain, stopDrain := context.WithTimeout(context.Background(), time.Duration(c.ShutdownSeconds)*time.Second)
	defer stopDrain()
	var drainWG sync.WaitGroup
	for _, server := range []*http.Server{inference, admin} {
		drainWG.Add(1)
		go func(s *http.Server) {
			defer drainWG.Done()
			if e := s.Shutdown(drain); e != nil {
				_ = s.Close()
			}
		}(server)
	}
	drainWG.Wait()
	finalize, stopFinalize := context.WithTimeout(context.Background(), 12*time.Second)
	if err := g.WaitInference(finalize); err != nil {
		log.Error("inference finalization timed out; pending records will recover on restart")
	}
	stopFinalize()
	stopWorkers()
	wg.Wait()
	g.Client.CloseIdleConnections()
	log.Info("gateway stopped")
	return serveErr
}

// Header value limits supplement the byte limits for requests with many small headers.
func newInferenceServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 32 << 10, MaxHeaderValueCount: 128}
}

func newAdminServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 5 * time.Minute, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10, MaxHeaderValueCount: 64}
}
