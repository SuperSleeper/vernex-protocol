package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloudflare/circl/sign"

	vernexca "vernex/daemon/ca"
)

type NodeStats struct {
	NodeID            string    `json:"node_id"`
	Hostname          string    `json:"hostname"`
	StartedAt         time.Time `json:"started_at"`
	Port              int       `json:"port"`
	APIPort           int       `json:"api_port"`
	Version           string    `json:"version"`
	UptimeSeconds     int64     `json:"uptime_seconds"`
	TotalConnections  int       `json:"total_connections"`
	ContributionScore float64   `json:"contribution_score"`
	SocialPartition   int       `json:"social_partition_pct"`
	PersonalPartition int       `json:"personal_partition_pct"`
	QueueDepth        int       `json:"queue_depth"`
}

// TrustRequest is an inbound request from a new node asking to join the trusted peer list.
type TrustRequest struct {
	NodeID         string    `json:"node_id"`
	PublicKey      string    `json:"public_key"`
	MLDSAPublicKey string    `json:"mldsa_public_key,omitempty"` // ML-DSA 44 public key; optional
	APIUrl         string    `json:"api_url"`
	RequestedAt    time.Time `json:"requested_at"`
	SourceIP       string    `json:"source_ip"`
}

// statusResponse is the /status payload. It embeds the static NodeStats fields
// and adds ip_address, gateway, and public_ip which are computed live on each call
// (ip_address and gateway are instant local calls; public_ip comes from the cache).
type statusResponse struct {
	NodeStats
	IPAddress    string `json:"ip_address"`
	Gateway      string `json:"gateway"`
	PublicIP     string `json:"public_ip"`
	ExternalIP   string `json:"external_ip,omitempty"`
	ExternalPort int    `json:"external_port,omitempty"`
	DirectPeers  int    `json:"direct_peers"`
	LocalPeers   int    `json:"local_peers"`
}

type SubmitRequest struct {
	Prompt string `json:"prompt"`
	Class  int    `json:"class"`
	Model  string `json:"model"`
}

type SubmitResponse struct {
	NodeID            string  `json:"node_id"`
	Hostname          string  `json:"hostname"`
	Class             int     `json:"class"`
	Model             string  `json:"model"`
	RoutedTo          string  `json:"routed_to"`
	Response          string  `json:"response"`
	ResponseTimeMs    int64   `json:"response_time_ms"`
	ContributionDelta float64 `json:"contribution_delta"`
	ContributionScore float64 `json:"contribution_score"`
	WebSearched       bool    `json:"web_searched"`
	SearchQuery       string  `json:"search_query,omitempty"`
}

type Node struct {
	cfg              NodeConfig
	stats            NodeStats
	mu               sync.RWMutex
	scheduler        *Scheduler
	reviews          map[string]pendingReview
	reviewsMu        sync.Mutex
	privateKey       ed25519.PrivateKey
	publicKey        ed25519.PublicKey
	mldsaPrivKey     sign.PrivateKey // ML-DSA 44 private key; nil-safe
	mldsaPubKey      sign.PublicKey  // ML-DSA 44 public key
	ollamaNodes      []ollamaNode
	rateLimiter      *RateLimiter
	peerRegistry     *PeerRegistry
	cachedPublicIP   atomic.Value // string; refreshed every 10 min by a background goroutine
	externalIP       atomic.Value // string; own external IP as seen by bootstrap peers via /stun
	externalPort     atomic.Int32 // own external port as seen by bootstrap peers via /stun
	trustRequests    []TrustRequest
	trustMu          sync.Mutex
	trustRateLimiter *RateLimiter    // 3 requests per IP per hour
	peerHoles        map[string]*net.UDPAddr // keyed by node_id; set when UDP punch packet received
	peerHolesMu      sync.RWMutex
	udpConn          *net.UDPConn  // single UDP socket on daemonPort for hole punching
	lastLANIP        atomic.Value  // string; last known LAN IP — watchdog detects changes
	lastPublicIP     atomic.Value  // string; last known public IP — watchdog detects changes
	mdnsDiscovered   map[string]bool   // node_id → true if found via mDNS browse
	mdnsDiscoveredMu sync.RWMutex
	dynamicPeers     map[string]string // node_id → api_url; mDNS-discovered peers for heartbeat
	dynamicPeersMu   sync.RWMutex
	trustStore       *vernexca.TrustStore
}

