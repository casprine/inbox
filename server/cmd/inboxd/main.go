package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/casprine/inbox/server/internal/alfather"
	"github.com/casprine/inbox/server/internal/auth"
	"github.com/casprine/inbox/server/internal/backfill"
	"github.com/casprine/inbox/server/internal/blob"
	"github.com/casprine/inbox/server/internal/cloudflare"
	"github.com/casprine/inbox/server/internal/config"
	"github.com/casprine/inbox/server/internal/crypto"
	"github.com/casprine/inbox/server/internal/events"
	"github.com/casprine/inbox/server/internal/gmail"
	"github.com/casprine/inbox/server/internal/health"
	"github.com/casprine/inbox/server/internal/httpapi"
	"github.com/casprine/inbox/server/internal/ids"
	"github.com/casprine/inbox/server/internal/ingest"
	"github.com/casprine/inbox/server/internal/mail"
	"github.com/casprine/inbox/server/internal/migrate"
	"github.com/casprine/inbox/server/internal/retention"
	"github.com/casprine/inbox/server/internal/rulesapi"
	"github.com/casprine/inbox/server/internal/store"
	"github.com/casprine/inbox/server/internal/telemetry"
)

const usage = "usage: inboxd serve | migrate | admin seed-demo|dev-token|rotate-keys | help"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "help", "-h", "--help", "version", "--version":
		fmt.Println(usage) // exits 0: the container image smoke test relies on this
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg := config.FromEnv()
	tcfg := telemetry.FromEnv(os.Getenv, cfg.Production)
	log, flush := telemetry.Setup(ctx, tcfg)
	defer func() {
		fctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = flush(fctx)
	}()

	var err error
	switch os.Args[1] {
	case "serve":
		err = serve(ctx, log, cfg)
	case "admin":
		err = admin(ctx, cfg, os.Args[2:])
	case "migrate":
		err = migrate.Up(ctx, cfg.DatabaseURL)
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		log.Error("fatal", "err", err)
		fctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = flush(fctx)
		cancel()
		os.Exit(1)
	}
}

