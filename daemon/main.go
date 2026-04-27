package main

import (
	"bytes"
	"container/heap"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/cloudflare/circl/sign"
	"github.com/cloudflare/circl/sign/mldsa/mldsa44"
	"github.com/godbus/dbus/v5"
)

var mldsaScheme = mldsa44.Scheme()

// PeerNode is a known peer whose public key we trust for signature verification.
type PeerNode struct {
	Name           string `json:"name"`
	BaseURL        string `json:"base_url"`
	PublicKey      string `json:"public_key"`                 // base64 ed25519 public key (32 bytes)
	MLDSAPublicKey string `json:"mldsa_public_key,omitempty"` // base64 ML-DSA 44 public key (1312 bytes); optional — hybrid enforcement only when set
}

type NodeConfig struct {
	NodeID               string     `json:"node_id"`
	SocialPartitionPct   int        `json:"social_partition_pct"`
	PersonalPartitionPct int        `json:"personal_partition_pct"`
	DaemonPort           int        `json:"daemon_port"`
	APIPort              int        `json:"api_port"`
	DashboardPort        int        `json:"dashboard_port"`
	RateLimitPerMin      int        `json:"rate_limit_per_min"`
	BraveAPIKey          string     `json:"brave_api_key,omitempty"` // Brave Search API key; omitted from config if empty
	PeerNodes            []PeerNode `json:"peer_nodes,omitempty"`
}

func loadConfig() NodeConfig {
	cfg := NodeConfig{
		SocialPartitionPct:   30,
		PersonalPartitionPct: 70,
		DaemonPort:           7700,
		APIPort:              7701,
		DashboardPort:        5000,
		RateLimitPerMin:      60,
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Println("  [!] Could not determine home dir — using defaults")
		return cfg
	}
	path := filepath.Join(home, "vernex", "config", "node.json")

	data, err := os.ReadFile(path)
	if err != nil {
		// Config file doesn't exist — generate ID and write default config
		cfg.NodeID = generateNodeID()
		if out, merr := json.MarshalIndent(cfg, "", "  "); merr == nil {
			os.MkdirAll(filepath.Dir(path), 0755)
			os.WriteFile(path, append(out, '\n'), 0644)
			fmt.Printf("  [✓] Generated Node ID and saved config to %s\n", path)
		} else {
			fmt.Printf("  [!] Config not found at %s — using defaults (ID not persisted)\n", path)
		}
		return cfg
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		fmt.Printf("  [!] Config parse error: %v — using defaults\n", err)
		return cfg
	}

	// Persist a generated node_id back to config so ID survives restarts
	if cfg.NodeID == "" {
		cfg.NodeID = generateNodeID()
		if out, merr := json.MarshalIndent(cfg, "", "  "); merr == nil {
			os.WriteFile(path, append(out, '\n'), 0644)
		}
		fmt.Printf("  [✓] Generated and saved Node ID: %s\n", cfg.NodeID)
	}

	fmt.Printf("  [✓] Config loaded from %s\n", path)
	return cfg
}

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

// --- Token Priority Queue ---

// TokenRequest is a work item submitted to the scheduler.
// Class 1 = community benefit (higher priority, higher contribution delta).
// Class 2 = personal benefit (lower priority, lower contribution delta).
type TokenRequest struct {
	Class          int           `json:"class"`
	Prompt         string        `json:"prompt"`
	Model          string        `json:"model,omitempty"`
	Justification  string        `json:"justification,omitempty"`   // required for Class 2 in Phase 4b
	EstimatedCost  float64       `json:"estimated_cost,omitempty"`  // reserved for Phase 4b
	RuntimeCeiling time.Duration `json:"runtime_ceiling,omitempty"` // reserved for graceful degradation
	seq            int64         // internal FIFO ordering within same class
	responseCh     chan tokenResult // result channel; nil for fire-and-forget
}

type tokenResult struct {
	response          string
	routedTo          string
	model             string
	responseTimeMs    int64
	contributionDelta float64
	err               error
}

// requestQueue implements heap.Interface. Lower class = higher priority.
// Within the same class, lower seq = earlier (FIFO).
type requestQueue []*TokenRequest

func (q requestQueue) Len() int { return len(q) }
func (q requestQueue) Less(i, j int) bool {
	if q[i].Class != q[j].Class {
		return q[i].Class < q[j].Class
	}
	return q[i].seq < q[j].seq
}
func (q requestQueue) Swap(i, j int)  { q[i], q[j] = q[j], q[i] }
func (q *requestQueue) Push(x any)    { *q = append(*q, x.(*TokenRequest)) }
func (q *requestQueue) Pop() any {
	old := *q
	n := len(old)
	item := old[n-1]
	*q = old[:n-1]
	return item
}

// Scheduler serialises requests through a priority queue.
// Class 1 runs before Class 2; within each class requests are FIFO.
type Scheduler struct {
	queue   requestQueue
	mu      sync.Mutex
	cond    *sync.Cond
	seqNext int64 // atomic counter for FIFO ordering
}

func NewScheduler() *Scheduler {
	s := &Scheduler{}
	s.cond = sync.NewCond(&s.mu)
	heap.Init(&s.queue)
	return s
}

func (s *Scheduler) Enqueue(req *TokenRequest) {
	s.mu.Lock()
	req.seq = atomic.AddInt64(&s.seqNext, 1)
	heap.Push(&s.queue, req)
	s.cond.Signal()
	s.mu.Unlock()
}

func (s *Scheduler) depth() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.queue.Len()
}

// classCounts returns [class1count, class2count] without holding the lock long.
func (s *Scheduler) classCounts() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var c1, c2 int
	for _, r := range s.queue {
		if r.Class == 1 {
			c1++
		} else {
			c2++
		}
	}
	return c1, c2
}

// run is the single worker goroutine. It processes one request at a time,
// Class 1 before Class 2, FIFO within each class.
func (s *Scheduler) run(node *Node) {
	for {
		s.mu.Lock()
		for s.queue.Len() == 0 {
			s.cond.Wait()
		}
		req := heap.Pop(&s.queue).(*TokenRequest)
		s.mu.Unlock()

		delta := 2.0
		if req.Class == 2 {
			delta = 1.0
		}

		model := req.Model
		if model == "" {
			model = defaultModel
		}

		start := time.Now()
		llmResp, routedTo, err := routedCallOllama(req.Prompt, model, node.ollamaNodes)
		elapsed := time.Since(start).Milliseconds()

		if err == nil {
			node.mu.Lock()
			node.stats.ContributionScore += delta
			node.stats.TotalConnections++
			node.mu.Unlock()
			fmt.Printf("  [✓] scheduler class=%d  %dms  score=%.1f  routed=%s\n",
				req.Class, elapsed, node.stats.ContributionScore, routedTo)
		} else {
			fmt.Printf("  [!] scheduler class=%d error: %v\n", req.Class, err)
		}

		if req.responseCh != nil {
			req.responseCh <- tokenResult{
				response:          llmResp,
				routedTo:          routedTo,
				model:             model,
				responseTimeMs:    elapsed,
				contributionDelta: delta,
				err:               err,
			}
		}
	}
}

// --- Commons Review ---
// Patent-critical mechanism: the system may SUGGEST upgrading a Class 2 request
// to Class 1, but CANNOT reclassify without explicit user consent.
// The consent requirement is legally significant — do not remove or bypass it.

const commonsReviewTTL = 60 * time.Second

type pendingReview struct {
	req         TokenRequest
	reason      string // why the system suggested upgrade
	expiresAt   time.Time
	webSearched bool
	searchQuery string
}

type commonsAssessment struct {
	CommunityBenefit bool   `json:"community_benefit"`
	Reason           string `json:"reason"`
}

func generateReviewID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return "RVW-" + hex.EncodeToString(b)
}

