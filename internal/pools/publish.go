package pools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"super-proxy-pool/internal/models"
)

type PublishBundle struct {
	ProdConfig  []byte
	ProbeConfig []byte
}

const MaxProbeSpeedSlots = 4

func BuildPublishBundle(secret, prodController, probeController string, probeMixedPort int, testURL, logLevel string, poolList []models.ProxyPool, members map[int64][]models.RuntimeNode, inventory []models.RuntimeNode) (PublishBundle, error) {
	prodConfig, err := buildProdConfig(secret, prodController, testURL, logLevel, poolList, members)
	if err != nil {
		return PublishBundle{}, err
	}
	probeConfig, err := buildProbeConfig(secret, probeController, probeMixedPort, logLevel, inventory)
	if err != nil {
		return PublishBundle{}, err
	}
	return PublishBundle{
		ProdConfig:  prodConfig,
		ProbeConfig: probeConfig,
	}, nil
}

func BuildProbeInventoryConfig(secret, probeController string, probeMixedPort int, logLevel string, inventory []models.RuntimeNode) ([]byte, error) {
	return buildProbeConfig(secret, probeController, probeMixedPort, logLevel, inventory)
}

func RuntimeNodeName(node models.RuntimeNode) string {
	return runtimeNodeName(node)
}

func ProbeSpeedSlotGroupName(slotIndex int) string {
	return probeSpeedSlotGroupName(slotIndex)
}

func ProbeSpeedSlotPort(probeMixedPort, slotIndex int) int {
	return probeSpeedSlotPort(probeMixedPort, slotIndex)
}

func buildProdConfig(secret, controller, testURL, logLevel string, poolList []models.ProxyPool, members map[int64][]models.RuntimeNode) ([]byte, error) {
	type listener struct {
		Name   string              `yaml:"name"`
		Type   string              `yaml:"type"`
		Listen string              `yaml:"listen"`
		Port   int                 `yaml:"port"`
		Proxy  string              `yaml:"proxy"`
		Users  []map[string]string `yaml:"users,omitempty"`
	}

	root := map[string]any{
		"mode":                "rule",
		"log-level":           normalizeLogLevel(logLevel),
		"allow-lan":           true,
		"external-controller": controller,
		"secret":              secret,
		"proxies":             []map[string]any{},
		"proxy-groups":        []map[string]any{},
		"listeners":           []listener{},
		"rules":               []string{"MATCH,DIRECT"},
	}
	seenProxyNames := make(map[string]struct{})

	for _, pool := range poolList {
		if !pool.Enabled {
			continue
		}
		groupName := poolGroupName(pool.ID)
		groupMembers := members[pool.ID]
		memberNames := make([]string, 0, len(groupMembers))
		for _, node := range groupMembers {
			if !node.Enabled {
				continue
			}
			name := runtimeNodeName(node)
			memberNames = append(memberNames, name)
			if _, ok := seenProxyNames[name]; ok {
				continue
			}
			payload := normalizedNodeMap(node)
			payload["name"] = name
			root["proxies"] = append(root["proxies"].([]map[string]any), payload)
			seenProxyNames[name] = struct{}{}
		}
		if len(memberNames) == 0 {
			memberNames = []string{"DIRECT"}
		}
		groupType, strategy := strategyToMihomo(pool.Strategy)
		group := map[string]any{
			"name":    groupName,
			"type":    groupType,
			"proxies": memberNames,
		}
		if strategy != "" {
			group["strategy"] = strategy
		}
		if shouldAttachHealthCheck(pool) {
			group["url"] = testURL
			group["interval"] = 300
			group["lazy"] = true
		}
		root["proxy-groups"] = append(root["proxy-groups"].([]map[string]any), group)

		listenerCfg := listener{
			Name:   fmt.Sprintf("pool-%d", pool.ID),
			Type:   pool.Protocol,
			Listen: pool.ListenHost,
			Port:   pool.ListenPort,
			Proxy:  groupName,
		}
		if pool.AuthEnabled {
			listenerCfg.Users = []map[string]string{{
				"username": pool.AuthUsername,
				"password": pool.AuthPasswordSecret,
			}}
		}
		root["listeners"] = append(root["listeners"].([]listener), listenerCfg)
	}

	return yaml.Marshal(root)
}

