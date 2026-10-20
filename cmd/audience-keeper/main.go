// Command audience-keeper runs a live classroom quiz: students answer
// questions asked on the fly, the teacher watches typing, pastes and tab
// switches in real time, and Claude can help through MCP.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"audience-keeper/internal/classroom"
	"audience-keeper/internal/mcpserver"
	"audience-keeper/internal/web"
)

var version = "dev"

const minTokenLen = 16

type config struct {
	addr           string
	adminToken     string
	mcpToken       string
	publicURL      string
	showNames      bool
	decksDir       string
	allowedOrigins []string
}

func loadConfig(getenv func(string) string) (config, error) {
	c := config{
		addr:       getenv("AK_ADDR"),
		adminToken: getenv("AK_ADMIN_TOKEN"),
		mcpToken:   getenv("AK_MCP_TOKEN"),
		publicURL:  strings.TrimSpace(getenv("AK_PUBLIC_URL")),
		showNames:  getenv("AK_MCP_SHOW_NAMES") == "true",
		decksDir:   strings.TrimSpace(getenv("AK_DECKS_DIR")),
	}
	if c.addr == "" {
		c.addr = ":8080"
	}
	if len(c.adminToken) < minTokenLen {
		return c, fmt.Errorf("AK_ADMIN_TOKEN doit faire au moins %d caractères", minTokenLen)
	}
	if c.mcpToken != "" && len(c.mcpToken) < minTokenLen {
		return c, fmt.Errorf("AK_MCP_TOKEN doit faire au moins %d caractères", minTokenLen)
	}
	if c.mcpToken != "" && c.mcpToken == c.adminToken {
		return c, errors.New("AK_MCP_TOKEN doit être différent de AK_ADMIN_TOKEN")
	}
	if c.publicURL != "" && !strings.HasPrefix(c.publicURL, "http://") && !strings.HasPrefix(c.publicURL, "https://") {
		return c, errors.New("AK_PUBLIC_URL doit commencer par http:// ou https://")
	}
	for _, o := range strings.Split(getenv("AK_ALLOWED_ORIGINS"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			c.allowedOrigins = append(c.allowedOrigins, o)
		}
	}
	return c, nil
}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		return err
	}
	room := classroom.New()

	var mcpHandler http.Handler
	if cfg.mcpToken != "" {
		srv := mcpserver.New(room, mcpserver.Options{ShowNames: cfg.showNames, Version: version})
		mcpHandler = web.RequireBearer(cfg.mcpToken, mcpserver.Handler(srv))
	} else {
		log.Warn("AK_MCP_TOKEN absent : endpoint MCP désactivé")
	}

	server, err := web.New(web.Config{AdminToken: cfg.adminToken, PublicURL: cfg.publicURL, AllowedOrigins: cfg.allowedOrigins, DecksDir: cfg.decksDir}, room, mcpHandler, log)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go server.Run(ctx)

	httpSrv := &http.Server{
		Addr:              cfg.addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// WebSocket handlers are hijacked and ignore Shutdown; deriving request
		// contexts from ctx makes them stop on SIGTERM.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("audience-keeper démarré", "addr", cfg.addr, "version", version, "mcp", mcpHandler != nil, "code", room.Code())
		errc <- httpSrv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}