// assessCommunityBenefit asks Mistral whether a prompt has broad community value.
// Returns (shouldReview, reason, error).
func assessCommunityBenefit(prompt string, nodes []ollamaNode) (bool, string, error) {
	assessment := `You are evaluating whether a user request has broad community or educational value that would benefit many people, not just the individual requester. Consider: Is this information publicly useful? Would multiple users benefit from knowing this?

Request: "` + prompt + `"

Respond only with valid JSON and no other text: {"community_benefit": true or false, "reason": "one sentence explanation"}`

	raw, _, err := routedCallOllama(assessment, defaultModel, nodes)
	if err != nil {
		return false, "", err
	}

	// Extract JSON from response (Mistral may include prose before/after)
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start == -1 || end == -1 || end <= start {
		return false, "", fmt.Errorf("no JSON in assessment response")
	}

	var a commonsAssessment
	if err := json.Unmarshal([]byte(raw[start:end+1]), &a); err != nil {
		return false, "", fmt.Errorf("parse assessment: %w", err)
	}
	return a.CommunityBenefit, a.Reason, nil
}

// --- Multi-node Ollama routing ---

const defaultModel = "mistral:7b-instruct-q4_K_M"

type ollamaNode struct {
	name    string
	baseURL string
}

// buildOllamaNodes constructs the Ollama endpoint list from config.
// Local node is always first; peer nodes are appended from cfg.PeerNodes.
// No IPs are hardcoded in source — all routing is driven by config/node.json.
func buildOllamaNodes(cfg NodeConfig) []ollamaNode {
	nodes := []ollamaNode{{name: "local", baseURL: "http://localhost:11434"}}
	for _, p := range cfg.PeerNodes {
		nodes = append(nodes, ollamaNode{name: p.Name, baseURL: p.BaseURL})
	}
	return nodes
}

type ollamaPsResponse struct {
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
}

// checkOllamaLoad calls /api/ps and returns the number of active models.
// A lower count means lighter load. Returns an error if the node is unreachable.
func checkOllamaLoad(baseURL string) (int, error) {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(baseURL + "/api/ps")
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	var ps ollamaPsResponse
	if err := json.NewDecoder(resp.Body).Decode(&ps); err != nil {
		return 0, err
	}
	return len(ps.Models), nil
}

// selectBestNode returns the available Ollama node with the lowest load.
// Falls back to the first node if none respond (generate call will surface the error).
func selectBestNode(nodes []ollamaNode) ollamaNode {
	best := nodes[0]
	bestLoad := -1
	for _, n := range nodes {
		load, err := checkOllamaLoad(n.baseURL)
		if err != nil {
			continue
		}
		if bestLoad == -1 || load < bestLoad {
			bestLoad = load
			best = n
		}
	}
	return best
}

type ollamaRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
}

type ollamaResponse struct {
	Model    string `json:"model"`
	Response string `json:"response"`
	Done     bool   `json:"done"`
}

// callOllamaAt sends a generate request to a specific Ollama endpoint.
func callOllamaAt(baseURL, model, prompt string) (string, error) {
	body, _ := json.Marshal(ollamaRequest{Model: model, Prompt: prompt, Stream: false})
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Post(baseURL+"/api/generate", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("ollama unreachable at %s: %w", baseURL, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading ollama response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// Treat any non-200 (e.g. 404 model not found, 500 internal) as a node failure
		// so routedCallOllama can fall back to the next node.
		return "", fmt.Errorf("ollama at %s returned HTTP %d: %s", baseURL, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var ollamaResp ollamaResponse
	if err := json.Unmarshal(raw, &ollamaResp); err != nil {
		return "", fmt.Errorf("parsing ollama response: %w", err)
	}
	return ollamaResp.Response, nil
}

// routedCallOllama selects the best available node and calls it.
// If the selected node fails, it tries remaining nodes before returning an error.
// Returns (response, routed_to_name, error).
func routedCallOllama(prompt, model string, nodes []ollamaNode) (string, string, error) {
	primary := selectBestNode(nodes)
	response, err := callOllamaAt(primary.baseURL, model, prompt)
	if err == nil {
		return response, primary.name, nil
	}
	fmt.Printf("  [!] ollama routing: %s failed (%v) — trying fallback nodes\n", primary.name, err)

	for _, n := range nodes {
		if n.baseURL == primary.baseURL {
			continue
		}
		response, ferr := callOllamaAt(n.baseURL, model, prompt)
		if ferr == nil {
			fmt.Printf("  [→] ollama routing: fell back to %s\n", n.name)
			return response, n.name, nil
		}
	}
	return "", "", fmt.Errorf("all ollama nodes unreachable (last error: %w)", err)
}

func generateNodeID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "VRX-" + hex.EncodeToString(b)
}

// nodeIDFromPublicKey derives a deterministic VRX- ID from an ed25519 public key.
// ID = "VRX-" + hex(SHA256(pubKey)[:8])
func nodeIDFromPublicKey(pub ed25519.PublicKey) string {
	h := sha256.Sum256(pub)
	return "VRX-" + hex.EncodeToString(h[:8])
}

// loadOrGenerateKeypair loads an existing ed25519 keypair from configDir or generates
// and persists a new one. node.key holds the 32-byte seed (mode 0600); node.pub holds
// the base64-encoded public key for easy sharing with peer operators.
func loadOrGenerateKeypair(configDir string) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	keyPath := filepath.Join(configDir, "node.key")
	pubPath := filepath.Join(configDir, "node.pub")

	seed, err := os.ReadFile(keyPath)
	if err == nil && len(seed) == ed25519.SeedSize {
		privKey := ed25519.NewKeyFromSeed(seed)
		pubKey := privKey.Public().(ed25519.PublicKey)
		fmt.Printf("  [✓] Keypair loaded from %s\n", keyPath)
		return privKey, pubKey, nil
	}

	// Generate new keypair
	pubKey, privKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, nil, fmt.Errorf("generating keypair: %w", err)
	}

	if err := os.MkdirAll(configDir, 0755); err != nil {
		return nil, nil, fmt.Errorf("creating config dir: %w", err)
	}
	// Write seed (private key material) with strict permissions
	if err := os.WriteFile(keyPath, privKey.Seed(), 0600); err != nil {
		return nil, nil, fmt.Errorf("writing node.key: %w", err)
	}
	// Write base64 public key for sharing with peer operators
	pubB64 := base64.StdEncoding.EncodeToString(pubKey) + "\n"
	if err := os.WriteFile(pubPath, []byte(pubB64), 0644); err != nil {
		return nil, nil, fmt.Errorf("writing node.pub: %w", err)
	}
	fmt.Printf("  [✓] Keypair generated and saved to %s\n", keyPath)
	fmt.Printf("  [✓] Public key: %s\n", strings.TrimSpace(pubB64))
	return privKey, pubKey, nil
}