func buildProbeConfig(secret, controller string, probeMixedPort int, logLevel string, inventory []models.RuntimeNode) ([]byte, error) {
	type listener struct {
		Name   string `yaml:"name"`
		Type   string `yaml:"type"`
		Listen string `yaml:"listen"`
		Port   int    `yaml:"port"`
		Proxy  string `yaml:"proxy"`
		UDP    bool   `yaml:"udp,omitempty"`
	}

	root := map[string]any{
		"mode":                "global",
		"log-level":           normalizeLogLevel(logLevel),
		"allow-lan":           false,
		"mixed-port":          probeMixedPort,
		"external-controller": controller,
		"secret":              secret,
		"proxies":             []map[string]any{},
		"proxy-groups":        []map[string]any{},
		"listeners":           []listener{},
		"rules":               []string{"MATCH,GLOBAL"},
	}

	names := make([]string, 0, len(inventory))
	for _, node := range inventory {
		if !node.Enabled {
			continue
		}
		name := runtimeNodeName(node)
		payload := normalizedNodeMap(node)
		payload["name"] = name
		root["proxies"] = append(root["proxies"].([]map[string]any), payload)
		names = append(names, name)
	}
	sort.Strings(names)
	root["proxy-groups"] = []map[string]any{
		{
			"name":    "GLOBAL",
			"type":    "select",
			"proxies": append(names, "DIRECT"),
		},
	}
	for slotIndex := 0; slotIndex < MaxProbeSpeedSlots; slotIndex++ {
		root["proxy-groups"] = append(root["proxy-groups"].([]map[string]any), map[string]any{
			"name":    probeSpeedSlotGroupName(slotIndex),
			"type":    "select",
			"proxies": append(names, "DIRECT"),
		})
		root["listeners"] = append(root["listeners"].([]listener), listener{
			Name:   fmt.Sprintf("speed-slot-%d", slotIndex+1),
			Type:   "socks",
			Listen: "127.0.0.1",
			Port:   probeSpeedSlotPort(probeMixedPort, slotIndex),
			Proxy:  probeSpeedSlotGroupName(slotIndex),
			UDP:    false,
		})
	}
	return yaml.Marshal(root)
}

func normalizedNodeMap(node models.RuntimeNode) map[string]any {
	var payload map[string]any
	if err := json.Unmarshal([]byte(node.NormalizedJSON), &payload); err != nil || len(payload) == 0 {
		payload = map[string]any{
			"type":   node.Protocol,
			"server": node.Server,
			"port":   node.Port,
			"name":   node.DisplayName,
		}
	}
	return payload
}

func strategyToMihomo(strategy string) (groupType string, lbStrategy string) {
	switch strategy {
	case "lowest_latency":
		return "url-test", ""
	case "failover":
		return "fallback", ""
	case "sticky":
		return "load-balance", "sticky-sessions"
	default:
		return "load-balance", "round-robin"
	}
}

func runtimeNodeName(node models.RuntimeNode) string {
	name := strings.TrimSpace(node.DisplayName)
	name = strings.ReplaceAll(name, " ", "-")
	name = strings.ReplaceAll(name, "/", "-")
	return fmt.Sprintf("%s-%d-%s", node.SourceType, node.SourceNodeID, name)
}

func poolGroupName(poolID int64) string {
	return fmt.Sprintf("pool-group-%d", poolID)
}

func probeSpeedSlotGroupName(slotIndex int) string {
	return fmt.Sprintf("SPEED_SLOT_%d", slotIndex+1)
}

func probeSpeedSlotPort(probeMixedPort, slotIndex int) int {
	return probeMixedPort + slotIndex + 1
}

func normalizeLogLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "trace", "debug", "info", "warning", "warn", "error", "silent":
		return strings.ToLower(strings.TrimSpace(level))
	default:
		return "info"
	}
}

func shouldAttachHealthCheck(pool models.ProxyPool) bool {
	switch pool.Strategy {
	case "failover", "lowest_latency":
		return true
	case "sticky", "round_robin", "":
		return pool.FailoverEnabled
	default:
		return pool.FailoverEnabled
	}
}
