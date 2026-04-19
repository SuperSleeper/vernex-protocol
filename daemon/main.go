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
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"
)

// PeerNode is a known peer whose public key we trust for signature verification.
type PeerNode struct {
	Name      string `json:"name"`
	BaseURL   string `json:"base_url"`
	PublicKey string `json:"public_key"` // base64-encoded ed25519 public key (32 bytes)
}

type NodeConfig struct {
	NodeID               string     `json:"node_id"`
	SocialPartitionPct   int        `json:"social_partition_pct"`
	PersonalPartitionPct int        `json:"personal_partition_pct"`
	DaemonPort           int        `json:"daemon_port"`
	APIPort              int        `json:"api_port"`
	DashboardPort        int        `json:"dashboard_port"`
	RateLimitPerMin      int        `json:"rate_limit_per_min"`
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

const defaultModel = "mistral"

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

// --- Rate Limiter ---

// RateLimiter enforces a per-key sliding-window rate limit.
// For signed inter-node requests the key is the peer's node ID;
// for unsigned local requests the key is the source IP address.
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string][]time.Time
	limit   int // max requests per 60-second window
}

func NewRateLimiter(limit int) *RateLimiter {
	return &RateLimiter{
		buckets: make(map[string][]time.Time),
		limit:   limit,
	}
}

// Allow returns true if the key is under the rate limit, false if it should be rejected.
func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-60 * time.Second)

	bucket := rl.buckets[key]
	// Prune timestamps outside the sliding window
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

// PruneEmpty removes buckets that have no timestamps in the last 60 seconds.
// Called periodically to prevent unbounded memory growth from stale keys.
func (rl *RateLimiter) PruneEmpty() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	cutoff := time.Now().Add(-60 * time.Second)
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