// loadOrGenerateMLDSAKeypair loads an existing ML-DSA 44 keypair from configDir or
// generates and persists a new one. Private key is stored raw (2560 bytes, mode 0600);
// public key is stored base64-encoded for easy sharing with peer operators.
func loadOrGenerateMLDSAKeypair(configDir string) (sign.PublicKey, sign.PrivateKey, error) {
	keyPath := filepath.Join(configDir, "node.mldsa.key")
	pubPath := filepath.Join(configDir, "node.mldsa.pub")

	privBytes, err := os.ReadFile(keyPath)
	if err == nil && len(privBytes) == mldsaScheme.PrivateKeySize() {
		privKey, err := mldsaScheme.UnmarshalBinaryPrivateKey(privBytes)
		if err != nil {
			return nil, nil, fmt.Errorf("parsing ML-DSA private key: %w", err)
		}
		pubKey, ok := privKey.Public().(sign.PublicKey)
		if !ok {
			return nil, nil, fmt.Errorf("type assertion for ML-DSA public key failed")
		}
		fmt.Printf("  [✓] ML-DSA 44 keypair loaded from %s\n", keyPath)
		return pubKey, privKey, nil
	}

	// Generate new ML-DSA 44 keypair
	pubKey, privKey, err := mldsaScheme.GenerateKey()
	if err != nil {
		return nil, nil, fmt.Errorf("generating ML-DSA keypair: %w", err)
	}
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return nil, nil, fmt.Errorf("creating config dir: %w", err)
	}
	privRaw, err := privKey.MarshalBinary()
	if err != nil {
		return nil, nil, fmt.Errorf("serializing ML-DSA private key: %w", err)
	}
	if err := os.WriteFile(keyPath, privRaw, 0600); err != nil {
		return nil, nil, fmt.Errorf("writing node.mldsa.key: %w", err)
	}
	pubRaw, err := pubKey.MarshalBinary()
	if err != nil {
		return nil, nil, fmt.Errorf("serializing ML-DSA public key: %w", err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(pubRaw) + "\n"
	if err := os.WriteFile(pubPath, []byte(pubB64), 0644); err != nil {
		return nil, nil, fmt.Errorf("writing node.mldsa.pub: %w", err)
	}
	fmt.Printf("  [✓] ML-DSA 44 keypair generated and saved to %s\n", keyPath)
	return pubKey, privKey, nil
}

// --- Rate Limiter ---

// RateLimiter enforces a per-key sliding-window rate limit.
// For signed inter-node requests the key is the peer's node ID;
// for unsigned local requests the key is the source IP address.
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string][]time.Time
	limit   int
	window  time.Duration
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		buckets: make(map[string][]time.Time),
		limit:   limit,
		window:  window,
	}
}

// Allow returns true if the key is under the rate limit, false if it should be rejected.
func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)

	bucket := rl.buckets[key]
	valid := bucket[:0]
	for _, t := range bucket {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	if len(valid) >= rl.limit {
		rl.buckets[key] = valid
		return false
	}
	rl.buckets[key] = append(valid, now)
	return true
}

// PruneEmpty removes buckets with no activity within the window.
// Called periodically to prevent unbounded memory growth from stale keys.
func (rl *RateLimiter) PruneEmpty() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	cutoff := time.Now().Add(-rl.window)
	for key, bucket := range rl.buckets {
		active := false
		for _, t := range bucket {
			if t.After(cutoff) {
				active = true
				break
			}
		}
		if !active {
			delete(rl.buckets, key)
		}
	}
}

// --- Peer Registry ---

const peerLiveTTL = 90 * time.Second

// PeerEntry is a node that has registered itself with this node.
type PeerEntry struct {
	NodeID       string    `json:"node_id"`
	APIURL       string    `json:"api_url"`
	ExternalIP   string    `json:"external_ip,omitempty"`
	ExternalPort int       `json:"external_port,omitempty"`
	LastSeen     time.Time `json:"last_seen"`
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

// rateLimitKey returns the rate-limit key for an incoming request:
// the peer node ID for signed inter-node requests, or the source IP for unsigned local ones.
func rateLimitKey(r *http.Request) string {
	if id := r.Header.Get("X-Vernex-Node-ID"); id != "" {
		return id
	}
	// Strip port from RemoteAddr (host:port or [::1]:port)
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// buildTLSConfig generates a self-signed X.509 certificate from the node's existing
// ed25519 keypair and returns a *tls.Config for the HTTP API server.
// Peer clients use InsecureSkipVerify because trust is enforced by ed25519 payload
// signatures — the distributed CA upgrade (with ML-DSA) will replace this later.
func buildTLSConfig(privKey ed25519.PrivateKey, pubKey ed25519.PublicKey, nodeID string) (*tls.Config, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("serial number: %w", err)
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: nodeID},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, pubKey, privKey)
	if err != nil {
		return nil, fmt.Errorf("creating TLS cert: %w", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{{
			Certificate: [][]byte{certDER},
			PrivateKey:  privKey,
		}},
	}, nil
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
	udpConn          *net.UDPConn // single UDP socket on daemonPort for hole punching
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

// ContextTurn is one message in a conversation history, as sent by the client.
type ContextTurn struct {
	Role    string `json:"role"`    // "user" or "assistant"
	Content string `json:"content"`
}

// buildPromptWithContext prepends formatted conversation history to the current prompt
// so Ollama receives full context without any special token handling.
// Assessment calls always pass the raw prompt; only inference calls use this.
func buildPromptWithContext(ctx []ContextTurn, prompt string) string {
	if len(ctx) == 0 {
		return prompt
	}
	var sb strings.Builder
	sb.WriteString("[INST] ")
	sb.WriteString("Here is our conversation so far:\n")
	for _, turn := range ctx {
		role := "User"
		if turn.Role == "assistant" {
			role = "Assistant"
		}
		sb.WriteString(role + ": " + turn.Content + "\n")
	}
	sb.WriteString("\nCurrent message: ")
	sb.WriteString(prompt)
	sb.WriteString(" [/INST]")
	return sb.String()
}

// braveSearchResponse holds the fields we use from the Brave Search API.
type braveSearchResponse struct {
	Web struct {
		Results []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
		} `json:"results"`
	} `json:"web"`
}

// searchWeb queries the Brave Search API and returns a compact formatted string
// suitable for prepending to a prompt. Falls back gracefully when apiKey is empty.
func searchWeb(query, apiKey string) (string, error) {
	if apiKey == "" {
		return "", fmt.Errorf("Brave API key not configured")
	}
	endpoint := "https://api.search.brave.com/res/v1/web/search?q=" + url.QueryEscape(query) + "&count=5"
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("building Brave request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", apiKey)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("Brave request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Brave API returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var brave braveSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&brave); err != nil {
		return "", fmt.Errorf("Brave parse error: %w", err)
	}
	if len(brave.Web.Results) == 0 {
		return "", fmt.Errorf("Brave returned no results for %q", query)
	}

	var sb strings.Builder
	sb.WriteString("[Web results for: " + query + "]\n")
	for i, r := range brave.Web.Results {
		sb.WriteString(fmt.Sprintf("%d. %s\n   URL: %s\n   %s\n", i+1, r.Title, r.URL, r.Description))
	}
	return sb.String(), nil
}

// needsWebSearch checks the prompt for keywords that signal a need for live/current data.
// Returns (true, query) when detected; query is the prompt itself since Brave handles
// natural language well.
func needsWebSearch(prompt string) (bool, string) {
	lower := strings.ToLower(prompt)
	keywords := []string{
		"today", "current", "latest", "news", "weather", "price",
		"score", " now", "recently", "who is", "what is happening", "stock",
	}
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			return true, prompt
		}
	}
	return false, ""
}

