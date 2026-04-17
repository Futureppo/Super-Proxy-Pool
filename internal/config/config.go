package config

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
)

const (
	defaultPanelHost                     = "0.0.0.0"
	defaultPanelPort                     = 7891
	defaultLatencyURL                    = "https://cp.cloudflare.com/generate_204"
	defaultLatencyTimeoutMS              = 5000
	defaultLatencyConcurrency            = 32
	defaultSpeedURL                      = "https://speed.cloudflare.com/__down?bytes=5000000"
	defaultSpeedTimeoutMS                = 10000
	defaultSpeedConcurrency              = 1
	MaxProbeSpeedSlots                   = 4
	defaultSubscriptionIntervalSec       = 3600
	defaultProbeControllerAddr           = "127.0.0.1:19091"
	defaultProdControllerAddr            = "127.0.0.1:19090"
	defaultProbeMixedPort                = 17891
	defaultSessionMaxAgeSec              = 86400
	defaultSpeedMaxBytes           int64 = 5000000
)

type App struct {
	PanelHost               string
	PanelPort               int
	DataDir                 string
	DBPath                  string
	RuntimeDir              string
	ProdConfigPath          string
	ProbeConfigPath         string
	MihomoBinaryPath        string
	ProdControllerAddr      string
	ProbeControllerAddr     string
	ProbeMixedPort          int
	SessionMaxAgeSec        int
	DefaultControllerSecret string
}

func Load() App {
	dataDir := getenv("DATA_DIR", defaultDataDir())
	runtimeDir := filepath.Join(dataDir, "runtime")
	cwd, _ := os.Getwd()

	return App{
		PanelHost:               getenv("PANEL_HOST", defaultPanelHost),
		PanelPort:               getenvInt("PANEL_PORT", defaultPanelPort),
		DataDir:                 dataDir,
		DBPath:                  getenv("DB_PATH", filepath.Join(dataDir, "app.db")),
		RuntimeDir:              runtimeDir,
		ProdConfigPath:          filepath.Join(runtimeDir, "mihomo-prod.yaml"),
		ProbeConfigPath:         filepath.Join(runtimeDir, "mihomo-probe.yaml"),
		MihomoBinaryPath:        resolveMihomoBinary(cwd, os.Getenv("MIHOMO_BINARY")),
		ProdControllerAddr:      getenv("PROD_CONTROLLER_ADDR", defaultProdControllerAddr),
		ProbeControllerAddr:     getenv("PROBE_CONTROLLER_ADDR", defaultProbeControllerAddr),
		ProbeMixedPort:          getenvInt("PROBE_MIXED_PORT", defaultProbeMixedPort),
		SessionMaxAgeSec:        getenvInt("SESSION_MAX_AGE_SEC", defaultSessionMaxAgeSec),
		DefaultControllerSecret: getenv("DEFAULT_CONTROLLER_SECRET", randomHex(24)),
	}
}

func EnsureDirs(cfg App) error {
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.RuntimeDir, 0o755); err != nil {
		return err
	}
	return nil
}

func DefaultPanelHost() string            { return defaultPanelHost }
func DefaultPanelPort() int               { return defaultPanelPort }
func DefaultLatencyURL() string           { return defaultLatencyURL }
func DefaultLatencyTimeoutMS() int        { return defaultLatencyTimeoutMS }
func DefaultLatencyConcurrency() int      { return defaultLatencyConcurrency }
func DefaultSpeedURL() string             { return defaultSpeedURL }
func DefaultSpeedTimeoutMS() int          { return defaultSpeedTimeoutMS }
func DefaultSpeedConcurrency() int        { return defaultSpeedConcurrency }
func DefaultSubscriptionIntervalSec() int { return defaultSubscriptionIntervalSec }
func DefaultSpeedMaxBytes() int64         { return defaultSpeedMaxBytes }

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			return parsed
		}
	}
	return fallback
}

func defaultDataDir() string {
	if runtime.GOOS == "windows" {
		if cwd, err := os.Getwd(); err == nil {
			return filepath.Join(cwd, "data")
		}
	}
	return "/data"
}

func defaultMihomoBinary() string {
	return defaultMihomoBinaryFor(runtime.GOOS)
}

func defaultMihomoBinaryFor(goos string) string {
	if goos == "windows" {
		return "mihomo.exe"
	}
	return "/usr/local/bin/mihomo"
}

func resolveMihomoBinary(baseDir, override string) string {
	if override != "" {
		return override
	}
	for _, candidate := range mihomoBinaryCandidates(baseDir, runtime.GOOS, runtime.GOARCH) {
		if resolved, err := exec.LookPath(candidate); err == nil {
			return resolved
		}
	}
	return defaultMihomoBinary()
}

func mihomoBinaryCandidates(baseDir, goos, goarch string) []string {
	name := mihomoBinaryName(goos)
	platformName := mihomoPlatformBinaryName(goos, goarch)
	locations := [][]string{
		{"bin"},
		{"tools"},
		{"deployments", "bin"},
		nil,
	}
	var candidates []string
	if baseDir != "" {
		for _, location := range locations {
			for _, binaryName := range []string{name, platformName} {
				if binaryName == "" {
					continue
				}
				parts := append([]string{baseDir}, location...)
				parts = append(parts, binaryName)
				candidates = append(candidates, filepath.Join(parts...))
			}
		}
	}
	candidates = append(candidates, defaultMihomoBinaryFor(goos), name)
	if goos == "windows" {
		candidates = append(candidates, "mihomo")
	}
	if platformName != "" {
		candidates = append(candidates, platformName)
	}
	return uniqueStrings(candidates)
}

func mihomoBinaryName(goos string) string {
	if goos == "windows" {
		return "mihomo.exe"
	}
	return "mihomo"
}

func mihomoPlatformBinaryName(goos, goarch string) string {
	if goos == "" || goarch == "" {
		return ""
	}
	name := "mihomo-" + goos + "-" + goarch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "super-proxy-pool-secret"
	}
	return hex.EncodeToString(buf)
}
