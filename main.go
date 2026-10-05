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
	"time"

	"github.com/s0up4200/winnow/internal/winnow"
)

func main() {
	configPath := flag.String("config", "/config/winnow.yaml", "path to the configuration file")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := serve(*configPath, log); err != nil {
		log.Error("winnow stopped", "error", err)
		os.Exit(1)
	}
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
