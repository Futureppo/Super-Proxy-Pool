package pools

import (
	"context"
	"path/filepath"
	"testing"

	"super-proxy-pool/internal/auth"
	"super-proxy-pool/internal/config"
	"super-proxy-pool/internal/db"
	"super-proxy-pool/internal/events"
	"super-proxy-pool/internal/mihomo"
	"super-proxy-pool/internal/nodes"
	"super-proxy-pool/internal/settings"
	"super-proxy-pool/internal/subscriptions"
)

func TestValidateUpsertRequestRequiresAuthFields(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	cfg := config.App{
		PanelHost:               "127.0.0.1",
		PanelPort:               7890,
		DataDir:                 tempDir,
		DBPath:                  filepath.Join(tempDir, "app.db"),
		RuntimeDir:              filepath.Join(tempDir, "runtime"),
		ProdConfigPath:          filepath.Join(tempDir, "runtime", "mihomo-prod.yaml"),
		ProbeConfigPath:         filepath.Join(tempDir, "runtime", "mihomo-probe.yaml"),
		ProdControllerAddr:      "127.0.0.1:19090",
		ProbeControllerAddr:     "127.0.0.1:19091",
		ProbeMixedPort:          17891,
		DefaultControllerSecret: "secret-token",
	}
	if err := config.EnsureDirs(cfg); err != nil {
		t.Fatalf("EnsureDirs() error = %v", err)
	}
	store, err := db.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	defer store.Close()

	settingsSvc := settings.NewService(store, cfg)
	hash, err := auth.HashPassword("admin")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	if err := settingsSvc.EnsureDefaults(ctx, hash); err != nil {
		t.Fatalf("EnsureDefaults() error = %v", err)
	}

	broker := events.NewBroker()
	nodeSvc := nodes.NewService(store, broker)
	subSvc := subscriptions.NewService(store, settingsSvc, broker)
	mihomoMgr := mihomo.NewManager(mihomo.Options{
		BinaryPath:          "nonexistent-mihomo-binary",
		RuntimeDir:          cfg.RuntimeDir,
		ProdConfigPath:      cfg.ProdConfigPath,
		ProbeConfigPath:     cfg.ProbeConfigPath,
		ProdControllerAddr:  cfg.ProdControllerAddr,
		ProbeControllerAddr: cfg.ProbeControllerAddr,
		ProbeMixedPort:      cfg.ProbeMixedPort,
	})
	poolSvc := NewService(store, settingsSvc, nodeSvc, subSvc, mihomoMgr, broker)

	// Missing username should fail
	if err := poolSvc.validateUpsertRequest(ctx, 0, UpsertRequest{
		Name:               "demo",
		AuthUsername:       "",
		AuthPasswordSecret: "pass",
	}); err == nil {
		t.Fatalf("expected auth validation error for missing username")
	}

	// Missing password should fail
	if err := poolSvc.validateUpsertRequest(ctx, 0, UpsertRequest{
		Name:               "demo",
		AuthUsername:       "user",
		AuthPasswordSecret: "",
	}); err == nil {
		t.Fatalf("expected auth validation error for missing password")
	}

	// Valid request should pass
	if err := poolSvc.validateUpsertRequest(ctx, 0, UpsertRequest{
		Name:               "demo",
		AuthUsername:       "user",
		AuthPasswordSecret: "pass",
		Strategy:           "round_robin",
		FailoverEnabled:    true,
		Enabled:            true,
	}); err != nil {
		t.Fatalf("validateUpsertRequest() error = %v", err)
	}
}
