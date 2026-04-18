package nodes

import (
	"encoding/base64"
	"testing"
)

func TestParseSSNode(t *testing.T) {
	node, err := ParseNodeURI("ss://" + base64.RawURLEncoding.EncodeToString([]byte("aes-128-gcm:pass@example.com:8388")) + "#hk")
	if err != nil {
		t.Fatalf("ParseNodeURI() error = %v", err)
	}
	if node.Protocol != "ss" || node.Server != "example.com" || node.Port != 8388 || node.DisplayName != "hk" {
		t.Fatalf("unexpected ss node: %+v", node)
	}
}

func TestParseVMessNode(t *testing.T) {
	payload := `{"v":"2","ps":"vmess-node","add":"vmess.example.com","port":"443","id":"uuid"}`
	node, err := ParseNodeURI("vmess://" + base64.StdEncoding.EncodeToString([]byte(payload)))
	if err != nil {
		t.Fatalf("ParseNodeURI() error = %v", err)
	}
	if node.Protocol != "vmess" || node.Server != "vmess.example.com" || node.Port != 443 {
		t.Fatalf("unexpected vmess node: %+v", node)
	}
}

func TestParseYAMLNodes(t *testing.T) {
	raw := `
proxies:
  - name: direct-a
    type: trojan
    server: demo.example.com
    port: 443
    password: secret
`
	nodes, errs := ParseRawNodes(raw)
	if len(errs) != 0 {
		t.Fatalf("ParseRawNodes() errs = %v", errs)
	}
	if len(nodes) != 1 || nodes[0].Protocol != "trojan" {
		t.Fatalf("unexpected yaml parse result: %+v", nodes)
	}
}

func TestParseSimpleURLNodePreservesProtocolType(t *testing.T) {
	node, err := ParseNodeURI("vless://uuid@example.org:8443?type=tcp&security=reality#node-b")
	if err != nil {
		t.Fatalf("ParseNodeURI() error = %v", err)
	}
	if node.Protocol != "vless" {
		t.Fatalf("Protocol = %q, want %q", node.Protocol, "vless")
	}
	if got := node.Normalized["type"]; got != "vless" {
		t.Fatalf("normalized type = %#v, want %q", got, "vless")
	}
	if got := node.Normalized["network"]; got != "tcp" {
		t.Fatalf("normalized network = %#v, want %q", got, "tcp")
	}
}
