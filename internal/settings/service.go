package settings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"super-proxy-pool/internal/config"
	"super-proxy-pool/internal/db"
	"super-proxy-pool/internal/models"
)

type Service struct {
	store *db.Store
	cfg   config.App
}

func NewService(store *db.Store, cfg config.App) *Service {
	return &Service{store: store, cfg: cfg}
}

func (s *Service) EnsureDefaults(ctx context.Context, passwordHash string) error {
	var count int
	if err := s.store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM settings WHERE id = 1`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	now := time.Now().UTC()
	_, err := s.store.DB.ExecContext(ctx, `INSERT INTO settings (
		id, panel_host, panel_port, password_hash, speed_test_enabled, latency_test_url, speed_test_url,
		latency_timeout_ms, speed_timeout_ms, latency_concurrency, speed_concurrency,
		default_subscription_interval_sec, mihomo_controller_secret, failure_retry_count, log_level,
		speed_max_bytes, pool_port_min, pool_port_max, created_at, updated_at
	) VALUES (1, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, ?, 2, 'info', ?, 0, 0, ?, ?)`,
		s.cfg.PanelHost,
		s.cfg.PanelPort,
		passwordHash,
		config.DefaultLatencyURL(),
		config.DefaultSpeedURL(),
		config.DefaultLatencyTimeoutMS(),
		config.DefaultSpeedTimeoutMS(),
		config.DefaultLatencyConcurrency(),
		config.DefaultSpeedConcurrency(),
		config.DefaultSubscriptionIntervalSec(),
		s.cfg.DefaultControllerSecret,
		config.DefaultSpeedMaxBytes(),
		now,
		now,
	)
	return err
}

func (s *Service) Get(ctx context.Context) (models.Settings, error) {
	row := s.store.DB.QueryRowContext(ctx, `SELECT id, panel_host, panel_port, password_hash, speed_test_enabled,
		latency_test_url, speed_test_url, latency_timeout_ms, speed_timeout_ms, latency_concurrency,
		speed_concurrency, default_subscription_interval_sec, mihomo_controller_secret, failure_retry_count,
		log_level, speed_max_bytes, pool_port_min, pool_port_max, created_at, updated_at FROM settings WHERE id = 1`)
	return scanSettings(row)
}

func (s *Service) Update(ctx context.Context, current models.Settings) (models.Settings, bool, error) {
	existing, err := s.Get(ctx)
	if err != nil {
		return models.Settings{}, false, err
	}
	restartRequired := existing.PanelHost != current.PanelHost || existing.PanelPort != current.PanelPort
	current.ID = 1
	current.PasswordHash = existing.PasswordHash
	current.PanelHost = strings.TrimSpace(current.PanelHost)
	current.LatencyTestURL = strings.TrimSpace(current.LatencyTestURL)
	current.SpeedTestURL = strings.TrimSpace(current.SpeedTestURL)
	current.MihomoControllerSecret = strings.TrimSpace(current.MihomoControllerSecret)
	current.LogLevel = normalizeLogLevel(current.LogLevel)
	if current.SpeedMaxBytes <= 0 {
		current.SpeedMaxBytes = config.DefaultSpeedMaxBytes()
	}
	if err := validateSettings(current); err != nil {
		return models.Settings{}, false, err
	}
	current.UpdatedAt = time.Now().UTC()
	_, err = s.store.DB.ExecContext(ctx, `UPDATE settings SET
		panel_host = ?, panel_port = ?, speed_test_enabled = ?, latency_test_url = ?, speed_test_url = ?,
		latency_timeout_ms = ?, speed_timeout_ms = ?, latency_concurrency = ?, speed_concurrency = ?,
		default_subscription_interval_sec = ?, mihomo_controller_secret = ?, failure_retry_count = ?,
		log_level = ?, speed_max_bytes = ?, pool_port_min = ?, pool_port_max = ?, updated_at = ? WHERE id = 1`,
		current.PanelHost, current.PanelPort, boolToInt(current.SpeedTestEnabled), current.LatencyTestURL, current.SpeedTestURL,
		current.LatencyTimeoutMS, current.SpeedTimeoutMS, current.LatencyConcurrency, current.SpeedConcurrency,
		current.DefaultSubscriptionIntervalSec, current.MihomoControllerSecret, current.FailureRetryCount,
		current.LogLevel, current.SpeedMaxBytes, current.PoolPortMin, current.PoolPortMax, current.UpdatedAt,
	)
	if err != nil {
		return models.Settings{}, false, err
	}
	updated, err := s.Get(ctx)
	return updated, restartRequired, err
}

func (s *Service) UpdatePasswordHash(ctx context.Context, hash string) error {
	_, err := s.store.DB.ExecContext(ctx, `UPDATE settings SET password_hash = ?, updated_at = ? WHERE id = 1`, hash, time.Now().UTC())
	return err
}

func scanSettings(scanner interface{ Scan(dest ...any) error }) (models.Settings, error) {
	var item models.Settings
	var speedEnabled int
	err := scanner.Scan(
		&item.ID, &item.PanelHost, &item.PanelPort, &item.PasswordHash, &speedEnabled,
		&item.LatencyTestURL, &item.SpeedTestURL, &item.LatencyTimeoutMS, &item.SpeedTimeoutMS,
		&item.LatencyConcurrency, &item.SpeedConcurrency, &item.DefaultSubscriptionIntervalSec,
		&item.MihomoControllerSecret, &item.FailureRetryCount, &item.LogLevel, &item.SpeedMaxBytes, &item.PoolPortMin, &item.PoolPortMax,
		&item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return models.Settings{}, err
	}
	item.SpeedTestEnabled = speedEnabled == 1
	return item, nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func validateSettings(item models.Settings) error {
	if item.PanelHost == "" {
		return errors.New("panel_host is required")
	}
	if item.PanelPort < 1 || item.PanelPort > 65535 {
		return fmt.Errorf("panel_port must be between 1 and 65535")
	}
	if item.LatencyTestURL == "" {
		return errors.New("latency_test_url is required")
	}
	if item.SpeedTestURL == "" {
		return errors.New("speed_test_url is required")
	}
	if item.LatencyTimeoutMS <= 0 {
		return errors.New("latency_timeout_ms must be greater than zero")
	}
	if item.SpeedTimeoutMS <= 0 {
		return errors.New("speed_timeout_ms must be greater than zero")
	}
	if item.LatencyConcurrency <= 0 {
		return errors.New("latency_concurrency must be greater than zero")
	}
	if item.SpeedConcurrency <= 0 {
		return errors.New("speed_concurrency must be greater than zero")
	}
	if item.DefaultSubscriptionIntervalSec <= 0 {
		return errors.New("default_subscription_interval_sec must be greater than zero")
	}
	if item.MihomoControllerSecret == "" {
		return errors.New("mihomo_controller_secret is required")
	}
	if item.FailureRetryCount < 0 {
		return errors.New("failure_retry_count must be zero or greater")
	}
	if !isAllowedLogLevel(item.LogLevel) {
		return fmt.Errorf("log_level must be one of trace, debug, info, warning, warn, error, silent")
	}
	if item.SpeedMaxBytes <= 0 {
		return errors.New("speed_max_bytes must be greater than zero")
	}
	if item.PoolPortMin < 0 || item.PoolPortMax < 0 {
		return errors.New("pool_port_min and pool_port_max must be zero or greater")
	}
	if (item.PoolPortMin == 0) != (item.PoolPortMax == 0) {
		return errors.New("pool_port_min and pool_port_max must both be zero, or both be set")
	}
	if item.PoolPortMin > 0 {
		if item.PoolPortMin < 1 || item.PoolPortMin > 65535 || item.PoolPortMax < 1 || item.PoolPortMax > 65535 {
			return errors.New("pool port range must stay between 1 and 65535")
		}
		if item.PoolPortMin > item.PoolPortMax {
			return errors.New("pool_port_min must be less than or equal to pool_port_max")
		}
	}
	return nil
}

func normalizeLogLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "trace", "debug", "info", "warning", "warn", "error", "silent":
		return strings.ToLower(strings.TrimSpace(level))
	default:
		return "info"
	}
}

func isAllowedLogLevel(level string) bool {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "trace", "debug", "info", "warning", "warn", "error", "silent":
		return true
	default:
		return false
	}
}

func IsNotFound(err error) bool {
	return err == sql.ErrNoRows
}