func serve(ctx context.Context, log *slog.Logger, cfg config.Config) error {
	if err := telemetry.FromEnv(os.Getenv, cfg.Production).Validate(); err != nil {
		return configProblems(log, err)
	}
	if err := cfg.ValidateServe(); err != nil {
		return configProblems(log, err)
	}
	// Hashed once, in memory, with a random salt; the password itself is never stored or logged.
	passwords, err := auth.NewPasswordVerifier(cfg.OwnerPassword)
	if err != nil {
		return err
	}
	var ingestHandler http.Handler // the dedicated ingest listener; nil when ingest is not configured
	var pinger httpapi.Pinger
	var opts []httpapi.Option
	var api *mail.API
	var rulesAPI *rulesapi.API
	var gmailAPI *gmail.API
	var gmailEngine *gmail.Engine
	var gmailSvc *gmail.Service
	var cfSvc *cloudflare.Service
	var bfSvc *backfill.Service
	var retAPI *retention.API
	var alfAPI *alfather.API
	var healthChecker *health.Checker
	if cfg.DatabaseURL != "" {
		// Forward-only schema migrations under an advisory lock: safe to run on every start (idempotent).
		log.Info("applying migrations")
		if err := migrate.Up(ctx, cfg.DatabaseURL); err != nil {
			return fmt.Errorf("migrations: %w", err)
		}
		pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
		if err != nil {
			return err
		}
		defer pool.Close()
		ready := &readiness{db: pool, ttl: 30 * time.Second}
		pinger = ready
		hub := events.NewHub(pool, store.New(pool), log)
		go hub.Run(ctx) // closes every SSE stream when ctx ends, before the HTTP server shuts down
		{
			api = &mail.API{Store: store.New(pool), Log: log}
			rulesAPI = &rulesapi.API{Store: store.New(pool), Log: log}
			cfSvc = &cloudflare.Service{Store: store.New(pool), Events: hub, Log: log, Client: &cloudflare.Client{
				Token: cfg.CFAPIToken, AccountID: cfg.CFAccountID, WorkerName: cfg.CFWorkerName, Base: cfg.CFAPIBase}}
			api.Zones = cfSvc
			gsvc := newGmailService(cfg, store.New(pool), hub, log)
			gmailSvc = gsvc
			gmailEngine = &gmail.Engine{Svc: gsvc, Store: store.New(pool), Log: log}
			gsvc.OnConnected = func(context.Context) { gmailEngine.Kick() }
			gmailAPI = &gmail.API{Svc: gsvc, Engine: gmailEngine}
			go gmailEngine.Run(ctx)
			alfAPI = &alfather.API{Store: store.New(pool), BaseURL: cfg.PublicBaseURL, Log: log}
			retAPI = &retention.API{Store: store.New(pool), Log: log}
			bfSvc = &backfill.Service{Store: store.New(pool), Gmail: gsvc, Events: hub, Log: log}
			healthChecker = &health.Checker{Store: store.New(pool), Gmail: gsvc}
			if cfg.S3Endpoint != "" && cfg.S3Bucket != "" {
				bs := blob.New(blob.Config{Endpoint: cfg.S3Endpoint, Region: cfg.S3Region, Bucket: cfg.S3Bucket,
					AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey})
				api.Blob = bs
				ready.blob = bs
			}
			svc := &auth.Service{Store: store.New(pool), Password: passwords, Login: &auth.LoginLimiter{},
				ClientIPHeader: cfg.LoginIPHeader, Log: log}
			opts = append(opts, httpapi.WithRoutes(func(r chi.Router) {
				svc.Routes(r, func(r chi.Router) {
					api.Routes(r)
					rulesAPI.Routes(r)
					gmailAPI.Routes(r)
					(&cloudflare.API{Svc: cfSvc}).Routes(r)
					(&backfill.API{Svc: bfSvc}).Routes(r)
					retAPI.Routes(r)
					alfAPI.Routes(r)
					(&health.API{Checker: healthChecker, Log: log}).Routes(r)
					r.Get("/events", hub.ServeHTTP)
				})
			}), httpapi.WithRoutes(gmailAPI.PublicRoutes), httpapi.WithRoutes(alfAPI.MCPRoutes))
		}
		if cfg.S3Endpoint != "" && cfg.S3Bucket != "" {
			blobs := blob.New(blob.Config{Endpoint: cfg.S3Endpoint, Region: cfg.S3Region, Bucket: cfg.S3Bucket,
				AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey})
			deps := ingest.Deps{Pool: pool, Store: store.New(pool), Blob: blobs, Log: log, ReconcileInterval: cfg.ReconcileInterval, ReconcileMinAge: cfg.ReconcileMinAge}
			if gmailSvc != nil {
				deps.Periodic = append(deps.Periodic, health.PeriodicSnapshot(), retention.PeriodicPrune())
				if cfSvc.Configured() {
					deps.Periodic = append(deps.Periodic, cloudflare.PeriodicRoutingCheck())
				}
				deps.Extra = func(w *river.Workers) {
					river.AddWorker(w, &cloudflare.RoutingCheckWorker{Svc: cfSvc})
					river.AddWorker(w, &retention.PruneWorker{Pruner: &retention.Pruner{Store: deps.Store, Blob: blobs, Log: log}})
					river.AddWorker(w, &health.SnapshotWorker{Checker: healthChecker, Events: hub, DeadmanURL: cfg.DeadmanURL, Log: log})
					ing := &ingest.Worker{Store: deps.Store, Blob: blobs, Router: ingest.RulesRouter{}, Log: log}
					river.AddWorker(w, &backfill.Worker{Gmail: gmailSvc, Store: deps.Store, Blob: blobs, Ingest: ing, Events: hub,
						Log: log, Throttle: backfill.DefaultThrottle})
					river.AddWorker(w, &gmail.HydrateWorker{Svc: gmailSvc, Store: deps.Store, Blob: blobs, Log: log,
						Ingest: &ingest.Worker{Store: deps.Store, Blob: blobs, Router: ingest.RulesRouter{}, Log: log}})
				}
			}
			rc, err := ingest.NewRiver(deps)
			if err != nil {
				return err
			}
			if err := rc.Start(ctx); err != nil {
				return err
			}
			defer func() {
				sctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				_ = rc.Stop(sctx)
			}()
			if api != nil {
				api.Retry = ingest.RiverEnqueuer{Client: rc}
				api.Refresh = ingest.RiverEnqueuer{Client: rc}
				rulesAPI.Reroute = ingest.RiverEnqueuer{Client: rc}
				gmailAPI.Reroute = ingest.RiverEnqueuer{Client: rc}
				bfSvc.Queue = backfill.RiverQueue{Client: rc}
				retAPI.Blob = blobs
				api.Fetch = &backfill.Importer{Gmail: gmailSvc, Store: deps.Store, Blob: blobs,
					Ingest: &ingest.Worker{Store: deps.Store, Blob: blobs, Router: ingest.RulesRouter{}, Log: log}}
				gmailSvc.OnConnected = func(ctx context.Context) {
					gmailEngine.Kick()
					bfSvc.ResumeSignedOut(ctx)
				}
				hydrate := gmail.RiverHydrate{Client: rc}
				gmailEngine.OnFound = func(ctx context.Context, thread string) {
					if err := hydrate.EnqueueHydrate(ctx, thread); err != nil {
						log.Warn("enqueue thread_hydrate", "err", err)
					}
				}
			}
			if cfg.IngestSecret != "" {
				ingestHandler = ingest.ListenerHandler(cfg.IngestSecret, ingest.RiverEnqueuer{Client: rc})
			} else {
				log.Warn("INGEST_SECRET not set: the ingest listener is off")
			}
		} else {
			log.Warn("S3 not configured: ingest disabled")
		}
	}
	// The API listener has no WriteTimeout cap that would cut long responses short beyond 2 minutes; the SSE
	// stream clears its own write deadline. The ingest listener only ever answers a tiny JSON POST.
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: httpapi.New(pinger, opts...), ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout: 60 * time.Second, WriteTimeout: 2 * time.Minute, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 32 << 10}
	servers := []*http.Server{srv}
	if ingestHandler != nil {
		// A second listener carries only /ingest/v1/notify, so it can be published to the internet on its own
		// (Tailscale Funnel) while the app API stays on the tailnet (D-020).
		servers = append(servers, &http.Server{Addr: cfg.IngestAddr, Handler: ingestHandler, ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10})
	}
	// SIGTERM (docker stop) cancels ctx: stop accepting, let in-flight requests finish (up to 20 s), then the
	// deferred River stop and pool close run. serve does not return before the drain is complete.
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		<-ctx.Done()
		log.Info("shutting down: draining requests")
		sctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		for _, s := range servers {
			if err := s.Shutdown(sctx); err != nil {
				log.Warn("shutdown", "addr", s.Addr, "err", err)
			}
		}
	}()
	errc := make(chan error, len(servers))
	for _, s := range servers {
		log.Info("listening", "addr", s.Addr, "ingest_only", s != srv)
		go func() { errc <- s.ListenAndServe() }()
	}
	var first error
	for range servers {
		err := <-errc
		if !errors.Is(err, http.ErrServerClosed) && first == nil {
			first = err
			stopAll(servers) // one listener failed (for example the port is taken): stop the other one too
		}
	}
	if first == nil {
		<-drained
		log.Info("stopped")
	}
	return first
}