type Node struct {
	cfg         NodeConfig
	stats       NodeStats
	mu          sync.RWMutex
	scheduler   *Scheduler
	reviews     map[string]pendingReview
	reviewsMu   sync.Mutex
	privateKey  ed25519.PrivateKey
	publicKey   ed25519.PublicKey
	ollamaNodes []ollamaNode
	rateLimiter *RateLimiter
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

// ddgResponse holds the fields we use from the DuckDuckGo Instant Answers API.
type ddgResponse struct {
	AbstractText  string `json:"AbstractText"`
	Answer        string `json:"Answer"`
	RelatedTopics []struct {
		Text string `json:"Text"` // category grouping entries have no Text; they're skipped
	} `json:"RelatedTopics"`
}

// searchWeb queries the DuckDuckGo Instant Answers API (no key required) and returns
// a compact formatted string suitable for prepending to a prompt.
func searchWeb(query string) (string, error) {
	endpoint := "https://api.duckduckgo.com/?q=" + url.QueryEscape(query) + "&format=json&no_html=1&skip_disambig=1"
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(endpoint)
	if err != nil {
		return "", fmt.Errorf("DDG request failed: %w", err)
	}
	defer resp.Body.Close()

	var ddg ddgResponse
	if err := json.NewDecoder(resp.Body).Decode(&ddg); err != nil {
		return "", fmt.Errorf("DDG parse error: %w", err)
	}

	var sb strings.Builder
	sb.WriteString("[Web results for: " + query + "]\n")

	if ddg.Answer != "" {
		sb.WriteString("Answer: " + ddg.Answer + "\n")
	}
	if ddg.AbstractText != "" {
		sb.WriteString("Summary: " + ddg.AbstractText + "\n")
	}

	count := 0
	for _, t := range ddg.RelatedTopics {
		if t.Text == "" {
			continue // skip category-grouping entries
		}
		sb.WriteString("Related: " + t.Text + "\n")
		count++
		if count == 3 {
			break
		}
	}

	if ddg.Answer == "" && ddg.AbstractText == "" && count == 0 {
		return "", fmt.Errorf("DDG returned no usable results for %q", query)
	}
	return sb.String(), nil
}

// needsWebSearch checks the prompt for keywords that signal a need for live/current data.
// Returns (true, query) when detected; query is the prompt itself since DDG handles
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

func NewNode(cfg NodeConfig, privKey ed25519.PrivateKey, pubKey ed25519.PublicKey) *Node {
	hostname, _ := os.Hostname()
	limit := cfg.RateLimitPerMin
	if limit <= 0 {
		limit = 60
	}
	return &Node{
		cfg:         cfg,
		scheduler:   NewScheduler(),
		reviews:     make(map[string]pendingReview),
		privateKey:  privKey,
		publicKey:   pubKey,
		ollamaNodes: buildOllamaNodes(cfg),
		rateLimiter: NewRateLimiter(limit),
		stats: NodeStats{
			NodeID:            cfg.NodeID,
			Hostname:          hostname,
			StartedAt:         time.Now(),
			Port:              cfg.DaemonPort,
			APIPort:           cfg.APIPort,
			Version:           "0.7.0",
			SocialPartition:   cfg.SocialPartitionPct,
			PersonalPartition: cfg.PersonalPartitionPct,
		},
	}
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
	fmt.Println("║       v0.7.0  — Patent Pending       ║")
	fmt.Println("╚══════════════════════════════════════╝")
	fmt.Printf("\n  Node ID   : %s\n", s.NodeID)
	fmt.Printf("  Public Key: %s\n", base64.StdEncoding.EncodeToString(n.publicKey))
	fmt.Printf("  Hostname  : %s\n", s.Hostname)
	fmt.Printf("  Port      : %d  (P2P)\n", s.Port)
	fmt.Printf("  HTTP      : %d (dashboard API)\n", s.APIPort)
	fmt.Printf("  Started   : %s\n", s.StartedAt.Format("2006-01-02 15:04:05"))
	fmt.Printf("  Partition : %d%% personal / %d%% social\n\n",
		s.PersonalPartition, s.SocialPartition)
}

// signRequest adds ed25519 signing headers to an outgoing inter-node HTTP request.
// Message signed: nodeID + "|" + timestamp + "|" + hex(SHA256(body))
func (n *Node) signRequest(req *http.Request, body []byte) {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	h := sha256.Sum256(body)
	bodyHash := hex.EncodeToString(h[:])
	msg := n.cfg.NodeID + "|" + ts + "|" + bodyHash
	sig := ed25519.Sign(n.privateKey, []byte(msg))
	req.Header.Set("X-Vernex-Node-ID", n.cfg.NodeID)
	req.Header.Set("X-Vernex-Timestamp", ts)
	req.Header.Set("X-Vernex-Signature", base64.StdEncoding.EncodeToString(sig))
}

// verifyPeerRequest verifies the ed25519 signature on an incoming inter-node request.
// Requests without X-Vernex-Node-ID are treated as local UI / Flask proxy calls and
// pass through without verification. Returns a non-nil error if headers are present
// but invalid (bad timestamp, unknown peer, bad signature).
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
		return fmt.Errorf("invalid signature encoding")
	}

	h := sha256.Sum256(body)
	bodyHash := hex.EncodeToString(h[:])
	msg := nodeID + "|" + tsStr + "|" + bodyHash
	if !ed25519.Verify(pubKey, []byte(msg), sig) {
		return fmt.Errorf("signature mismatch")
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

	tlsCfg, err := buildTLSConfig(privKey, pubKey, cfg.NodeID)
	if err != nil {
		fmt.Printf("  [!] TLS config error: %v — exiting\n", err)
		os.Exit(1)
	}

	node := NewNode(cfg, privKey, pubKey)
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

	// Start HTTP status API on port 7701
	go func() {
		http.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			json.NewEncoder(w).Encode(node.getStats())
		})
		http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, "ok")
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
				if results, serr := searchWeb(query); serr == nil {
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
