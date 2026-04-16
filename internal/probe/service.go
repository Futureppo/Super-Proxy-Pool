package probe

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/proxy"

	"super-proxy-pool/internal/db"
	"super-proxy-pool/internal/events"
	"super-proxy-pool/internal/mihomo"
	"super-proxy-pool/internal/models"
	"super-proxy-pool/internal/nodes"
	"super-proxy-pool/internal/pools"
	"super-proxy-pool/internal/settings"
	"super-proxy-pool/internal/subscriptions"
)

type Service struct {
	settingsSvc   *settings.Service
	store         *db.Store
	manualNodes   *nodes.Service
	subscriptions *subscriptions.Service
	pools         *pools.Service
	mihomo        *mihomo.Manager
	events        *events.Broker

	latencyQueue chan task
	speedQueue   chan task
	speedSlots   chan int

	activeLatency            int32
	activeSpeed              int32
	backgroundLatencyRunning int32
	backgroundSpeedRunning   int32
	startOnce                sync.Once
}

type task struct {
	SourceType   string
	SourceNodeID int64
	TestType     string
}

func NewService(settingsSvc *settings.Service, store *db.Store, manualNodes *nodes.Service, subscriptions *subscriptions.Service, poolSvc *pools.Service, mihomoMgr *mihomo.Manager, broker *events.Broker) *Service {
	speedSlots := make(chan int, pools.MaxProbeSpeedSlots)
	for slotIndex := 0; slotIndex < pools.MaxProbeSpeedSlots; slotIndex++ {
		speedSlots <- slotIndex
	}
	return &Service{
		settingsSvc:   settingsSvc,
		store:         store,
		manualNodes:   manualNodes,
		subscriptions: subscriptions,
		pools:         poolSvc,
		mihomo:        mihomoMgr,
		events:        broker,
		latencyQueue:  make(chan task, 512),
		speedQueue:    make(chan task, 128),
		speedSlots:    speedSlots,
	}
}

func (s *Service) Start(ctx context.Context) {
	s.startOnce.Do(func() {
		go s.dispatchLatency(ctx)
		go s.dispatchSpeed(ctx)
		go s.runBackgroundLatencySweep(ctx)
		go s.runBackgroundSpeedSweep(ctx)
	})
}

func (s *Service) EnqueueLatency(sourceType string, sourceNodeID int64) error {
	item := task{SourceType: sourceType, SourceNodeID: sourceNodeID, TestType: "latency"}
	select {
	case s.latencyQueue <- item:
		s.events.Publish("probe.queued", map[string]any{
			"source_type":    sourceType,
			"source_node_id": sourceNodeID,
			"test_type":      "latency",
		})
		return nil
	default:
		return fmt.Errorf("latency queue is full")
	}
}

func (s *Service) EnqueueSpeed(sourceType string, sourceNodeID int64) error {
	item := task{SourceType: sourceType, SourceNodeID: sourceNodeID, TestType: "speed"}
	select {
	case s.speedQueue <- item:
		s.events.Publish("probe.queued", map[string]any{
			"source_type":    sourceType,
			"source_node_id": sourceNodeID,
			"test_type":      "speed",
		})
		return nil
	default:
		return fmt.Errorf("speed queue is full")
	}
}

func (s *Service) dispatchLatency(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case item := <-s.latencyQueue:
			s.waitForSlot(ctx, &s.activeLatency, func() int { return s.currentLatencyLimit(ctx) })
			if ctx.Err() != nil {
				return
			}
			atomic.AddInt32(&s.activeLatency, 1)
			go func(it task) {
				defer atomic.AddInt32(&s.activeLatency, -1)
				s.runLatency(ctx, it)
			}(item)
		}
	}
}

func (s *Service) dispatchSpeed(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case item := <-s.speedQueue:
			s.waitForSlot(ctx, &s.activeSpeed, func() int { return s.currentSpeedLimit(ctx) })
			if ctx.Err() != nil {
				return
			}
			atomic.AddInt32(&s.activeSpeed, 1)
			go func(it task) {
				defer atomic.AddInt32(&s.activeSpeed, -1)
				s.runSpeed(ctx, it)
			}(item)
		}
	}
}

func (s *Service) runBackgroundLatencySweep(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	s.enqueuePoolMemberLatencySweep(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.enqueuePoolMemberLatencySweep(ctx)
		}
	}
}

func (s *Service) runBackgroundSpeedSweep(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Minute)
	defer ticker.Stop()
	s.enqueueBackgroundSpeedSweep(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.enqueueBackgroundSpeedSweep(ctx)
		}
	}
}

