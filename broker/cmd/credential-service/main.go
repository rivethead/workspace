// Command credential-service is the broker's own service behind agentgateway.
//
//	credential-service serve                         run the HTTP API
//	credential-service stop <agent> <by> <reason>    set the stop switch
//	credential-service start <agent> <by>            clear the stop switch
//	credential-service history <agent>               who stopped and started the agent
//
// Configuration comes from the environment:
//
//	BROKER_ADDR           listen address (default :8090)
//	BROKER_JWKS_URL       SPIRE's key set (the OIDC discovery provider's /keys)
//	BROKER_TRUST_DOMAIN   SPIFFE trust domain (default workspace.local)
//	BROKER_AUDIENCE       the JWT-SVID audience this service accepts (default broker)
//	BROKER_RULES          the rules file the provisioner publishes
//	BROKER_DATABASE_URL   Postgres for the stop switch
//	BROKER_GITHUB_API     GitHub API base URL (default https://api.github.com)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/example-org/workspace/broker/internal/github"
	"github.com/example-org/workspace/broker/internal/identity"
	"github.com/example-org/workspace/broker/internal/rules"
	"github.com/example-org/workspace/broker/internal/server"
	"github.com/example-org/workspace/broker/internal/stops"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "credential-service:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: credential-service serve | stop <agent> <by> <reason> | start <agent> <by> | history <agent>")
	}
	switch args[0] {
	case "serve":
		return serve(ctx)
	case "stop":
		if len(args) < 4 {
			return errors.New("usage: credential-service stop <agent> <by> <reason>")
		}
		return withStops(ctx, func(s *stops.Postgres) error {
			return s.Set(ctx, stops.Stop{Agent: args[1], By: args[2], Reason: strings.Join(args[3:], " ")})
		})
	case "start":
		if len(args) != 3 {
			return errors.New("usage: credential-service start <agent> <by>")
		}
		return withStops(ctx, func(s *stops.Postgres) error { return s.Clear(ctx, args[1], args[2]) })
	case "history":
		if len(args) != 2 {
			return errors.New("usage: credential-service history <agent>")
		}
		return withStops(ctx, func(s *stops.Postgres) error {
			events, err := s.History(ctx, args[1])
			for _, e := range events {
				fmt.Printf("%s\t%s\t%s\t%s\n", e.At.UTC().Format(time.RFC3339), e.Action, e.By, e.Reason)
			}
			return err
		})
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func required(name string) (string, error) {
	if v := os.Getenv(name); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("%s must be set", name)
}

func withStops(ctx context.Context, fn func(*stops.Postgres) error) error {
	url, err := required("BROKER_DATABASE_URL")
	if err != nil {
		return err
	}
	s, err := stops.NewPostgres(ctx, url)
	if err != nil {
		return err
	}
	defer s.Close()
	return fn(s)
}

func serve(ctx context.Context) error {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	jwks, err := required("BROKER_JWKS_URL")
	if err != nil {
		return err
	}
	rulesPath, err := required("BROKER_RULES")
	if err != nil {
		return err
	}
	dbURL, err := required("BROKER_DATABASE_URL")
	if err != nil {
		return err
	}
	ruleFile, err := rules.OpenFile(rulesPath)
	if err != nil {
		return err
	}
	stopStore, err := stops.NewPostgres(ctx, dbURL)
	if err != nil {
		return err
	}
	defer stopStore.Close()

	minter := github.NewMinter()
	minter.API = env("BROKER_GITHUB_API", minter.API)
	s := &server.Server{
		Verifier:  identity.NewVerifier(jwks, env("BROKER_TRUST_DOMAIN", "workspace.local"), env("BROKER_AUDIENCE", "broker")),
		Rules:     ruleFile,
		Stops:     stopStore,
		Minter:    minter,
		SignerFor: server.LocalSigners(),
		Log:       log,
	}
	httpSrv := &http.Server{
		Addr:         env("BROKER_ADDR", ":8090"),
		Handler:      s.Handler(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- httpSrv.ListenAndServe() }()
	log.Info("listening", "addr", httpSrv.Addr)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdown)
	}
}