func NewNode(cfg NodeConfig, privKey ed25519.PrivateKey, pubKey ed25519.PublicKey, mldsaPrivKey sign.PrivateKey, mldsaPubKey sign.PublicKey) *Node {
	hostname, _ := os.Hostname()
	limit := cfg.RateLimitPerMin
	if limit <= 0 {
		limit = 60
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
		stats: NodeStats{
			NodeID:            cfg.NodeID,
			Hostname:          hostname,
			StartedAt:         time.Now(),
			Port:              cfg.DaemonPort,
			APIPort:           cfg.APIPort,
			Version:           "0.9.0",
			SocialPartition:   cfg.SocialPartitionPct,
			PersonalPartition: cfg.PersonalPartitionPct,
		},
	}
	n.cachedPublicIP.Store(fetchPublicIP())
	n.externalIP.Store("")
	return n
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

func (n *Node) printBanner() {
	s := n.getStats()
	fmt.Println("╔══════════════════════════════════════╗")
	fmt.Println("║       VERNEX PROTOCOL NODE           ║")
	fmt.Println("║       v0.9.0  — Patent Pending       ║")
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

// connectionType returns "local", "direct", or "relayed" for a registered peer.
// "local"   — peer API URL resolves to a private/loopback address (same LAN, no NAT needed).
// "direct"  — a VERNEX-PUNCH UDP packet was received from this peer's external address.
// "relayed" — connection falls through the TCP daemon relay; hole punch not yet confirmed.
func (n *Node) connectionType(peer PeerEntry) string {
	if u, err := url.Parse(peer.APIURL); err == nil && isPrivateIP(u.Hostname()) {
		return "local"
	}
	n.peerHolesMu.RLock()
	_, direct := n.peerHoles[peer.NodeID]
	n.peerHolesMu.RUnlock()
	if direct {
		return "direct"
	}
	return "relayed"
}

// sendHolePunchPackets sends count UDP "VERNEX-PUNCH" datagrams to addr with 50ms spacing.
func (n *Node) sendHolePunchPackets(addr *net.UDPAddr, count int) {
	if n.udpConn == nil || addr == nil {
		return
	}
	for i := 0; i < count; i++ {
		n.udpConn.WriteToUDP([]byte("VERNEX-PUNCH"), addr) //nolint:errcheck
		time.Sleep(50 * time.Millisecond)
	}
}

// initiatePunch triggers simultaneous UDP hole punching toward peer.
// Signals the peer via /punch-signal so both sides open NAT holes concurrently.
func (n *Node) initiatePunch(peer PeerEntry) {
	if peer.ExternalIP == "" || peer.ExternalPort == 0 {
		return
	}
	target := &net.UDPAddr{IP: net.ParseIP(peer.ExternalIP), Port: peer.ExternalPort}
	extIP, _ := n.externalIP.Load().(string)
	extPort := int(n.externalPort.Load())
	go func() {
		if err := signalPunch(peer.APIURL, extIP, extPort); err != nil {
			fmt.Printf("  [!] punch-signal → %s failed: %v\n", peer.NodeID, err)
		}
	}()
	n.sendHolePunchPackets(target, 5)
}

// signRequest adds hybrid signing headers to an outgoing inter-node HTTP request.
// Message signed: nodeID + "|" + timestamp + "|" + hex(SHA256(body))
// Both ed25519 (classical) and ML-DSA 44 (post-quantum) signatures are attached.
// Peers enforce ML-DSA only when mldsa_public_key is configured (rolling upgrade path).
func (n *Node) signRequest(req *http.Request, body []byte) {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	h := sha256.Sum256(body)
	bodyHash := hex.EncodeToString(h[:])
	msg := n.cfg.NodeID + "|" + ts + "|" + bodyHash

	sig := ed25519.Sign(n.privateKey, []byte(msg))
	req.Header.Set("X-Vernex-Node-ID", n.cfg.NodeID)
	req.Header.Set("X-Vernex-Timestamp", ts)
	req.Header.Set("X-Vernex-Signature", base64.StdEncoding.EncodeToString(sig))

	if n.mldsaPrivKey != nil {
		mldsaSig := mldsaScheme.Sign(n.mldsaPrivKey, []byte(msg), nil)
		req.Header.Set("X-Vernex-Signature-MLDSA", base64.StdEncoding.EncodeToString(mldsaSig))
	}
}

// verifyPeerRequest verifies the hybrid signature on an incoming inter-node request.
// Requests without X-Vernex-Node-ID pass through (local UI / Flask proxy).
// ed25519 is always verified for signed requests. ML-DSA 44 is additionally enforced
// when mldsa_public_key is configured for the peer (rolling upgrade: absent = not yet enrolled).
func (n *Node) verifyPeerRequest(r *http.Request, body []byte) error {
	nodeID := r.Header.Get("X-Vernex-Node-ID")
	if nodeID == "" {
		return nil // unsigned — local request
	}
	tsStr := r.Header.Get("X-Vernex-Timestamp")
	sigB64 := r.Header.Get("X-Vernex-Signature")
	if tsStr == "" || sigB64 == "" {
		return fmt.Errorf("incomplete signing headers")
	}

	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid timestamp")
	}
	age := time.Now().Unix() - ts
	if age > 30 || age < -5 {
		return fmt.Errorf("timestamp out of window (%ds)", age)
	}

	pubKey, err := n.peerPublicKey(nodeID)
	if err != nil {
		return err
	}

	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return fmt.Errorf("invalid ed25519 signature encoding")
	}

	h := sha256.Sum256(body)
	bodyHash := hex.EncodeToString(h[:])
	msg := nodeID + "|" + tsStr + "|" + bodyHash

	if !ed25519.Verify(pubKey, []byte(msg), sig) {
		return fmt.Errorf("ed25519 signature mismatch")
	}

	// ML-DSA 44 hybrid check — only enforced when peer has mldsa_public_key configured.
	// If not configured, we skip silently (rolling upgrade: peer may not have upgraded yet).
	mldsaPub, err := n.peerMLDSAPublicKey(nodeID)
	if err == nil {
		mldsaSigB64 := r.Header.Get("X-Vernex-Signature-MLDSA")
		if mldsaSigB64 == "" {
			return fmt.Errorf("ML-DSA signature required for hybrid-enrolled peer %s", nodeID)
		}
		mldsaSig, err := base64.StdEncoding.DecodeString(mldsaSigB64)
		if err != nil {
			return fmt.Errorf("invalid ML-DSA signature encoding")
		}
		if !mldsaScheme.Verify(mldsaPub, []byte(msg), mldsaSig, nil) {
			return fmt.Errorf("ML-DSA signature mismatch")
		}
	}

	return nil
}

// peerPublicKey looks up a peer's ed25519 public key by deriving its node ID
// from each configured peer's stored public key and comparing.
func (n *Node) peerPublicKey(nodeID string) (ed25519.PublicKey, error) {
	for _, peer := range n.cfg.PeerNodes {
		raw, err := base64.StdEncoding.DecodeString(peer.PublicKey)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			continue
		}
		pub := ed25519.PublicKey(raw)
		if nodeIDFromPublicKey(pub) == nodeID {
			return pub, nil
		}
	}
	return nil, fmt.Errorf("unknown peer node ID: %s", nodeID)
}

// peerMLDSAPublicKey looks up a peer's ML-DSA 44 public key by matching their ed25519-derived
// node ID against configured peers. Returns an error if no ML-DSA key is configured for the peer —
// callers treat this as "ML-DSA not yet enrolled, skip hybrid check" (rolling upgrade path).
func (n *Node) peerMLDSAPublicKey(nodeID string) (sign.PublicKey, error) {
	for _, peer := range n.cfg.PeerNodes {
		if peer.MLDSAPublicKey == "" {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(peer.PublicKey)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			continue
		}
		if nodeIDFromPublicKey(ed25519.PublicKey(raw)) != nodeID {
			continue
		}
		mldsaRaw, err := base64.StdEncoding.DecodeString(peer.MLDSAPublicKey)
		if err != nil {
			return nil, fmt.Errorf("invalid ML-DSA public key encoding for peer %s", peer.Name)
		}
		pub, err := mldsaScheme.UnmarshalBinaryPublicKey(mldsaRaw)
		if err != nil {
			return nil, fmt.Errorf("parsing ML-DSA public key for peer %s: %w", peer.Name, err)
		}
		return pub, nil
	}
	return nil, fmt.Errorf("no ML-DSA public key configured for node %s", nodeID)
}

// takeInhibitorLock takes a systemd-logind sleep/idle inhibitor lock.
// The returned file keeps the lock active until closed.
func takeInhibitorLock() (*os.File, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, fmt.Errorf("D-Bus connect: %w", err)
	}
	defer conn.Close()

	obj := conn.Object("org.freedesktop.login1", "/org/freedesktop/login1")
	var fd dbus.UnixFD
	err = obj.Call("org.freedesktop.login1.Manager.Inhibit", 0,
		"sleep:idle",
		"Vernex Node",
		"Contributing compute to the Vernex Protocol",
		"block",
	).Store(&fd)
	if err != nil {
		return nil, fmt.Errorf("Inhibit call: %w", err)
	}
	return os.NewFile(uintptr(fd), "inhibitor"), nil
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