// outboundIP returns the local IP address used to reach peerHost.
// Uses a UDP "dial" — no data is sent; the OS just picks the right interface.
func outboundIP(peerHost string) string {
	conn, err := net.Dial("udp", peerHost+":80")
	if err != nil {
		return "unknown"
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

// defaultGateway returns the default gateway IP by parsing `ip route show default`.
func defaultGateway() string {
	out, err := exec.Command("ip", "route", "show", "default").Output()
	if err != nil {
		return "unknown"
	}
	// Output: "default via 192.168.1.1 dev eth0 ..."
	fields := strings.Fields(string(out))
	for i, f := range fields {
		if f == "via" && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	return "unknown"
}

// fetchPublicIP queries api.ipify.org for the node's public IP address.
// Returns "behind NAT" on any failure so the status endpoint always has a value.
func fetchPublicIP() string {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("https://api.ipify.org")
	if err != nil {
		return "behind NAT"
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		return "behind NAT"
	}
	ip := strings.TrimSpace(string(body))
	if net.ParseIP(ip) == nil {
		return "behind NAT"
	}
	return ip
}

func NewNode(cfg NodeConfig, configDir string, privKey ed25519.PrivateKey, pubKey ed25519.PublicKey, mldsaPrivKey sign.PrivateKey, mldsaPubKey sign.PublicKey) *Node {
	hostname, _ := os.Hostname()
	limit := cfg.RateLimitPerMin
	if limit <= 0 {
		limit = 60
	}
	ts, tsErr := vernexca.LoadTrustStore(configDir)
	if tsErr != nil {
		fmt.Printf("  [!] TrustStore load warning: %v\n", tsErr)
	}
	n := &Node{
		cfg:              cfg,
		scheduler:        NewScheduler(),
		reviews:          make(map[string]pendingReview),
		privateKey:       privKey,
		publicKey:        pubKey,
		mldsaPrivKey:     mldsaPrivKey,
		mldsaPubKey:      mldsaPubKey,
		ollamaNodes:      buildOllamaNodes(cfg),
		rateLimiter:      NewRateLimiter(limit, 60*time.Second),
		peerRegistry:     NewPeerRegistry(),
		trustRateLimiter: NewRateLimiter(3, 60*time.Minute),
		peerHoles:        make(map[string]*net.UDPAddr),
		mdnsDiscovered:   make(map[string]bool),
		dynamicPeers:     make(map[string]string),
		trustStore: ts,
		stats: NodeStats{
			NodeID:            cfg.NodeID,
			Hostname:          hostname,
			StartedAt:         time.Now(),
			Port:              cfg.DaemonPort,
			APIPort:           cfg.APIPort,
			Version:           "0.11.5",
			SocialPartition:   cfg.SocialPartitionPct,
			PersonalPartition: cfg.PersonalPartitionPct,
		},
	}
	initialPublicIP := fetchPublicIP()
	n.cachedPublicIP.Store(initialPublicIP)
	n.externalIP.Store("")
	n.lastLANIP.Store(outboundIP("8.8.8.8"))
	n.lastPublicIP.Store(initialPublicIP)
	return n
}

// buildPeerTLSClient returns an http.Client for inter-node calls using the node's
// TrustStore for TOFU TLS verification. Centralizes all peer HTTP client construction.
func (n *Node) buildPeerTLSClient(timeout time.Duration) *http.Client {
	return n.trustStore.NewTLSClient(timeout)
}

func (n *Node) recordConnection() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.stats.TotalConnections++
	n.stats.ContributionScore += 0.5
}

func (n *Node) getStats() NodeStats {
	n.mu.RLock()
	defer n.mu.RUnlock()
	s := n.stats
	s.UptimeSeconds = int64(time.Since(s.StartedAt).Seconds())
	s.QueueDepth = n.scheduler.depth()
	return s
}

// getOwnStatus builds a full statusResponse without an HTTP round-trip.
// Used to embed own status in heartbeat payloads so bootstrap nodes see our stats
// even when we're behind NAT and not directly reachable inbound.
func getOwnStatus(n *Node) statusResponse {
	pubIP, _ := n.cachedPublicIP.Load().(string)
	extIP, _ := n.externalIP.Load().(string)
	livePeers := n.peerRegistry.LivePeers()
	directCount, localCount := 0, 0
	for _, p := range livePeers {
		switch n.connectionType(p) {
		case "direct":
			directCount++
		case "local":
			localCount++
		}
	}
	return statusResponse{
		NodeStats:    n.getStats(),
		IPAddress:    outboundIP("8.8.8.8"),
		Gateway:      defaultGateway(),
		PublicIP:     pubIP,
		ExternalIP:   extIP,
		ExternalPort: int(n.externalPort.Load()),
		DirectPeers:  directCount,
		LocalPeers:   localCount,
	}
}

func (n *Node) printBanner() {
	s := n.getStats()
	fmt.Println("╔══════════════════════════════════════╗")
	fmt.Println("║       VERNEX PROTOCOL NODE           ║")
	fmt.Println("║       v0.11.5 — Patent Pending       ║")
	fmt.Println("╚══════════════════════════════════════╝")
	fmt.Printf("\n  Node ID   : %s\n", s.NodeID)
	fmt.Printf("  Ed25519   : %s\n", base64.StdEncoding.EncodeToString(n.publicKey))
	mldsaRaw, _ := n.mldsaPubKey.MarshalBinary()
	mldsaB64 := base64.StdEncoding.EncodeToString(mldsaRaw)
	if len(mldsaB64) > 32 {
		fmt.Printf("  ML-DSA 44 : %s…\n", mldsaB64[:32])
	} else {
		fmt.Printf("  ML-DSA 44 : %s\n", mldsaB64)
	}
	fmt.Printf("  Hostname  : %s\n", s.Hostname)
	fmt.Printf("  Port      : %d  (P2P)\n", s.Port)
	fmt.Printf("  HTTP      : %d (dashboard API)\n", s.APIPort)
	fmt.Printf("  Started   : %s\n", s.StartedAt.Format("2006-01-02 15:04:05"))
	fmt.Printf("  Partition : %d%% personal / %d%% social\n\n",
		s.PersonalPartition, s.SocialPartition)
}

// startContributionTicker increments the contribution score by 0.1 every 10 seconds.
func startContributionTicker(node *Node) {
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		for range ticker.C {
			node.mu.Lock()
			node.stats.ContributionScore += 0.1
			node.mu.Unlock()
		}
	}()
}

// startPublicIPRefresher re-fetches the public IP every 10 minutes.
func startPublicIPRefresher(node *Node) {
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		for range ticker.C {
			ip := fetchPublicIP()
			node.cachedPublicIP.Store(ip)
			fmt.Printf("  [→] public IP refreshed: %s\n", ip)
		}
	}()
}