func (s *Service) enqueuePoolMemberLatencySweep(ctx context.Context) {
	if !atomic.CompareAndSwapInt32(&s.backgroundLatencyRunning, 0, 1) {
		return
	}
	defer atomic.StoreInt32(&s.backgroundLatencyRunning, 0)
	if s.pools == nil {
		return
	}
	poolList, err := s.pools.List(ctx)
	if err != nil {
		return
	}
	seen := make(map[string]struct{})
	for _, pool := range poolList {
		if !pool.Enabled {
			continue
		}
		members, err := s.pools.GetMembers(ctx, pool.ID)
		if err != nil {
			continue
		}
		for _, member := range members {
			if !member.Enabled {
				continue
			}
			key := fmt.Sprintf("%s:%d", member.SourceType, member.SourceNodeID)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			_ = s.EnqueueLatency(member.SourceType, member.SourceNodeID)
		}
	}
}

func (s *Service) enqueueBackgroundSpeedSweep(ctx context.Context) {
	if !atomic.CompareAndSwapInt32(&s.backgroundSpeedRunning, 0, 1) {
		return
	}
	defer atomic.StoreInt32(&s.backgroundSpeedRunning, 0)
	settingsRow, err := s.settingsSvc.Get(ctx)
	if err != nil || !settingsRow.SpeedTestEnabled {
		return
	}
	manualInventory, err := s.manualNodes.AllRuntimeNodes(ctx)
	if err != nil {
		return
	}
	subInventory, err := s.subscriptions.AllRuntimeNodes(ctx)
	if err != nil {
		return
	}
	seen := make(map[string]struct{})
	for _, node := range append(manualInventory, subInventory...) {
		if !node.Enabled {
			continue
		}
		key := fmt.Sprintf("%s:%d", node.SourceType, node.SourceNodeID)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		_ = s.EnqueueSpeed(node.SourceType, node.SourceNodeID)
	}
}

