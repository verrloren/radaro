package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/verrloren/radaro/internal/alerts"
	"github.com/verrloren/radaro/internal/model"
	"github.com/verrloren/radaro/internal/outbox"
	"github.com/verrloren/radaro/internal/pipeline"
	"github.com/verrloren/radaro/internal/server"
	"github.com/verrloren/radaro/internal/store"
	"github.com/verrloren/radaro/web"
)

func (a *app) serveCmd() *cobra.Command {
	var (
		port int
		host string
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Launch the local web dashboard",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := a.openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			return a.runServer(cmd.Context(), st, host, port)
		},
	}
	cmd.Flags().IntVar(&port, "port", 8042, "port")
	cmd.Flags().StringVar(&host, "host", "127.0.0.1", "bind host")
	return cmd
}

func (a *app) runServer(ctx context.Context, st *store.Store, host string, port int) error {
	if port < 1 || port > 65535 {
		return errors.New("--port must be between 1 and 65535")
	}
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		stderr("! Serve over HTTPS (a reverse proxy such as Caddy) when this address is reachable from other machines: sign-in sends passwords.\n")
	}
	api, err := server.New(a.cfg, st, version, web.Dist())
	if err != nil {
		return err
	}
	ob := api.Outbox()
	ob.Notify = pipeline.AccountNotifier(a.cfg)
	ob.Logf = func(format string, args ...any) { stderr("  ! "+format+"\n", args...) }
	stopChecks := a.startAccountChecks(ctx, ob)
	defer stopChecks()
	srv := &http.Server{
		Addr:              net.JoinHostPort(host, strconv.Itoa(port)),
		Handler:           api.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	fmt.Printf("\nRadaro dashboard → http://%s\n", srv.Addr)
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

// startAccountChecks waits for a check in flight before the store is closed.
func (a *app) startAccountChecks(ctx context.Context, ob *outbox.Service) func() {
	if a.cfg.AccountCheckInterval <= 0 {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ob.Run(ctx, a.cfg.AccountCheckInterval)
	}()
	return func() { cancel(); <-done }
}

func (a *app) testAlertCmd() *cobra.Command {
	var transport, kind, webhookURL string
	cmd := &cobra.Command{
		Use:   "test-alert",
		Short: "Send one synthetic alert to verify a webhook/Slack or SMTP configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if webhookURL != "" {
				a.cfg.WebhookURL = webhookURL
			}
			if transport == "" {
				transport = "webhook"
				if a.cfg.WebhookURL == "" && len(a.cfg.EmailTo) > 0 {
					transport = "email"
				}
			}
			if transport != "webhook" && transport != "email" {
				return errors.New("--transport must be webhook or email")
			}
			if kind != "negative" && kind != "volume" && kind != "sentiment" {
				return errors.New("--kind must be negative, volume or sentiment")
			}
			targets, problems := pipeline.Targets(a.cfg)
			var target alerts.Target
			for _, t := range targets {
				if t.Name() == transport {
					target = t
				}
			}
			if target == nil {
				for _, p := range problems {
					stderr("! %s\n", p)
				}
				if transport == "webhook" {
					return errors.New("set RADARO_WEBHOOK_URL or pass --webhook-url")
				}
				return errors.New("configure RADARO_EMAIL_TO, RADARO_EMAIL_FROM and RADARO_SMTP_HOST")
			}
			ctx := cmd.Context()
			var err error
			if kind == "negative" {
				m := &model.Mention{
					Source: "radaro", Query: "alert test", Author: model.Str("Radaro"),
					Text:      "This is a synthetic negative-mention alert. Your alert transport is configured correctly.",
					CreatedAt: time.Now(), Sentiment: model.Negative, SentimentScore: model.Float(-1),
				}
				m.Normalize()
				err = target.SendMentions(ctx, m.Query, []*model.Mention{m})
			} else {
				event := "radaro.volume_spike"
				if kind == "sentiment" {
					event = "radaro.sentiment_drop"
				}
				err = target.SendThreshold(ctx, "Radaro test: synthetic "+kind+" threshold alert",
					map[string]any{"event": event, "query": "alert test", "synthetic": true})
			}
			if err != nil {
				return fmt.Errorf("alert test failed: %w", err)
			}
			fmt.Printf("✓ %s test delivered\n", transport)
			return nil
		},
	}
	cmd.Flags().StringVar(&transport, "transport", "", "webhook or email (default: the configured one)")
	cmd.Flags().StringVar(&kind, "kind", "negative", "synthetic event: negative, volume or sentiment")
	cmd.Flags().StringVar(&webhookURL, "webhook-url", "", "override RADARO_WEBHOOK_URL for this test")
	return cmd
}