// configProblems logs every configuration problem on its own line (each names its variable, none prints a
// secret) and returns a short error for main to exit on.
func configProblems(log *slog.Logger, err error) error {
	var problems []string
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		for _, e := range j.Unwrap() {
			problems = append(problems, e.Error())
		}
	} else {
		problems = []string{err.Error()}
	}
	for _, p := range problems {
		log.Error("invalid configuration", "problem", p)
	}
	return fmt.Errorf("refusing to start: %d configuration problem(s), see the lines above", len(problems))
}

func stopAll(servers []*http.Server) {
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, s := range servers {
		_ = s.Shutdown(sctx)
	}
}

// newGmailService builds the Gmail account service. Without a Google client or a valid
// TOKEN_ENC_KEY the service still answers status and reports "not configured".
func newGmailService(cfg config.Config, st *store.Store, pub events.Publisher, log *slog.Logger) *gmail.Service {
	svc := &gmail.Service{Store: st, Events: pub, Log: log, Cfg: gmail.Config{
		ClientID: cfg.GoogleClientID, ClientSecret: cfg.GoogleClientSecret,
		RedirectURL: strings.TrimRight(cfg.PublicBaseURL, "/") + "/oauth/google/callback",
		AuthURL:     cfg.GoogleAuthURL, TokenURL: cfg.GoogleTokenURL, RevokeURL: cfg.GoogleRevokeURL, APIBase: cfg.GmailAPIBase,
	}}
	if cfg.TokenEncKey != "" || cfg.TokenEncKeys != "" {
		keys, err := crypto.ParseKeyring(cfg.TokenEncKeys, cfg.TokenEncKey)
		if err != nil {
			log.Warn("token encryption keys invalid: Gmail disabled", "err", err)
		} else {
			svc.Keys = keys
		}
	}
	if !svc.Configured() {
		log.Warn("Gmail not configured (GOOGLE_CLIENT_ID/GOOGLE_CLIENT_SECRET/TOKEN_ENC_KEYS)")
	}
	return svc
}

