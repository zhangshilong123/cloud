package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/wanglongan587/cloud/internal/api/router"
	"github.com/wanglongan587/cloud/internal/collab"
	"github.com/wanglongan587/cloud/internal/config"
	"github.com/wanglongan587/cloud/internal/controlgrpc"
	"github.com/wanglongan587/cloud/internal/core"
	"github.com/wanglongan587/cloud/internal/logger"
	"github.com/wanglongan587/cloud/internal/repository"
	"github.com/wanglongan587/cloud/internal/skillstore"
	"github.com/wanglongan587/cloud/internal/skillstore/s3store"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}

// configureCollaboration installs the optional development Agent/Team/Workflow fixtures on the store
// when the deployment explicitly enables them (`collaboration.development_fixtures`). It is the
// cmd/server composition gate: production default is OFF, leaving the collaboration ports nil so the
// target discovery API serves only human targets. It is intentionally independent of authentication —
// enabling GitHub Auth must never enable these fixtures, and an auth failure must never fall back to
// a fixture identity.
func configureCollaboration(store *core.Store, developmentFixtures bool, log *zap.Logger) {
	if !developmentFixtures {
		return
	}
	collab.WireDevelopmentFixtures(store)
	log.Warn("development collaboration fixtures enabled: Agent/Team/Workflow targets served from in-memory fixtures (development-only; production must leave collaboration.development_fixtures false)")
}

// s3Config translates the resolved `storage` section into the concrete s3store.Config. TLS
// verification is a resolved bool (defaults already applied by config.Load), and credential_mode is
// normalized.
func s3Config(sc *config.StorageConfig) s3store.Config {
	verify := true
	if sc.TLS.Verify != nil {
		verify = *sc.TLS.Verify
	}
	return s3store.Config{
		Region:             sc.Region,
		Bucket:             sc.Bucket,
		Endpoint:           sc.Endpoint,
		PathStyle:          sc.PathStyle,
		CredentialMode:     sc.CredentialMode,
		CredentialsFile:    sc.CredentialsFile,
		InsecureSkipVerify: !verify,
		CAFile:             sc.TLS.CAFile,
		ConnectTimeout:     sc.Timeouts.Connect,
		RequestTimeout:     sc.Timeouts.Request,
	}
}

// wireObjectStore translates the resolved `storage` section into the production
// S3-compatible ObjectStore and returns it, or nil when the section is absent (the
// saga then reports object_store_unavailable). An invalid section was already
// rejected by config.Load, so a non-nil error here is a construction failure
// (unreadable CA file, unusable credentials) and must fail startup.
func wireObjectStore(ctx context.Context, sc *config.StorageConfig, log *zap.Logger) (skillstore.ObjectStore, error) {
	if sc == nil {
		log.Info("object storage not configured; Skill uploads unavailable")
		return nil, nil
	}
	store, err := s3store.New(ctx, s3Config(sc))
	if err != nil {
		return nil, fmt.Errorf("configure object storage: %w", err)
	}
	log.Info("object storage configured", zap.String("provider", sc.Provider), zap.String("bucket", sc.Bucket))
	return store, nil
}

// wireRetrievalIssuer translates the resolved `storage` section into the production S3-compatible
// RetrievalCapabilityIssuer, or nil when the section is absent (mint then reports
// storage_not_configured). It is deliberately separate from wireObjectStore: ObjectStore owns
// durable put/stat/get, the issuer mints ephemeral credentials (Step 5B ADR D28).
func wireRetrievalIssuer(ctx context.Context, sc *config.StorageConfig, log *zap.Logger) (skillstore.RetrievalCapabilityIssuer, error) {
	if sc == nil {
		log.Info("object storage not configured; Skill retrieval capabilities unavailable")
		return nil, nil
	}
	issuer, err := s3store.NewIssuer(ctx, s3Config(sc))
	if err != nil {
		return nil, fmt.Errorf("configure retrieval capability issuer: %w", err)
	}
	log.Info("retrieval capability issuer configured", zap.String("provider", sc.Provider), zap.String("bucket", sc.Bucket))
	return issuer, nil
}

func run() (runErr error) {
	configPath := flag.String("config", "", "configuration file")
	flag.Parse()
	cfg, e := config.Load(*configPath)
	if e != nil {
		return e
	}
	log, e := logger.New(cfg.Logger)
	if e != nil {
		return e
	}
	defer func() { runErr = errors.Join(runErr, logger.Sync(log)) }()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	db, e := repository.InitDB(ctx, cfg.Database)
	if e != nil {
		return e
	}
	store, e := core.NewStore(db)
	if e != nil {
		return e
	}
	defer func() { runErr = errors.Join(runErr, store.Pool.Close()) }()
	if store.SkillsObjectStore, e = wireObjectStore(ctx, cfg.Storage, log); e != nil {
		return e
	}
	if store.RetrievalCapabilityIssuer, e = wireRetrievalIssuer(ctx, cfg.Storage, log); e != nil {
		return e
	}
	if cfg.Storage != nil {
		store.RetrievalCapabilityTTL = cfg.Storage.RetrievalCapabilityTTL
	}
	configureCollaboration(store, cfg.Collaboration.DevelopmentFixtures, log)
	if e := store.CheckSchema(ctx); e != nil {
		return e
	}
	auth, e := core.NewAuthenticator(cfg.Auth.Audience, cfg.Auth.Keys)
	if e != nil {
		return e
	}
	gin.SetMode(cfg.Server.Mode)
	server := &http.Server{Addr: fmt.Sprintf(":%d", cfg.Server.Port), Handler: router.New(store, auth, log), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: cfg.Server.ReadTimeout, WriteTimeout: cfg.Server.WriteTimeout, IdleTimeout: 60 * time.Second}
	// The control listener is bound before serving so a taken port fails startup, not a Controller.
	control, e := net.Listen("tcp", cfg.Control.GRPCAddr)
	if e != nil {
		return e
	}
	grpcServer := controlgrpc.New(store)
	failed := make(chan error, 2)
	go func() {
		log.Info("Cloud listening", zap.String("address", server.Addr))
		failed <- server.ListenAndServe()
	}()
	go func() {
		log.Info("Cloud control listening", zap.String("address", control.Addr().String()))
		failed <- grpcServer.Serve(control)
	}()
	select {
	case e = <-failed:
		if errors.Is(e, http.ErrServerClosed) || errors.Is(e, grpc.ErrServerStopped) {
			return nil
		}
		return e
	case <-ctx.Done():
		shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		// In-flight control calls finish or are cut at the same deadline as HTTP; a Controller
		// retries with the same submission identity, so cutting them loses nothing durable.
		// Tell the lease holder to stop claiming before its stream is cut; the Drain signal is a
		// hint, so a Controller that misses it simply fails its next claim against a stopped server.
		store.Signals.Drain()
		stopped := make(chan struct{})
		go func() {
			grpcServer.GracefulStop()
			close(stopped)
		}()
		select {
		case <-stopped:
		case <-shutdown.Done():
			grpcServer.Stop()
		}
		return server.Shutdown(shutdown)
	}
}