func (s *Service) waitForSlot(ctx context.Context, active *int32, limitFn func() int) {
	for {
		limit := limitFn()
		if limit < 1 {
			limit = 1
		}
		if int(atomic.LoadInt32(active)) < limit {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (s *Service) currentLatencyLimit(ctx context.Context) int {
	current, err := s.settingsSvc.Get(ctx)
	if err != nil {
		return 1
	}
	return current.LatencyConcurrency
}

func (s *Service) currentSpeedLimit(ctx context.Context) int {
	current, err := s.settingsSvc.Get(ctx)
	if err != nil {
		return 1
	}
	if current.SpeedConcurrency > pools.MaxProbeSpeedSlots {
		return pools.MaxProbeSpeedSlots
	}
	return current.SpeedConcurrency
}

func (s *Service) runLatency(ctx context.Context, item task) {
	settingsRow, err := s.settingsSvc.Get(ctx)
	if err != nil {
		return
	}
	node, err := s.refreshProbeInventoryAndGetNode(ctx, item.SourceType, item.SourceNodeID, settingsRow.MihomoControllerSecret, settingsRow.LogLevel)
	if err != nil {
		_ = s.setStatus(ctx, item.SourceType, item.SourceNodeID, "unavailable", err.Error())
		return
	}
	_ = s.setStatus(ctx, item.SourceType, item.SourceNodeID, "testing", "")

	taskCtx, cancel := context.WithTimeout(ctx, time.Duration(settingsRow.LatencyTimeoutMS+3000)*time.Millisecond)
	defer cancel()

	delay, err := s.mihomo.Delay(taskCtx, settingsRow.MihomoControllerSecret, runtimeNodeName(node), settingsRow.LatencyTestURL, settingsRow.LatencyTimeoutMS)
	if err != nil {
		_ = s.updateResult(ctx, item, nil, nil, "unavailable", err.Error(), false)
		s.events.Publish("probe.finished", map[string]any{"source_type": item.SourceType, "source_node_id": item.SourceNodeID, "test_type": "latency", "success": false, "error": err.Error()})
		return
	}
	_ = s.updateResult(ctx, item, &delay, nil, "available", "", false)
	s.events.Publish("probe.finished", map[string]any{"source_type": item.SourceType, "source_node_id": item.SourceNodeID, "test_type": "latency", "success": true, "latency_ms": delay})
}

func (s *Service) runSpeed(ctx context.Context, item task) {
	settingsRow, err := s.settingsSvc.Get(ctx)
	if err != nil {
		return
	}
	slotIndex, err := s.acquireSpeedSlot(ctx)
	if err != nil {
		_ = s.setStatus(ctx, item.SourceType, item.SourceNodeID, "unavailable", err.Error())
		return
	}
	defer s.releaseSpeedSlot(slotIndex)
	node, err := s.refreshProbeInventoryAndGetNode(ctx, item.SourceType, item.SourceNodeID, settingsRow.MihomoControllerSecret, settingsRow.LogLevel)
	if err != nil {
		_ = s.setStatus(ctx, item.SourceType, item.SourceNodeID, "unavailable", err.Error())
		return
	}
	_ = s.setStatus(ctx, item.SourceType, item.SourceNodeID, "testing", "")

	taskCtx, cancel := context.WithTimeout(ctx, time.Duration(settingsRow.SpeedTimeoutMS+3000)*time.Millisecond)
	defer cancel()

	if err := s.mihomo.SetProxySelection(taskCtx, settingsRow.MihomoControllerSecret, pools.ProbeSpeedSlotGroupName(slotIndex), runtimeNodeName(node)); err != nil {
		_ = s.updateResult(ctx, item, nil, nil, "unavailable", err.Error(), true)
		s.events.Publish("probe.finished", map[string]any{"source_type": item.SourceType, "source_node_id": item.SourceNodeID, "test_type": "speed", "success": false, "error": err.Error()})
		return
	}
	speedMbps, err := s.measureDownloadSpeed(taskCtx, slotIndex, settingsRow.SpeedTestURL, settingsRow.SpeedMaxBytes)
	if err != nil {
		_ = s.updateResult(ctx, item, nil, nil, "unavailable", err.Error(), true)
		s.events.Publish("probe.finished", map[string]any{"source_type": item.SourceType, "source_node_id": item.SourceNodeID, "test_type": "speed", "success": false, "error": err.Error()})
		return
	}
	_ = s.updateResult(ctx, item, nil, &speedMbps, "available", "", true)
	s.events.Publish("probe.finished", map[string]any{"source_type": item.SourceType, "source_node_id": item.SourceNodeID, "test_type": "speed", "success": true, "speed_mbps": speedMbps})
}

func (s *Service) refreshProbeInventoryAndGetNode(ctx context.Context, sourceType string, sourceNodeID int64, secret, logLevel string) (models.RuntimeNode, error) {
	manualInventory, err := s.manualNodes.AllRuntimeNodes(ctx)
	if err != nil {
		return models.RuntimeNode{}, err
	}
	subscriptionInventory, err := s.subscriptions.AllRuntimeNodes(ctx)
	if err != nil {
		return models.RuntimeNode{}, err
	}
	inventory := append(manualInventory, subscriptionInventory...)
	cfg, err := pools.BuildProbeInventoryConfig(secret, s.mihomo.ProbeControllerAddr(), s.mihomo.ProbeMixedPort(), logLevel, inventory)
	if err != nil {
		return models.RuntimeNode{}, err
	}
	if err := s.mihomo.ApplyProbeConfig(cfg); err != nil {
		return models.RuntimeNode{}, err
	}
	if sourceType == "manual" {
		return s.manualNodes.NodeBySource(ctx, sourceNodeID)
	}
	return s.subscriptions.NodeBySource(ctx, sourceNodeID)
}

func (s *Service) measureDownloadSpeed(ctx context.Context, slotIndex int, targetURL string, maxBytes int64) (float64, error) {
	dialer, err := proxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", pools.ProbeSpeedSlotPort(s.mihomo.ProbeMixedPort(), slotIndex)), nil, proxy.Direct)
	if err != nil {
		return 0, err
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.Dial(network, addr)
		},
	}
	client := &http.Client{
		Timeout:   0,
		Transport: transport,
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return 0, err
	}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	readBytes, err := io.Copy(io.Discard, io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		return 0, err
	}
	elapsed := time.Since(start).Seconds()
	if elapsed <= 0 {
		return 0, fmt.Errorf("invalid speed timing")
	}
	return float64(readBytes*8) / elapsed / 1_000_000, nil
}

func (s *Service) updateResult(ctx context.Context, item task, latency *int64, speed *float64, status, errMsg string, isSpeed bool) error {
	if item.SourceType == "manual" {
		if err := s.manualNodes.UpdateProbeResult(ctx, item.SourceNodeID, latency, speed, status, errMsg, isSpeed); err != nil {
			return err
		}
	} else {
		if err := s.subscriptions.UpdateProbeResult(ctx, item.SourceNodeID, latency, speed, status, errMsg, isSpeed); err != nil {
			return err
		}
	}
	return s.store.ExecContext(ctx, `INSERT INTO probe_history (source_type, source_node_id, test_type, success, latency_ms, speed_mbps, error_message, tested_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		item.SourceType, item.SourceNodeID, item.TestType, boolToInt(errMsg == ""), latency, speed, errMsg, time.Now().UTC(),
	)
}

func (s *Service) setStatus(ctx context.Context, sourceType string, sourceNodeID int64, status, errMsg string) error {
	if sourceType == "manual" {
		return s.manualNodes.SetTransientStatus(ctx, sourceNodeID, status, errMsg)
	}
	return s.subscriptions.SetTransientStatus(ctx, sourceNodeID, status, errMsg)
}

func runtimeNodeName(node models.RuntimeNode) string {
	return pools.RuntimeNodeName(node)
}

func (s *Service) acquireSpeedSlot(ctx context.Context) (int, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case slotIndex := <-s.speedSlots:
		return slotIndex, nil
	}
}

func (s *Service) releaseSpeedSlot(slotIndex int) {
	select {
	case s.speedSlots <- slotIndex:
	default:
	}
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
