package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"super-proxy-pool/internal/auth"
	"super-proxy-pool/internal/config"
	"super-proxy-pool/internal/db"
	"super-proxy-pool/internal/events"
	"super-proxy-pool/internal/mihomo"
	"super-proxy-pool/internal/nodes"
	"super-proxy-pool/internal/pools"
	"super-proxy-pool/internal/probe"
	"super-proxy-pool/internal/settings"
	"super-proxy-pool/internal/subscriptions"
	"super-proxy-pool/internal/web"
)

func main() {
	cfg := config.Load()
	if err := config.EnsureDirs(cfg); err != nil {
		log.Fatalf("ensure dirs: %v", err)
	}

	store, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer store.Close()

	settingsSvc := settings.NewService(store, cfg)
	defaultHash, err := auth.HashPassword("admin")
	if err != nil {
		log.Fatalf("hash default password: %v", err)
	}
	if err := settingsSvc.EnsureDefaults(context.Background(), defaultHash); err != nil {
		log.Fatalf("ensure default settings: %v", err)
	}

	broker := events.NewBroker()
	authSvc := auth.NewService(settingsSvc, cfg.SessionMaxAgeSec)
	nodeSvc := nodes.NewService(store, broker)
	subSvc := subscriptions.NewService(store, settingsSvc, broker)
	rootCtx, rootCancel := context.WithCancel(context.Background())
	var shutdownOnce sync.Once
	requestShutdown := func() {
		shutdownOnce.Do(rootCancel)
	}

	currentSettings, err := settingsSvc.Get(context.Background())
	if err != nil {
		log.Fatalf("load settings: %v", err)
	}

	mihomoMgr := mihomo.NewManager(mihomo.Options{
		BinaryPath:          cfg.MihomoBinaryPath,
		RuntimeDir:          cfg.RuntimeDir,
		ProdConfigPath:      cfg.ProdConfigPath,
		ProbeConfigPath:     cfg.ProbeConfigPath,
		ProdControllerAddr:  cfg.ProdControllerAddr,
		ProbeControllerAddr: cfg.ProbeControllerAddr,
		ProbeMixedPort:      cfg.ProbeMixedPort,
		InitialLogLevel:     currentSettings.LogLevel,
	})
	poolSvc := pools.NewService(store, settingsSvc, nodeSvc, subSvc, mihomoMgr, broker)
	probeSvc := probe.NewService(settingsSvc, store, nodeSvc, subSvc, poolSvc, mihomoMgr, broker)
	subSvc.SetAfterSyncHook(func(ctx context.Context, subscriptionID int64, nodeIDs []int64) {
		_ = subscriptionID
		for _, nodeID := range nodeIDs {
			_ = probeSvc.EnqueueLatency("subscription", nodeID)
		}
	})
	if err := mihomoMgr.Start(context.Background(), currentSettings.MihomoControllerSecret); err != nil {
		log.Printf("mihomo start skipped: %v", err)
	}
	probeSvc.Start(rootCtx)
	subSvc.StartScheduler(rootCtx)

	webApp, err := web.New(authSvc, settingsSvc, nodeSvc, subSvc, poolSvc, probeSvc, broker, requestShutdown)
	if err != nil {
		log.Fatalf("build web app: %v", err)
	}
	router, err := webApp.Router()
	if err != nil {
		log.Fatalf("build router: %v", err)
	}

	server := &http.Server{
		Addr:              currentSettings.PanelHost + ":" + strconv.Itoa(currentSettings.PanelPort),
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("super-proxy-pool listening on %s", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server error: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		requestShutdown()
	}()

	<-rootCtx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("server shutdown error: %v", err)
	}
	mihomoMgr.Stop()
}