// stunResponse is the payload returned by the /stun endpoint.
type stunResponse struct {
	ExternalIP   string `json:"external_ip"`
	ExternalPort int    `json:"external_port"`
	NodeID       string `json:"node_id"`
}

// discoverExternalEndpoint calls /stun on each configured peer in turn and returns
// the first successful response. This reveals the node's external IP:port as seen
// through NAT — the foundation for UDP hole punching (Phase 2).
// Returns ("", 0) if no peer responds.
func discoverExternalEndpoint(cfg NodeConfig) (string, int) {
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		},
	}
	for _, peer := range cfg.PeerNodes {
		apiURL, err := peerAPIURL(peer)
		if err != nil {
			continue
		}
		resp, err := client.Get(apiURL + "/stun")
		if err != nil {
			continue
		}
		var stun stunResponse
		err = json.NewDecoder(resp.Body).Decode(&stun)
		resp.Body.Close()
		if err != nil || stun.ExternalIP == "" {
			continue
		}
		fmt.Printf("  [✓] STUN via %s: external endpoint %s:%d\n", peer.Name, stun.ExternalIP, stun.ExternalPort)
		return stun.ExternalIP, stun.ExternalPort
	}
	return "", 0
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

// signalPunch sends a /punch-signal to targetAPIURL, instructing that node to initiate
// UDP hole punching toward punchIP:punchPort simultaneously.
func signalPunch(targetAPIURL, punchIP string, punchPort int) error {
	payload, _ := json.Marshal(map[string]any{
		"punch_ip":   punchIP,
		"punch_port": punchPort,
	})
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		},
	}
	resp, err := client.Post(targetAPIURL+"/punch-signal", "application/json", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
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

// registerWithPeers POSTs our node_id, api_url, and external endpoint to each peer's
// /register endpoint. Failures are logged but not fatal — peers will pick us up on the
// next heartbeat. extIP/extPort are the STUN-discovered external address (may be empty).
func registerWithPeers(cfg NodeConfig, ownAPIPort int, extIP string, extPort int) {
	for _, peer := range cfg.PeerNodes {
		apiURL, err := peerAPIURL(peer)
		if err != nil {
			fmt.Printf("  [!] heartbeat: bad peer URL %q: %v\n", peer.BaseURL, err)
			continue
		}
		host, err := url.Parse(peer.BaseURL)
		if err != nil {
			continue
		}
		localIP := outboundIP(host.Hostname())
		ownURL := fmt.Sprintf("https://%s:%d", localIP, ownAPIPort)

		payload, _ := json.Marshal(map[string]any{
			"node_id":       cfg.NodeID,
			"api_url":       ownURL,
			"external_ip":   extIP,
			"external_port": extPort,
		})
		client := &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
			},
		}
		resp, err := client.Post(apiURL+"/register", "application/json", bytes.NewReader(payload))
		if err != nil {
			fmt.Printf("  [!] heartbeat: could not reach %s (%s): %v\n", peer.Name, apiURL, err)
			continue
		}
		resp.Body.Close()
		fmt.Printf("  [✓] heartbeat: registered with %s (%s)\n", peer.Name, apiURL)
	}
}

// isLocalhost returns true only if the request came from 127.0.0.1 or ::1.
func isLocalhost(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	return host == "127.0.0.1" || host == "::1"
}

// saveConfig writes cfg to ~/vernex/config/node.json.
func saveConfig(cfg NodeConfig) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	path := filepath.Join(home, "vernex", "config", "node.json")
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0644)
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

