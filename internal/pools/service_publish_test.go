package pools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

func TestServicePublishWritesConfigs(t *testing.T) {
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

	createdNodes, _, err := nodeSvc.Create(ctx, nodes.CreateRequest{
		Content: "trojan://password@demo.example.com:443#demo-node",
	})
	if err != nil {
		t.Fatalf("Create manual node error = %v", err)
	}
	pool, err := poolSvc.Create(ctx, UpsertRequest{
		Name:            "demo-pool",
		Protocol:        "http",
		ListenHost:      "0.0.0.0",
		ListenPort:      18080,
		Strategy:        "round_robin",
		FailoverEnabled: true,
		Enabled:         true,
	})
	if err != nil {
		t.Fatalf("Create pool error = %v", err)
	}
	if err := poolSvc.UpdateMembers(ctx, pool.ID, []MemberInput{{
		SourceType:   "manual",
		SourceNodeID: createdNodes[0].ID,
		Enabled:      true,
		Weight:       1,
	}}); err != nil {
		t.Fatalf("UpdateMembers() error = %v", err)
	}
	if err := poolSvc.Publish(ctx, pool.ID); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	prodConfig, err := os.ReadFile(cfg.ProdConfigPath)
	if err != nil {
		t.Fatalf("ReadFile(prod) error = %v", err)
	}
	probeConfig, err := os.ReadFile(cfg.ProbeConfigPath)
	if err != nil {
		t.Fatalf("ReadFile(probe) error = %v", err)
	}

	if !strings.Contains(string(prodConfig), "pool-group-") || !strings.Contains(string(prodConfig), "listeners:") || !strings.Contains(string(prodConfig), "demo.example.com") {
		t.Fatalf("unexpected prod config:\n%s", string(prodConfig))
	}
	if !strings.Contains(string(probeConfig), "mixed-port: 17891") || !strings.Contains(string(probeConfig), "demo.example.com") {
		t.Fatalf("unexpected probe config:\n%s", string(probeConfig))
	}
}
