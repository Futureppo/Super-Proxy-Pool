package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	DB *sql.DB
}

func Open(path string) (*Store, error) {
	database, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(1)
	database.SetConnMaxLifetime(30 * time.Minute)

	store := &Store{DB: database}
	if err := store.migrate(context.Background()); err != nil {
		_ = database.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.DB.Close()
}

func (s *Store) ExecContext(ctx context.Context, query string, args ...any) error {
	_, err := s.DB.ExecContext(ctx, query, args...)
	return err
}

func (s *Store) migrate(ctx context.Context) error {
	statements := []string{
		`PRAGMA journal_mode = WAL;`,
		`PRAGMA foreign_keys = ON;`,
		`CREATE TABLE IF NOT EXISTS settings (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			panel_host TEXT NOT NULL,
			panel_port INTEGER NOT NULL,
			password_hash TEXT NOT NULL,
			speed_test_enabled INTEGER NOT NULL DEFAULT 0,
			latency_test_url TEXT NOT NULL,
			speed_test_url TEXT NOT NULL,
			latency_timeout_ms INTEGER NOT NULL,
			speed_timeout_ms INTEGER NOT NULL,
			latency_concurrency INTEGER NOT NULL,
			speed_concurrency INTEGER NOT NULL,
			default_subscription_interval_sec INTEGER NOT NULL,
			mihomo_controller_secret TEXT NOT NULL,
			failure_retry_count INTEGER NOT NULL DEFAULT 2,
			log_level TEXT NOT NULL DEFAULT 'info',
			speed_max_bytes INTEGER NOT NULL DEFAULT 5000000,
			pool_port_min INTEGER NOT NULL DEFAULT 0,
			pool_port_max INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS subscriptions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			url TEXT NOT NULL,
			headers_json TEXT NOT NULL DEFAULT '{}',
			enabled INTEGER NOT NULL DEFAULT 1,
			sync_interval_sec INTEGER NOT NULL,
			last_sync_at TIMESTAMP NULL,
			last_sync_status TEXT NOT NULL DEFAULT '',
			last_error TEXT NOT NULL DEFAULT '',
			etag TEXT NOT NULL DEFAULT '',
			last_modified TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS subscription_nodes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			subscription_id INTEGER NOT NULL,
			display_name TEXT NOT NULL,
			protocol TEXT NOT NULL,
			server TEXT NOT NULL,
			port INTEGER NOT NULL,
			raw_payload TEXT NOT NULL,
			normalized_json TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			last_latency_ms INTEGER NULL,
			last_speed_mbps REAL NULL,
			last_status TEXT NOT NULL DEFAULT 'unknown',
			last_test_at TIMESTAMP NULL,
			last_speed_at TIMESTAMP NULL,
			last_error TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL,
			FOREIGN KEY(subscription_id) REFERENCES subscriptions(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS manual_nodes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			display_name TEXT NOT NULL,
			protocol TEXT NOT NULL,
			server TEXT NOT NULL,
			port INTEGER NOT NULL,
			raw_payload TEXT NOT NULL,
			normalized_json TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			last_latency_ms INTEGER NULL,
			last_speed_mbps REAL NULL,
			last_status TEXT NOT NULL DEFAULT 'unknown',
			last_test_at TIMESTAMP NULL,
			last_speed_at TIMESTAMP NULL,
			last_error TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS proxy_pools (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			protocol TEXT NOT NULL,
			listen_host TEXT NOT NULL,
			listen_port INTEGER NOT NULL,
			auth_enabled INTEGER NOT NULL DEFAULT 0,
			auth_username TEXT NOT NULL DEFAULT '',
			auth_password_secret TEXT NOT NULL DEFAULT '',
			strategy TEXT NOT NULL,
			failover_enabled INTEGER NOT NULL DEFAULT 1,
			enabled INTEGER NOT NULL DEFAULT 1,
			last_published_at TIMESTAMP NULL,
			last_publish_status TEXT NOT NULL DEFAULT '',
			last_error TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL
		);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_proxy_pools_listen_port ON proxy_pools(listen_port);`,
		`CREATE TABLE IF NOT EXISTS proxy_pool_members (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			pool_id INTEGER NOT NULL,
			source_type TEXT NOT NULL,
			source_node_id INTEGER NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			weight INTEGER NOT NULL DEFAULT 1,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL,
			FOREIGN KEY(pool_id) REFERENCES proxy_pools(id) ON DELETE CASCADE
		);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_proxy_pool_member_unique
			ON proxy_pool_members(pool_id, source_type, source_node_id);`,
		`CREATE TABLE IF NOT EXISTS probe_history (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_type TEXT NOT NULL,
			source_node_id INTEGER NOT NULL,
			test_type TEXT NOT NULL,
			success INTEGER NOT NULL,
			latency_ms INTEGER NULL,
			speed_mbps REAL NULL,
			error_message TEXT NOT NULL DEFAULT '',
			tested_at TIMESTAMP NOT NULL
		);`,
	}

	for _, stmt := range statements {
		if _, err := s.DB.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
	}
	if err := s.ensureColumn(ctx, "settings", "pool_port_min", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "settings", "pool_port_max", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	return nil
}

func (s *Store) ensureColumn(ctx context.Context, table, column, definition string) error {
	rows, err := s.DB.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return fmt.Errorf("inspect table %s: %w", table, err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name string
		var colType string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &pk); err != nil {
			return fmt.Errorf("scan table %s info: %w", table, err)
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate table %s info: %w", table, err)
	}

	if _, err := s.DB.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, definition)); err != nil {
		return fmt.Errorf("add column %s.%s: %w", table, column, err)
	}
	return nil
}
