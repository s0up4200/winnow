// Command winnow receives GitHub webhooks, routes them by Rules, and sends
// them to Discord.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"time"

	"github.com/s0up4200/winnow/internal/winnow"
)

func main() {
	isCheck := len(os.Args) > 1 && os.Args[1] == "check"
	if isCheck {
		os.Args = slices.Delete(os.Args, 1, 2)
	}
	configPath := flag.String("config", "/config/winnow.yaml", "path to the configuration file")
	flag.Parse()

	if isCheck {
		os.Exit(check(*configPath))
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := serve(*configPath, log); err != nil {
		log.Error("winnow stopped", "error", err)
		os.Exit(1)
	}
}

// check loads the configuration with the same function as startup and prints
// each error and warning. It returns the exit code: 1 when there is an error
// or a warning, else 0. It starts no server and sends nothing.
func check(configPath string) int {
	data, err := os.ReadFile(configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	_, errs, warns := winnow.Load(data)
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

func serve(configPath string, log *slog.Logger) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	cfg, errs, warns := winnow.Load(data)
	for _, w := range warns {
		log.Warn("configuration", "warning", w)
	}
	if len(errs) > 0 {
		return fmt.Errorf("configuration: %w", errors.Join(errs...))
	}
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           winnow.New(cfg, log),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       time.Minute,
	}
	log.Info("listening", "addr", cfg.Listen)
	return srv.ListenAndServe()
}