func main() {
	cfg := loadConfig()

	// Load or generate ed25519 keypair; derive node ID from public key.
	home, _ := os.UserHomeDir()
	configDir := filepath.Join(home, "vernex", "config")
	privKey, pubKey, err := loadOrGenerateKeypair(configDir)
	if err != nil {
		fmt.Printf("  [!] Keypair error: %v — exiting\n", err)
		os.Exit(1)
	}
	derivedID := nodeIDFromPublicKey(pubKey)
	if cfg.NodeID != derivedID {
		fmt.Printf("  [→] Node ID updated: %s → %s (derived from keypair)\n", cfg.NodeID, derivedID)
		cfg.NodeID = derivedID
		cfgPath := filepath.Join(configDir, "node.json")
		if out, merr := json.MarshalIndent(cfg, "", "  "); merr == nil {
			os.WriteFile(cfgPath, append(out, '\n'), 0644)
		}
	}

	mldsaPubKey, mldsaPrivKey, err := loadOrGenerateMLDSAKeypair(configDir)
	if err != nil {
		fmt.Printf("  [!] ML-DSA keypair error: %v — exiting\n", err)
		os.Exit(1)
	}

	tlsCfg, err := buildTLSConfig(privKey, pubKey, cfg.NodeID)
	if err != nil {
		fmt.Printf("  [!] TLS config error: %v — exiting\n", err)
		os.Exit(1)
	}

	node := NewNode(cfg, privKey, pubKey, mldsaPrivKey, mldsaPubKey)
	node.printBanner()

	// Take sleep/idle inhibitor lock via systemd-logind
	inhibitor, err := takeInhibitorLock()
	if err != nil {
		fmt.Printf("  [!] Sleep inhibitor unavailable: %v\n", err)
	} else {
		defer inhibitor.Close()
		fmt.Println("  [✓] Sleep inhibitor active (node will not sleep)")
	}

	// Handle SIGINT/SIGTERM for clean shutdown (releases inhibitor via defer)
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigs
		fmt.Printf("\n  [→] Received %s — shutting down...\n", sig)
		if inhibitor != nil {
			inhibitor.Close()
		}
		os.Exit(0)
	}()

	// Start token scheduler worker
	go node.scheduler.run(node)
	fmt.Println("  [✓] Token scheduler running (Class 1 > Class 2, FIFO)")

	// Expire stale Commons Review entries
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		for range ticker.C {
			now := time.Now()
			node.reviewsMu.Lock()
			for id, rev := range node.reviews {
				if now.After(rev.expiresAt) {
					fmt.Printf("  [~] commons review expired  id=%s — auto-running as Class 2\n", id)
					respCh := make(chan tokenResult, 1)
					node.scheduler.Enqueue(&TokenRequest{
						Class:      rev.req.Class,
						Prompt:     rev.req.Prompt,
						Model:      rev.req.Model,
						responseCh: respCh,
					})
					delete(node.reviews, id)
					go func() { <-respCh }() // drain result; no client waiting
				}
			}
			node.reviewsMu.Unlock()
		}
	}()

	// Start contribution score ticker
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		for range ticker.C {
			node.mu.Lock()
			node.stats.ContributionScore += 0.1
			node.mu.Unlock()
		}
	}()

	// Prune stale rate-limiter buckets every 5 minutes to prevent memory growth
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		for range ticker.C {
			node.rateLimiter.PruneEmpty()
		}
	}()

	// Re-fetch public IP every 10 minutes — it can change on dynamic ISP addresses.
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		for range ticker.C {
			ip := fetchPublicIP()
			node.cachedPublicIP.Store(ip)
			fmt.Printf("  [→] public IP refreshed: %s\n", ip)
		}
	}()

	// Discover external endpoint via STUN, then register with peers every 60 seconds.
	// STUN reveals our external IP:port as seen through NAT — used in heartbeat payloads
	// so peers know how to reach us for future UDP hole punching (Phase 2).
	if len(cfg.PeerNodes) > 0 {
		go func() {
			// Brief delay so our own HTTP server is ready before we hit peers.
			time.Sleep(2 * time.Second)
			extIP, extPort := discoverExternalEndpoint(cfg)
			node.externalIP.Store(extIP)
			node.externalPort.Store(int32(extPort))
			if extIP != "" {
				fmt.Printf("  [✓] External endpoint: %s:%d\n", extIP, extPort)
			} else {
				fmt.Println("  [!] External endpoint: unknown (no bootstrap peer responded to /stun)")
			}
			registerWithPeers(cfg, cfg.APIPort, extIP, extPort)
			ticker := time.NewTicker(60 * time.Second)
			for range ticker.C {
				curIP, _ := node.externalIP.Load().(string)
				registerWithPeers(cfg, cfg.APIPort, curIP, int(node.externalPort.Load()))
			}
		}()
		fmt.Printf("  [✓] Peer heartbeat goroutine started (%d peers)\n", len(cfg.PeerNodes))
	}

	// Start UDP listener on daemonPort for hole-punch packets (coexists with the TCP listener below).
	// Single UDPConn handles both send (WriteToUDP) and receive (ReadFromUDP).
	{
		udpAddr := &net.UDPAddr{Port: cfg.DaemonPort}
		udpConn, uerr := net.ListenUDP("udp", udpAddr)
		if uerr != nil {
			fmt.Printf("  [!] UDP listener failed on port %d: %v (hole punching disabled)\n", cfg.DaemonPort, uerr)
		} else {
			node.udpConn = udpConn
			fmt.Printf("  [✓] UDP listener on port %d (hole punching)\n", cfg.DaemonPort)
			go func() {
				buf := make([]byte, 64)
				for {
					n2, remoteAddr, err := udpConn.ReadFromUDP(buf)
					if err != nil {
						return
					}
					if string(buf[:n2]) != "VERNEX-PUNCH" {
						continue
					}
					for _, p := range node.peerRegistry.LivePeers() {
						if p.ExternalIP == remoteAddr.IP.String() {
							node.peerHolesMu.Lock()
							if _, already := node.peerHoles[p.NodeID]; !already {
								node.peerHoles[p.NodeID] = remoteAddr
								fmt.Printf("  [✓] UDP hole punched  peer=%s  addr=%s\n", p.NodeID, remoteAddr)
							}
							node.peerHolesMu.Unlock()
							break
						}
					}
				}
			}()
		}
	}

	// Auto-punch goroutine: every 5 minutes attempt direct connections to RELAYED peers.
	// Skips LOCAL peers (same LAN, no NAT) and peers with no known external endpoint.
	go func() {
		time.Sleep(15 * time.Second)
		for {
			for _, p := range node.peerRegistry.LivePeers() {
				if node.connectionType(p) == "relayed" && p.ExternalIP != "" {
					fmt.Printf("  [→] auto-punch: initiating toward %s (%s:%d)\n", p.NodeID, p.ExternalIP, p.ExternalPort)
					go node.initiatePunch(p)
				}
			}
			time.Sleep(5 * time.Minute)
		}
	}()

	// Start HTTP status API on port 7701
	go func() {
		http.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			pubIP, _ := node.cachedPublicIP.Load().(string)
			extIP, _ := node.externalIP.Load().(string)
			livePeers := node.peerRegistry.LivePeers()
			directCount, localCount := 0, 0
			for _, p := range livePeers {
				switch node.connectionType(p) {
				case "direct":
					directCount++
				case "local":
					localCount++
				}
			}
			json.NewEncoder(w).Encode(statusResponse{
				NodeStats:    node.getStats(),
				IPAddress:    outboundIP("8.8.8.8"),
				Gateway:      defaultGateway(),
				PublicIP:     pubIP,
				ExternalIP:   extIP,
				ExternalPort: int(node.externalPort.Load()),
				DirectPeers:  directCount,
				LocalPeers:   localCount,
			})
		})
		http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, "ok")
		})
		// /stun — returns the caller's external IP:port as seen by this node.
		// No auth required. Used by compute nodes to discover their NAT-translated endpoint.
		http.HandleFunc("/stun", func(w http.ResponseWriter, r *http.Request) {
			host, portStr, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				http.Error(w, "could not parse remote address", http.StatusInternalServerError)
				return
			}
			port, _ := strconv.Atoi(portStr)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(stunResponse{
				ExternalIP:   host,
				ExternalPort: port,
				NodeID:       node.cfg.NodeID,
			})
		})
		http.HandleFunc("/submit", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "POST required", http.StatusMethodNotAllowed)
				return
			}
			// Read body once so it's available for both signature verification and JSON decode.
			rawBody, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "failed to read body", http.StatusBadRequest)
				return
			}
			if err := node.verifyPeerRequest(r, rawBody); err != nil {
				fmt.Printf("  [!] /submit rejected — signature: %v\n", err)
				http.Error(w, "unauthorized: "+err.Error(), http.StatusUnauthorized)
				return
			}
			key := rateLimitKey(r)
			if !node.rateLimiter.Allow(key) {
				fmt.Printf("  [!] /submit rate limited  key=%s\n", key)
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			var incoming struct {
				Class         int           `json:"class"`
				Prompt        string        `json:"prompt"`
				Model         string        `json:"model"`
				Justification string        `json:"justification"`
				EstimatedCost float64       `json:"estimated_cost"`
				Context       []ContextTurn `json:"context"`
			}
			if err := json.NewDecoder(bytes.NewReader(rawBody)).Decode(&incoming); err != nil {
				http.Error(w, "invalid JSON", http.StatusBadRequest)
				return
			}
			if incoming.Class != 1 && incoming.Class != 2 {
				http.Error(w, "class must be 1 or 2", http.StatusBadRequest)
				return
			}
			if incoming.Prompt == "" {
				http.Error(w, "prompt required", http.StatusBadRequest)
				return
			}
			if incoming.Model == "" {
				incoming.Model = defaultModel
			}
			// Web search: augment the prompt with live results when the message
			// signals a need for current data. Assessment still uses the raw prompt.
			webSearched := false
			searchQuery := ""
			augmentedPrompt := incoming.Prompt
			if detected, query := needsWebSearch(incoming.Prompt); detected {
				if results, serr := searchWeb(query, node.cfg.BraveAPIKey); serr == nil {
					augmentedPrompt = results + "\n" + incoming.Prompt
					webSearched = true
					searchQuery = query
					fmt.Printf("  [🔍] web search: %q\n", query)
				} else {
					fmt.Printf("  [!] web search failed: %v — proceeding without\n", serr)
				}
			}

			// Build the prompt that will actually be sent to Ollama.
			// Assessment (assessCommunityBenefit) uses incoming.Prompt alone so that
			// community-benefit scoring is based on the current message, not history.
			effectivePrompt := buildPromptWithContext(incoming.Context, augmentedPrompt)

			// Commons Review: Class 2 requests are assessed for community benefit.
			// If benefit is detected the system SUGGESTS an upgrade to Class 1.
			// The request is held pending explicit user consent — it is NEVER
			// reclassified automatically. This is the core patented constraint.
			if incoming.Class == 2 {
				benefit, reason, err := assessCommunityBenefit(incoming.Prompt, node.ollamaNodes)
				if err != nil {
					fmt.Printf("  [!] commons assessment error: %v — proceeding as Class 2\n", err)
				} else if benefit {
					reviewID := generateReviewID()
					node.reviewsMu.Lock()
					node.reviews[reviewID] = pendingReview{
						req: TokenRequest{
							Class:         2,
							Prompt:        effectivePrompt,
							Model:         incoming.Model,
							Justification: incoming.Justification,
							EstimatedCost: incoming.EstimatedCost,
						},
						reason:      reason,
						expiresAt:   time.Now().Add(commonsReviewTTL),
						webSearched: webSearched,
						searchQuery: searchQuery,
					}
					node.reviewsMu.Unlock()

					fmt.Printf("  [↑] commons review triggered  id=%s  reason=%q\n", reviewID, reason)
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(map[string]any{
						"status":         "commons_review",
						"review_id":      reviewID,
						"original_class": 2,
						"suggestion":     reason,
						"message":        "This request may qualify as Class 1 (community benefit). Upgrading increases priority and contribution delta. POST to /consent with review_id and upgrade=true to accept, upgrade=false to proceed as Class 2.",
						"expires_in_sec": int(commonsReviewTTL.Seconds()),
					})
					return
				}
			}

			// No commons review — enqueue directly at submitted class.
			respCh := make(chan tokenResult, 1)
			node.scheduler.Enqueue(&TokenRequest{
				Class:         incoming.Class,
				Prompt:        effectivePrompt,
				Model:         incoming.Model,
				Justification: incoming.Justification,
				EstimatedCost: incoming.EstimatedCost,
				responseCh:    respCh,
			})

			result := <-respCh
			if result.err != nil {
				http.Error(w, fmt.Sprintf("LLM error: %v", result.err), http.StatusServiceUnavailable)
				return
			}

			s := node.getStats()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(SubmitResponse{
				NodeID:            s.NodeID,
				Hostname:          s.Hostname,
				Class:             incoming.Class,
				Model:             result.model,
				RoutedTo:          result.routedTo,
				Response:          result.response,
				ResponseTimeMs:    result.responseTimeMs,
				ContributionDelta: result.contributionDelta,
				ContributionScore: s.ContributionScore,
				WebSearched:       webSearched,
				SearchQuery:       searchQuery,
			})
		})

		// /consent — explicit user consent for Commons Review upgrade.
		// upgrade=true  → execute as Class 1 (community)
		// upgrade=false → execute as Class 2 (personal, original class)
		// The system NEVER upgrades without this explicit consent call.
		http.HandleFunc("/consent", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "POST required", http.StatusMethodNotAllowed)
				return
			}
			key := rateLimitKey(r)
			if !node.rateLimiter.Allow(key) {
				fmt.Printf("  [!] /consent rate limited  key=%s\n", key)
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			var req struct {
				ReviewID string `json:"review_id"`
				Upgrade  *bool  `json:"upgrade"` // pointer — must be explicitly provided
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "invalid JSON", http.StatusBadRequest)
				return
			}
			if req.ReviewID == "" {
				http.Error(w, "review_id required", http.StatusBadRequest)
				return
			}
			if req.Upgrade == nil {
				http.Error(w, "upgrade (true/false) required — explicit consent is mandatory", http.StatusBadRequest)
				return
			}

			node.reviewsMu.Lock()
			review, ok := node.reviews[req.ReviewID]
			if ok {
				delete(node.reviews, req.ReviewID)
			}
			node.reviewsMu.Unlock()

			if !ok {
				http.Error(w, "review_id not found or expired", http.StatusNotFound)
				return
			}
			if time.Now().After(review.expiresAt) {
				http.Error(w, "review expired — resubmit request", http.StatusGone)
				return
			}

			finalClass := review.req.Class // default: keep as Class 2
			if *req.Upgrade {
				finalClass = 1 // user consented to Class 1 upgrade
				fmt.Printf("  [✓] consent: upgraded to Class 1  id=%s\n", req.ReviewID)
			} else {
				fmt.Printf("  [→] consent: kept as Class 2  id=%s\n", req.ReviewID)
			}

			respCh := make(chan tokenResult, 1)
			node.scheduler.Enqueue(&TokenRequest{
				Class:         finalClass,
				Prompt:        review.req.Prompt,
				Model:         review.req.Model,
				Justification: review.req.Justification,
				EstimatedCost: review.req.EstimatedCost,
				responseCh:    respCh,
			})

			result := <-respCh
			if result.err != nil {
				http.Error(w, fmt.Sprintf("LLM error: %v", result.err), http.StatusServiceUnavailable)
				return
			}

			s := node.getStats()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(SubmitResponse{
				NodeID:            s.NodeID,
				Hostname:          s.Hostname,
				Class:             finalClass,
				Model:             result.model,
				RoutedTo:          result.routedTo,
				Response:          result.response,
				ResponseTimeMs:    result.responseTimeMs,
				ContributionDelta: result.contributionDelta,
				ContributionScore: s.ContributionScore,
				WebSearched:       review.webSearched,
				SearchQuery:       review.searchQuery,
			})
		})

		http.HandleFunc("/queue", func(w http.ResponseWriter, r *http.Request) {
			c1, c2 := node.scheduler.classCounts()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"depth":   c1 + c2,
				"class_1": c1,
				"class_2": c2,
			})
		})

		// /register — peer heartbeat registration.
		// Accepts {"node_id": "VRX-xxx", "api_url": "https://ip:7701", "external_ip": "...", "external_port": 12345}.
		// No signature required: registration is informational; trust is enforced at /submit.
		http.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "POST required", http.StatusMethodNotAllowed)
				return
			}
			var req struct {
				NodeID       string `json:"node_id"`
				APIURL       string `json:"api_url"`
				ExternalIP   string `json:"external_ip,omitempty"`
				ExternalPort int    `json:"external_port,omitempty"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "invalid JSON", http.StatusBadRequest)
				return
			}
			if req.NodeID == "" || req.APIURL == "" {
				http.Error(w, "node_id and api_url required", http.StatusBadRequest)
				return
			}
			node.peerRegistry.Register(PeerEntry{
				NodeID:       req.NodeID,
				APIURL:       req.APIURL,
				ExternalIP:   req.ExternalIP,
				ExternalPort: req.ExternalPort,
				LastSeen:     time.Now(),
			})
			fmt.Printf("  [↔] registered peer  id=%s  ext=%s:%d\n", req.NodeID, req.ExternalIP, req.ExternalPort)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "ok", "node_id": node.cfg.NodeID})
		})

		// /peers — returns all peers that have sent a heartbeat within the last 90 seconds.
		http.HandleFunc("/peers", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			peers := node.peerRegistry.LivePeers()
			type peerOut struct {
				NodeID         string `json:"node_id"`
				APIURL         string `json:"api_url"`
				ExternalIP     string `json:"external_ip,omitempty"`
				ExternalPort   int    `json:"external_port,omitempty"`
				ConnectionType string `json:"connection_type"`
				LastSeenAgoSec int64  `json:"last_seen_ago_sec"`
			}
			out := make([]peerOut, 0, len(peers))
			for _, p := range peers {
				out = append(out, peerOut{
					NodeID:         p.NodeID,
					APIURL:         p.APIURL,
					ExternalIP:     p.ExternalIP,
					ExternalPort:   p.ExternalPort,
					ConnectionType: node.connectionType(p),
					LastSeenAgoSec: int64(time.Since(p.LastSeen).Seconds()),
				})
			}
			json.NewEncoder(w).Encode(out)
		})

		// /trust-request — any node can POST to request joining the trusted peer list.
		// Rate limited to 3 requests per IP per hour. No auth required.
		http.HandleFunc("/trust-request", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "POST required", http.StatusMethodNotAllowed)
				return
			}
			srcHost, _, _ := net.SplitHostPort(r.RemoteAddr)
			if !node.trustRateLimiter.Allow(srcHost) {
				http.Error(w, "rate limit exceeded (3/hour per IP)", http.StatusTooManyRequests)
				return
			}
			var req struct {
				NodeID         string `json:"node_id"`
				PublicKey      string `json:"public_key"`
				MLDSAPublicKey string `json:"mldsa_public_key,omitempty"`
				APIUrl         string `json:"api_url"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "invalid JSON", http.StatusBadRequest)
				return
			}
			if req.NodeID == "" || req.PublicKey == "" || req.APIUrl == "" {
				http.Error(w, "node_id, public_key, and api_url required", http.StatusBadRequest)
				return
			}
			entry := TrustRequest{
				NodeID:         req.NodeID,
				PublicKey:      req.PublicKey,
				MLDSAPublicKey: req.MLDSAPublicKey,
				APIUrl:         req.APIUrl,
				RequestedAt:    time.Now(),
				SourceIP:       srcHost,
			}
			node.trustMu.Lock()
			// Upsert: replace existing entry for the same node_id
			replaced := false
			for i := range node.trustRequests {
				if node.trustRequests[i].NodeID == req.NodeID {
					node.trustRequests[i] = entry
					replaced = true
					break
				}
			}
			if !replaced {
				node.trustRequests = append(node.trustRequests, entry)
			}
			node.trustMu.Unlock()
			fmt.Printf("  [↑] trust request  id=%s  src=%s\n", req.NodeID, srcHost)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "pending", "message": "trust request received — awaiting operator approval"})
		})

		// /trust-requests — localhost only — returns pending trust requests.
		http.HandleFunc("/trust-requests", func(w http.ResponseWriter, r *http.Request) {
			if !isLocalhost(r) {
				http.Error(w, "localhost only", http.StatusForbidden)
				return
			}
			node.trustMu.Lock()
			out := make([]TrustRequest, len(node.trustRequests))
			copy(out, node.trustRequests)
			node.trustMu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			json.NewEncoder(w).Encode(out)
		})

		// /trust-approve — localhost only — adds node to cfg.PeerNodes and saves config.
		http.HandleFunc("/trust-approve", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "POST required", http.StatusMethodNotAllowed)
				return
			}
			if !isLocalhost(r) {
				http.Error(w, "localhost only", http.StatusForbidden)
				return
			}
			var req struct {
				NodeID string `json:"node_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "invalid JSON", http.StatusBadRequest)
				return
			}
			node.trustMu.Lock()
			var found *TrustRequest
			filtered := node.trustRequests[:0]
			for i := range node.trustRequests {
				if node.trustRequests[i].NodeID == req.NodeID {
					tr := node.trustRequests[i]
					found = &tr
				} else {
					filtered = append(filtered, node.trustRequests[i])
				}
			}
			node.trustRequests = filtered
			node.trustMu.Unlock()
			if found == nil {
				http.Error(w, "trust request not found", http.StatusNotFound)
				return
			}
			newPeer := PeerNode{
				Name:           found.NodeID,
				BaseURL:        deriveOllamaURL(found.APIUrl),
				PublicKey:      found.PublicKey,
				MLDSAPublicKey: found.MLDSAPublicKey,
			}
			node.mu.Lock()
			node.cfg.PeerNodes = append(node.cfg.PeerNodes, newPeer)
			node.ollamaNodes = buildOllamaNodes(node.cfg)
			cfgSnap := node.cfg
			node.mu.Unlock()
			if err := saveConfig(cfgSnap); err != nil {
				fmt.Printf("  [!] trust-approve: save config failed: %v\n", err)
			}
			fmt.Printf("  [✓] trust approved  id=%s  url=%s\n", found.NodeID, found.APIUrl)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "approved", "node_id": found.NodeID})
		})

		// /trust-deny — localhost only — removes from queue without adding to peers.
		http.HandleFunc("/trust-deny", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "POST required", http.StatusMethodNotAllowed)
				return
			}
			if !isLocalhost(r) {
				http.Error(w, "localhost only", http.StatusForbidden)
				return
			}
			var req struct {
				NodeID string `json:"node_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "invalid JSON", http.StatusBadRequest)
				return
			}
			node.trustMu.Lock()
			filtered := node.trustRequests[:0]
			found := false
			for i := range node.trustRequests {
				if node.trustRequests[i].NodeID == req.NodeID {
					found = true
				} else {
					filtered = append(filtered, node.trustRequests[i])
				}
			}
			node.trustRequests = filtered
			node.trustMu.Unlock()
			if !found {
				http.Error(w, "trust request not found", http.StatusNotFound)
				return
			}
			fmt.Printf("  [✗] trust denied  id=%s\n", req.NodeID)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "denied", "node_id": req.NodeID})
		})

		// /punch-request — bootstrap coordination endpoint.
		// Looks up both peers in the registry and signals each to punch toward the other.
		http.HandleFunc("/punch-request", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "POST required", http.StatusMethodNotAllowed)
				return
			}
			var req struct {
				InitiatorID string `json:"initiator_id"`
				TargetID    string `json:"target_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "invalid JSON", http.StatusBadRequest)
				return
			}
			initiator, ok1 := node.peerRegistry.GetByNodeID(req.InitiatorID)
			target, ok2 := node.peerRegistry.GetByNodeID(req.TargetID)
			if !ok1 || !ok2 {
				http.Error(w, "one or both peers not registered", http.StatusNotFound)
				return
			}
			go func() {
				if err := signalPunch(initiator.APIURL, target.ExternalIP, target.ExternalPort); err != nil {
					fmt.Printf("  [!] punch-request: signal to initiator %s failed: %v\n", req.InitiatorID, err)
				}
			}()
			go func() {
				if err := signalPunch(target.APIURL, initiator.ExternalIP, initiator.ExternalPort); err != nil {
					fmt.Printf("  [!] punch-request: signal to target %s failed: %v\n", req.TargetID, err)
				}
			}()
			fmt.Printf("  [↔] punch-request: coordinating %s ↔ %s\n", req.InitiatorID, req.TargetID)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "punching"})
		})

		// /punch-signal — node receives instruction to punch toward a peer's external endpoint.
		http.HandleFunc("/punch-signal", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "POST required", http.StatusMethodNotAllowed)
				return
			}
			var req struct {
				PunchIP   string `json:"punch_ip"`
				PunchPort int    `json:"punch_port"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "invalid JSON", http.StatusBadRequest)
				return
			}
			if req.PunchIP == "" || req.PunchPort == 0 {
				http.Error(w, "punch_ip and punch_port required", http.StatusBadRequest)
				return
			}
			target := &net.UDPAddr{IP: net.ParseIP(req.PunchIP), Port: req.PunchPort}
			go node.sendHolePunchPackets(target, 5)
			fmt.Printf("  [→] punch-signal: punching toward %s:%d\n", req.PunchIP, req.PunchPort)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "punching"})
		})

		fmt.Printf("  [✓] Dashboard API (HTTPS) listening on port %d\n", node.cfg.APIPort)
		srv := &http.Server{
			Addr:      fmt.Sprintf(":%d", node.cfg.APIPort),
			TLSConfig: tlsCfg,
		}
		// ListenAndServeTLS with empty strings uses certs already in TLSConfig.
		srv.ListenAndServeTLS("", "")
	}()

	// Start P2P listener
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", node.cfg.DaemonPort))
	if err != nil {
		fmt.Printf("  ERROR: Could not bind to port %d — %v\n", node.cfg.DaemonPort, err)
		os.Exit(1)
	}
	defer listener.Close()

	fmt.Printf("  [✓] P2P listener on port %d\n", node.cfg.DaemonPort)
	fmt.Println("  [✓] Node is online — waiting for connections...")
	fmt.Println("  Press Ctrl+C to stop\n")

	for {
		conn, err := listener.Accept()
		if err != nil {
			continue
		}
		go handleConnection(conn, node)
	}
}

func handleConnection(conn net.Conn, node *Node) {
	defer conn.Close()
	remote := conn.RemoteAddr().String()
	node.recordConnection()
	s := node.getStats()

	fmt.Printf("  [→] Connection from %s  (total: %d  score: %.1f)\n",
		remote, s.TotalConnections, s.ContributionScore)

	response, _ := json.MarshalIndent(s, "  ", "  ")
	conn.Write(response)
	conn.Write([]byte("\n"))
}
