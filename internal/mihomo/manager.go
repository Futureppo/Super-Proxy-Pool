package mihomo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Options struct {
	BinaryPath          string
	RuntimeDir          string
	ProdConfigPath      string
	ProbeConfigPath     string
	ProdControllerAddr  string
	ProbeControllerAddr string
	ProbeMixedPort      int
}

type Manager struct {
	opts       Options
	httpClient *http.Client

	mu         sync.Mutex
	prodCmd    *exec.Cmd
	probeCmd   *exec.Cmd
	hasBinary  bool
	lastSecret string
}

func NewManager(opts Options) *Manager {
	_, err := exec.LookPath(opts.BinaryPath)
	return &Manager{
		opts:      opts,
		hasBinary: err == nil,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (m *Manager) Start(ctx context.Context, secret string) error {
	if err := os.MkdirAll(m.opts.RuntimeDir, 0o755); err != nil {
		return err
	}
	m.lastSecret = secret

	if _, err := os.Stat(m.opts.ProdConfigPath); errors.Is(err, os.ErrNotExist) {
		if err := writeFileAtomic(m.opts.ProdConfigPath, minimalProdConfig(secret, m.opts.ProdControllerAddr)); err != nil {
			return err
		}
	}
	if _, err := os.Stat(m.opts.ProbeConfigPath); errors.Is(err, os.ErrNotExist) {
		if err := writeFileAtomic(m.opts.ProbeConfigPath, minimalProbeConfig(secret, m.opts.ProbeControllerAddr, m.opts.ProbeMixedPort)); err != nil {
			return err
		}
	}
	if !m.hasBinary {
		return nil
	}
	if err := m.startProcess(ctx, "prod"); err != nil {
		return err
	}
	if err := m.startProcess(ctx, "probe"); err != nil {
		return err
	}
	return nil
}

func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	stopCmd(m.prodCmd)
	stopCmd(m.probeCmd)
	m.prodCmd = nil
	m.probeCmd = nil
}

func (m *Manager) ProbeMixedPort() int {
	return m.opts.ProbeMixedPort
}

func (m *Manager) ProdControllerAddr() string {
	return m.opts.ProdControllerAddr
}

func (m *Manager) ProbeControllerAddr() string {
	return m.opts.ProbeControllerAddr
}

func (m *Manager) ApplyProdConfig(payload []byte) error {
	if err := writeFileAtomic(m.opts.ProdConfigPath, payload); err != nil {
		return err
	}
	if !m.hasBinary {
		return nil
	}
	return m.restartProcess(context.Background(), "prod")
}

func (m *Manager) ApplyProbeConfig(payload []byte) error {
	if err := writeFileAtomic(m.opts.ProbeConfigPath, payload); err != nil {
		return err
	}
	if !m.hasBinary {
		return nil
	}
	if err := m.restartProcess(context.Background(), "probe"); err != nil {
		return err
	}
	return m.waitController(context.Background(), true)
}

func (m *Manager) Delay(ctx context.Context, secret, proxyName, targetURL string, timeoutMS int) (int64, error) {
	if !m.hasBinary {
		return 0, errors.New("mihomo binary not available")
	}
	if err := m.waitController(ctx, true); err != nil {
		return 0, err
	}
	endpoint := fmt.Sprintf("http://%s/proxies/%s/delay?url=%s&timeout=%d",
		m.opts.ProbeControllerAddr,
		url.PathEscape(proxyName),
		url.QueryEscape(targetURL),
		timeoutMS,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
	}
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return 0, fmt.Errorf("delay api failed: %s", strings.TrimSpace(string(body)))
	}
	var data struct {
		Delay int64 `json:"delay"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return 0, err
	}
	return data.Delay, nil
}

func (m *Manager) SetGlobalProxy(ctx context.Context, secret, proxyName string) error {
	if !m.hasBinary {
		return errors.New("mihomo binary not available")
	}
	if err := m.waitController(ctx, true); err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]string{"name": proxyName})
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, fmt.Sprintf("http://%s/proxies/GLOBAL", m.opts.ProbeControllerAddr), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("set global proxy failed: %s", strings.TrimSpace(string(body)))
	}
	return nil
}

func (m *Manager) waitController(ctx context.Context, probe bool) error {
	if !m.hasBinary {
		return nil
	}
	addr := m.opts.ProdControllerAddr
	if probe {
		addr = m.opts.ProbeControllerAddr
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://%s/version", addr), nil)
		if m.lastSecret != "" {
			req.Header.Set("Authorization", "Bearer "+m.lastSecret)
		}
		resp, err := m.httpClient.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 500 {
				return nil
			}
		}
		if time.Now().After(deadline) {
			if err != nil {
				return err
			}
			return fmt.Errorf("controller %s not ready", addr)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func (m *Manager) startProcess(ctx context.Context, kind string) error {
	configPath := m.opts.ProdConfigPath
	if kind == "probe" {
		configPath = m.opts.ProbeConfigPath
	}
	cmd := exec.CommandContext(ctx, m.opts.BinaryPath, "-d", m.opts.RuntimeDir, "-f", configPath)
	cmd.Dir = m.opts.RuntimeDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}

	m.mu.Lock()
	if kind == "prod" {
		m.prodCmd = cmd
	} else {
		m.probeCmd = cmd
	}
	m.mu.Unlock()

	go func() {
		_ = cmd.Wait()
	}()
	return nil
}

func (m *Manager) restartProcess(ctx context.Context, kind string) error {
	m.mu.Lock()
	if kind == "prod" {
		stopCmd(m.prodCmd)
		m.prodCmd = nil
	} else {
		stopCmd(m.probeCmd)
		m.probeCmd = nil
	}
	m.mu.Unlock()
	time.Sleep(300 * time.Millisecond)
	return m.startProcess(ctx, kind)
}

func stopCmd(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}

func writeFileAtomic(path string, payload []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func minimalProdConfig(secret, controller string) []byte {
	return []byte(fmt.Sprintf(`mode: rule
log-level: info
allow-lan: true
external-controller: %s
secret: "%s"
proxies: []
proxy-groups: []
listeners: []
rules:
  - MATCH,DIRECT
`, controller, secret))
}

func minimalProbeConfig(secret, controller string, mixedPort int) []byte {
	return []byte(fmt.Sprintf(`mode: global
log-level: info
allow-lan: false
mixed-port: %d
external-controller: %s
secret: "%s"
proxies: []
proxy-groups:
  - name: GLOBAL
    type: select
    proxies:
      - DIRECT
rules:
  - MATCH,GLOBAL
`, mixedPort, controller, secret))
}
