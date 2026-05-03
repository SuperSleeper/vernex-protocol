package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"sync"
	"time"
)

const peerLiveTTL = 90 * time.Second

// PeerEntry is a node that has registered itself with this node.
type PeerEntry struct {
	NodeID        string          `json:"node_id"`
	APIURL        string          `json:"api_url"`
	ExternalIP    string          `json:"external_ip,omitempty"`
	ExternalPort  int             `json:"external_port,omitempty"`
	LastSeen      time.Time       `json:"last_seen"`
	PushedStatus  json.RawMessage `json:"pushed_status,omitempty"` // last /status payload pushed on heartbeat
	CertVerified  bool            `json:"cert_verified"`            // true if VernexCert chain verified via TrustStore
	TrustApproved bool            `json:"trust_approved"`           // true if operator approved or CA-verified
}

// PeerRegistry holds in-memory heartbeat registrations from peer nodes.
type PeerRegistry struct {
	mu      sync.RWMutex
	entries map[string]PeerEntry // keyed by node_id
}

func NewPeerRegistry() *PeerRegistry {
	return &PeerRegistry{entries: make(map[string]PeerEntry)}
}

func (pr *PeerRegistry) Register(entry PeerEntry) {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	pr.entries[entry.NodeID] = entry
}

// GetByNodeID returns the registry entry for a specific node ID.
func (pr *PeerRegistry) GetByNodeID(nodeID string) (PeerEntry, bool) {
	pr.mu.RLock()
	defer pr.mu.RUnlock()
	e, ok := pr.entries[nodeID]
	return e, ok
}

// SetCertVerified atomically sets CertVerified on a stored entry without a
// read-modify-Register round-trip that could race with a concurrent heartbeat.
// Returns false if the peer is no longer in the registry.
func (pr *PeerRegistry) SetCertVerified(nodeID string, verified bool) bool {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	e, ok := pr.entries[nodeID]
	if !ok {
		return false
	}
	e.CertVerified = verified
	pr.entries[nodeID] = e
	return true
}

// SetTrustApproved atomically marks a peer as operator-approved or CA-verified.
// Returns false if the peer is no longer in the registry.
func (pr *PeerRegistry) SetTrustApproved(nodeID string, approved bool) bool {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	e, ok := pr.entries[nodeID]
	if !ok {
		return false
	}
	e.TrustApproved = approved
	pr.entries[nodeID] = e
	return true
}

// LivePeers returns all entries whose last heartbeat was within peerLiveTTL.
func (pr *PeerRegistry) LivePeers() []PeerEntry {
	pr.mu.RLock()
	defer pr.mu.RUnlock()
	cutoff := time.Now().Add(-peerLiveTTL)
	var out []PeerEntry
	for _, e := range pr.entries {
		if e.LastSeen.After(cutoff) {
			out = append(out, e)
		}
	}
	return out
}

// isPrivateIP returns true when ip falls within RFC 1918 private ranges or loopback.
// Used to classify same-LAN peers that don't need NAT hole punching.
func isPrivateIP(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, cidr := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "127.0.0.0/8"} {
		_, network, _ := net.ParseCIDR(cidr)
		if network.Contains(parsed) {
			return true
		}
	}
	return false
}

// peerAPIURL derives a peer's daemon API URL from its Ollama base_url by
// replacing the port with 7701 and switching to https.
func peerAPIURL(peer PeerNode) (string, error) {
	u, err := url.Parse(peer.BaseURL)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("https://%s:7701", u.Hostname()), nil
}

// deriveOllamaURL converts a Vernex API URL (https://IP:7701) to the
// corresponding Ollama endpoint (http://IP:11434).
func deriveOllamaURL(apiURL string) string {
	u, err := url.Parse(apiURL)
	if err != nil {
		return "http://localhost:11434"
	}
	return fmt.Sprintf("http://%s:11434", u.Hostname())
}

