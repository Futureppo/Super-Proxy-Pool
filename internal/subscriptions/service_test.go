package subscriptions

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"super-proxy-pool/internal/auth"
	"super-proxy-pool/internal/config"
	"super-proxy-pool/internal/db"
	"super-proxy-pool/internal/events"
	"super-proxy-pool/internal/models"
	"super-proxy-pool/internal/settings"
)

func TestShouldSyncSubscription(t *testing.T) {
	now := time.Date(2026, 4, 14, 0, 0, 0, 0, time.UTC)
	recent := now.Add(-5 * time.Minute)
	old := now.Add(-2 * time.Hour)

	cases := []struct {
		name string
		item models.Subscription
		want bool
	}{
		{
			name: "disabled subscription",
			item: models.Subscription{Enabled: false, SyncIntervalSec: 3600},
			want: false,
		},
		{
			name: "never synced",
			item: models.Subscription{Enabled: true, SyncIntervalSec: 3600},
			want: true,
		},
		{
			name: "not due yet",
			item: models.Subscription{Enabled: true, SyncIntervalSec: 3600, LastSyncAt: &recent},
			want: false,
		},
		{
			name: "due now",
			item: models.Subscription{Enabled: true, SyncIntervalSec: 3600, LastSyncAt: &old},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldSyncSubscription(tc.item, now); got != tc.want {
				t.Fatalf("shouldSyncSubscription() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSyncNotModifiedPreservesExistingNodes(t *testing.T) {
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

	lastModified := time.Now().UTC().Format(http.TimeFormat)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("If-None-Match"); got != "etag-123" {
			t.Fatalf("If-None-Match = %q, want %q", got, "etag-123")
		}
		if got := r.Header.Get("If-Modified-Since"); got != lastModified {
			t.Fatalf("If-Modified-Since = %q, want %q", got, lastModified)
		}
		w.WriteHeader(http.StatusNotModified)
	}))
	defer server.Close()

	svc := NewService(store, settingsSvc, events.NewBroker())
	sub, err := svc.Create(ctx, UpsertRequest{
		Name:            "demo-subscription",
		URL:             server.URL,
		Enabled:         true,
		SyncIntervalSec: 3600,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	now := time.Now().UTC()
	if _, err := store.DB.ExecContext(ctx, `UPDATE subscriptions
		SET etag = ?, last_modified = ?, last_sync_status = ?, last_sync_at = ?, updated_at = ?
		WHERE id = ?`,
		"etag-123", lastModified, "ok", now, now, sub.ID,
	); err != nil {
		t.Fatalf("prepare subscription metadata error = %v", err)
	}
	if _, err := store.DB.ExecContext(ctx, `INSERT INTO subscription_nodes (
			subscription_id, display_name, protocol, server, port, raw_payload, normalized_json, enabled, last_status, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, 1, 'available', ?, ?)`,
		sub.ID, "persist-node", "ss", "example.com", 443, "ss://example", `{}`, now, now,
	); err != nil {
		t.Fatalf("insert existing subscription node error = %v", err)
	}

	outcome, err := svc.Sync(ctx, sub.ID)
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if outcome.Status != "not_modified" {
		t.Fatalf("Sync() status = %q, want %q", outcome.Status, "not_modified")
	}
	if outcome.Modified {
		t.Fatalf("Sync() modified = true, want false")
	}
	if outcome.CreatedCount != 0 || outcome.FailedCount != 0 {
		t.Fatalf("unexpected sync outcome: %+v", outcome)
	}

	updated, err := svc.Get(ctx, sub.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if updated.LastSyncStatus != "not_modified" {
		t.Fatalf("LastSyncStatus = %q, want %q", updated.LastSyncStatus, "not_modified")
	}
	if updated.LastError != "" {
		t.Fatalf("LastError = %q, want empty", updated.LastError)
	}
	if updated.LastSyncAt == nil {
		t.Fatalf("LastSyncAt should be set after 304 sync")
	}

	nodes, err := svc.ListNodes(ctx, sub.ID)
	if err != nil {
		t.Fatalf("ListNodes() error = %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("ListNodes() len = %d, want 1", len(nodes))
	}
	if nodes[0].DisplayName != "persist-node" {
		t.Fatalf("preserved node name = %q, want %q", nodes[0].DisplayName, "persist-node")
	}
}