func admin(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) < 1 {
		return errors.New("usage: inboxd admin seed-demo|dev-token|rotate-keys")
	}
	pool, err := store.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	switch args[0] {
	case "rotate-keys":
		// Re-seal the Gmail token under the first key of TOKEN_ENC_KEYS. See docs/RUNBOOK.md.
		keys, err := crypto.ParseKeyring(cfg.TokenEncKeys, cfg.TokenEncKey)
		if err != nil {
			return err
		}
		res, err := gmail.RotateKeys(ctx, store.New(pool), keys)
		if err != nil {
			return err
		}
		fmt.Printf("current key %q: re-sealed %d, already current %d\n", keys.CurrentID(), res.Resealed, res.Current)
		return nil
	case "dev-token":
		// Local development only: registers a device whose token you choose, so a simulator app can
		// call the real server without the login step. Needs shell access to the database.
		if len(args) < 2 || len(args[1]) < 16 {
			return errors.New("usage: inboxd admin dev-token <token of at least 16 characters>")
		}
		hash, err := hex.DecodeString(crypto.HashToken(args[1]))
		if err != nil {
			return err
		}
		st := store.New(pool)
		if _, err := st.DeviceByTokenHash(ctx, hash); err == nil {
			fmt.Println("device already registered")
			return nil
		}
		if err := st.CreateDevice(ctx, ids.NewV7(), "dev", hash); err != nil {
			return err
		}
		fmt.Println("device registered")
		return nil
	case "seed-demo":
		if cfg.S3Endpoint == "" || cfg.S3Bucket == "" {
			return errors.New("seed-demo needs S3_ENDPOINT and S3_BUCKET")
		}
		bl := blob.New(blob.Config{Endpoint: cfg.S3Endpoint, Region: cfg.S3Region, Bucket: cfg.S3Bucket,
			AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey})
		n, err := mail.SeedDemo(ctx, store.New(pool), bl, slog.Default())
		if err != nil {
			return err
		}
		fmt.Printf("seeded %d messages\n", n)
		return nil
	}
	return errors.New("usage: inboxd admin seed-demo|dev-token|rotate-keys")
}