// registerWithPeers POSTs our node_id, api_url, external endpoint, and current status
// to each peer's /register endpoint. Failures are logged but not fatal — peers will pick
// us up on the next heartbeat. extIP/extPort are the STUN-discovered external address (may be empty).
// Static peer_nodes entries whose hostname is already reachable via mDNS are skipped —
// the mDNS heartbeat loop covers them, avoiding redundant WAN traffic for LAN peers.
func registerWithPeers(node *Node, extIP string, extPort int) {
	cfg := node.cfg
	ownAPIPort := cfg.APIPort
	statusJSON, _ := json.Marshal(getOwnStatus(node))

	// Snapshot dynamic peers once; also build a hostname set for LAN-skip logic below.
	node.dynamicPeersMu.RLock()
	dynamicSnapshot := make(map[string]string, len(node.dynamicPeers))
	mDNSHosts := make(map[string]struct{}, len(node.dynamicPeers))
	for id, u := range node.dynamicPeers {
		dynamicSnapshot[id] = u
		if parsed, err := url.Parse(u); err == nil {
			mDNSHosts[parsed.Hostname()] = struct{}{}
		}
	}
	node.dynamicPeersMu.RUnlock()

	// Also include the public_ip from PushedStatus of mDNS-discovered peers.
	// Handles the case where the static peer_nodes entry uses a node's public IP
	// (e.g. 76.244.40.49) while mDNS discovers the same node at its LAN IP
	// (e.g. 172.17.0.132). PushedStatus is pre-populated by fetchAndStorePeerStatus
	// on mDNS discovery, so this mapping is available on the first heartbeat tick.
	for _, p := range node.peerRegistry.LivePeers() {
		if _, mDNS := dynamicSnapshot[p.NodeID]; !mDNS || len(p.PushedStatus) == 0 {
			continue
		}
		var s struct {
			PublicIP string `json:"public_ip"`
		}
		if json.Unmarshal(p.PushedStatus, &s) == nil && s.PublicIP != "" {
			mDNSHosts[s.PublicIP] = struct{}{}
		}
	}

	for _, peer := range cfg.PeerNodes {
		apiURL, err := peerAPIURL(peer)
		if err != nil {
			fmt.Printf("  [!] heartbeat: bad peer URL %q: %v\n", peer.BaseURL, err)
			continue
		}
		// Peer already on the LAN via mDNS — the dynamic loop below will heartbeat it.
		if parsed, err := url.Parse(apiURL); err == nil {
			if _, lanPeer := mDNSHosts[parsed.Hostname()]; lanPeer {
				fmt.Printf("  [~] heartbeat: skipping %s — already reachable via mDNS\n", peer.Name)
				continue
			}
		}
		host, err := url.Parse(peer.BaseURL)
		if err != nil {
			continue
		}
		ownIP := outboundIP(host.Hostname())
		if extIP != "" && extIP != ownIP {
			ownIP = extIP // use external IP when behind NAT so bootstrap can reach us
		}
		ownURL := fmt.Sprintf("https://%s:%d", ownIP, ownAPIPort)

		payload, _ := json.Marshal(map[string]any{
			"node_id":       cfg.NodeID,
			"api_url":       ownURL,
			"external_ip":   extIP,
			"external_port": extPort,
			"status":        json.RawMessage(statusJSON),
		})
		client := node.buildPeerTLSClient(5 * time.Second)
		resp, err := client.Post(apiURL+"/register", "application/json", bytes.NewReader(payload))
		if err != nil {
			fmt.Printf("  [!] heartbeat: could not reach %s (%s): %v\n", peer.Name, apiURL, err)
			continue
		}
		resp.Body.Close()
		fmt.Printf("  [✓] heartbeat: registered with %s (%s)\n", peer.Name, apiURL)
	}

	// Heartbeat to mDNS-discovered peers.
	for peerNodeID, peerURL := range dynamicSnapshot {
		parsed, err := url.Parse(peerURL)
		if err != nil {
			continue
		}
		ownIP := outboundIP(parsed.Hostname())
		if extIP != "" && extIP != ownIP {
			ownIP = extIP
		}
		ownURL := fmt.Sprintf("https://%s:%d", ownIP, ownAPIPort)
		payload, _ := json.Marshal(map[string]any{
			"node_id":       cfg.NodeID,
			"api_url":       ownURL,
			"external_ip":   extIP,
			"external_port": extPort,
			"status":        json.RawMessage(statusJSON),
		})
		client := node.buildPeerTLSClient(5 * time.Second)
		resp, err := client.Post(peerURL+"/register", "application/json", bytes.NewReader(payload))
		if err != nil {
			fmt.Printf("  [!] heartbeat: could not reach mDNS peer %s (%s): %v\n", peerNodeID, peerURL, err)
			continue
		}
		resp.Body.Close()
		fmt.Printf("  [✓] heartbeat: registered with mDNS peer %s (%s)\n", peerNodeID, peerURL)
	}
}

// startHeartbeatLoop discovers the external endpoint via STUN then registers with peers
// every 60 seconds. Only started when peer_nodes is non-empty.
func startHeartbeatLoop(node *Node) {
	if len(node.cfg.PeerNodes) == 0 {
		return
	}
	go func() {
		// Delay allows mDNS discovery + fetchAndStorePeerStatus to complete so
		// the public_ip mapping is available before the first heartbeat fires.
		time.Sleep(15 * time.Second)
		extIP, extPort := discoverExternalEndpoint(node.cfg, node.trustStore)
		node.externalIP.Store(extIP)
		node.externalPort.Store(int32(extPort))
		if extIP != "" {
			fmt.Printf("  [✓] External endpoint: %s:%d\n", extIP, extPort)
		} else {
			fmt.Println("  [!] External endpoint: unknown (no bootstrap peer responded to /stun)")
		}
		registerWithPeers(node, extIP, extPort)
		ticker := time.NewTicker(60 * time.Second)
		for range ticker.C {
			curIP, _ := node.externalIP.Load().(string)
			registerWithPeers(node, curIP, int(node.externalPort.Load()))
		}
	}()
	fmt.Printf("  [✓] Peer heartbeat goroutine started (%d peers)\n", len(node.cfg.PeerNodes))
}
