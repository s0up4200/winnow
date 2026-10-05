// Command winnow receives GitHub webhooks, routes them by Rules, and sends
// them to Discord.
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
	"slices"
	"syscall"
	"time"

	"github.com/lmittmann/tint"
	"github.com/s0up4200/winnow/internal/winnow"
)

// drainTime is the longest time that winnow waits on SIGTERM for the HTTP
// server to stop and the Sink queues to empty.
const drainTime = 10 * time.Second

func main() {
	var cmd string
	if len(os.Args) > 1 && (os.Args[1] == "check" || os.Args[1] == "healthcheck") {
		cmd = os.Args[1]
		os.Args = slices.Delete(os.Args, 1, 2)
	}
	configPath := flag.String("config", "/config/winnow.yaml", "path to the configuration file")
	flag.Parse()

	switch cmd {
	case "check":
		os.Exit(check(*configPath))
	case "healthcheck":
		os.Exit(healthcheck(*configPath))
	}
	var handler slog.Handler = slog.NewJSONHandler(os.Stdout, nil)
	if fi, err := os.Stdout.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		handler = tint.NewTextHandler(os.Stdout, nil)
	}
	log := slog.New(handler)
	if err := serve(*configPath, log); err != nil {
		log.Error("winnow stopped", "error", err)
		os.Exit(1)
	}
}

// load reads the configuration file and loads it with winnow.Load.
func load(configPath string) (*winnow.Config, []error, []string) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, []error{err}, nil
	}
	return winnow.Load(data)
}

// check loads the configuration with the same function as startup and prints
// each error and warning. It returns the exit code: 1 when there is an error
// or a warning, else 0. It starts no server and sends nothing.
func check(configPath string) int {
	_, errs, warns := load(configPath)
	for _, err := range errs {
		fmt.Fprintln(os.Stderr, "error:", err)
	}
	for _, w := range warns {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	if len(errs) > 0 || len(warns) > 0 {
		return 1
	}
	return 0
}

// healthcheck sends GET /healthz to the port of listen on 127.0.0.1. It
// returns the exit code: 0 when the reply is 200, else 1. The Docker image
// has no curl, so its HEALTHCHECK runs this command.
func healthcheck(configPath string) int {
	cfg, errs, _ := load(configPath)
	if err := errors.Join(errs...); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	_, port, err := net.SplitHostPort(cfg.Listen)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: listen:", err)
		return 1
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort("127.0.0.1", port) + "/healthz")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "error: /healthz:", resp.Status)
		return 1
	}
	return 0
}

// serve runs the HTTP server until SIGTERM or SIGINT. Then it stops the
// server and drains the Sink queues for at most drainTime.
func serve(configPath string, log *slog.Logger) error {
	cfg, errs, warns := load(configPath)
	for _, w := range warns {
		log.Warn("configuration", "warning", w)
	}
	if len(errs) > 0 {
		return fmt.Errorf("configuration: %w", errors.Join(errs...))
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	handler := winnow.New(cfg, log)
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       time.Minute,
	}
	served := make(chan error, 1)
	go func() { served <- srv.ListenAndServe() }()
	log.Info("listening", "addr", cfg.Listen)
	select {
	case err := <-served:
		return err
	case <-ctx.Done():
	}

	log.Info("shutting down", "drain", drainTime.String())
	ctx, cancel := context.WithTimeout(context.Background(), drainTime)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Warn("HTTP server did not stop", "error", err)
	}
	handler.Shutdown(ctx)
	log.Info("stopped")
	return nil
}
