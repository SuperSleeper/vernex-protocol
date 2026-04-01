package main

import (
	"bytes"
	"container/heap"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"
)

type NodeConfig struct {
	NodeID               string `json:"node_id"`
	SocialPartitionPct   int    `json:"social_partition_pct"`
	PersonalPartitionPct int    `json:"personal_partition_pct"`
	DaemonPort           int    `json:"daemon_port"`
	APIPort              int    `json:"api_port"`
	DashboardPort        int    `json:"dashboard_port"`
}

func loadConfig() NodeConfig {
	cfg := NodeConfig{
		SocialPartitionPct:   30,
		PersonalPartitionPct: 70,
		DaemonPort:           7700,
		APIPort:              7701,
		DashboardPort:        5000,
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Println("  [!] Could not determine home dir — using defaults")
		return cfg
	}
	path := filepath.Join(home, "vernex", "config", "node.json")

	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Printf("  [!] Config not found at %s — using defaults\n", path)
		return cfg
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		fmt.Printf("  [!] Config parse error: %v — using defaults\n", err)
		return cfg
	}

	// Persist a generated node_id back to config so ID survives restarts
	if cfg.NodeID == "" {
		cfg.NodeID = generateNodeID()
		if out, err := json.MarshalIndent(cfg, "", "  "); err == nil {
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
	Justification  string        `json:"justification,omitempty"`   // required for Class 2 in Phase 4b
	EstimatedCost  float64       `json:"estimated_cost,omitempty"`  // reserved for Phase 4b
	RuntimeCeiling time.Duration `json:"runtime_ceiling,omitempty"` // reserved for graceful degradation
	seq        int64             // internal FIFO ordering within same class
	responseCh chan tokenResult   // result channel; nil for fire-and-forget
}

type tokenResult struct {
	response          string
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
func (q requestQueue) Swap(i, j int)       { q[i], q[j] = q[j], q[i] }
func (q *requestQueue) Push(x any)         { *q = append(*q, x.(*TokenRequest)) }
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

		start := time.Now()
		llmResp, err := callOllama(req.Prompt)
		elapsed := time.Since(start).Milliseconds()

		if err == nil {
			node.mu.Lock()
			node.stats.ContributionScore += delta
			node.stats.TotalConnections++
			node.mu.Unlock()
			fmt.Printf("  [✓] scheduler class=%d  %dms  score=%.1f\n",
				req.Class, elapsed, node.stats.ContributionScore)
		} else {
			fmt.Printf("  [!] scheduler class=%d error: %v\n", req.Class, err)
		}

		if req.responseCh != nil {
			req.responseCh <- tokenResult{
				response:          llmResp,
				responseTimeMs:    elapsed,
				contributionDelta: delta,
				err:               err,
			}
		}
	}
}

type Node struct {
	cfg       NodeConfig
	stats     NodeStats
	mu        sync.RWMutex
	scheduler *Scheduler
}

type SubmitRequest struct {
	Prompt string `json:"prompt"`
	Class  int    `json:"class"`
}

type SubmitResponse struct {
	NodeID            string  `json:"node_id"`
	Hostname          string  `json:"hostname"`
	Class             int     `json:"class"`
	Model             string  `json:"model"`
	Response          string  `json:"response"`
	ResponseTimeMs    int64   `json:"response_time_ms"`
	ContributionDelta float64 `json:"contribution_delta"`
	ContributionScore float64 `json:"contribution_score"`
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

const ollamaURL = "http://localhost:11434/api/generate"
const ollamaModel = "mistral"

func callOllama(prompt string) (string, error) {
	body, _ := json.Marshal(ollamaRequest{Model: ollamaModel, Prompt: prompt, Stream: false})
	resp, err := http.Post(ollamaURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("ollama unreachable: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading ollama response: %w", err)
	}
	var ollamaResp ollamaResponse
	if err := json.Unmarshal(raw, &ollamaResp); err != nil {
		return "", fmt.Errorf("parsing ollama response: %w", err)
	}
	return ollamaResp.Response, nil
}

func generateNodeID() string {
	bytes := make([]byte, 8)
	rand.Read(bytes)
	return "VRX-" + hex.EncodeToString(bytes)
}

func NewNode(cfg NodeConfig) *Node {
	hostname, _ := os.Hostname()
	return &Node{
		cfg:       cfg,
		scheduler: NewScheduler(),
		stats: NodeStats{
			NodeID:            cfg.NodeID,
			Hostname:          hostname,
			StartedAt:         time.Now(),
			Port:              cfg.DaemonPort,
			APIPort:           cfg.APIPort,
			Version:           "0.3.0",
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
	fmt.Println("║       v0.3.0  — Patent Pending       ║")
	fmt.Println("╚══════════════════════════════════════╝")
	fmt.Printf("\n  Node ID   : %s\n", s.NodeID)
	fmt.Printf("  Hostname  : %s\n", s.Hostname)
	fmt.Printf("  Port      : %d  (P2P)\n", s.Port)
	fmt.Printf("  HTTP      : %d (dashboard API)\n", s.APIPort)
	fmt.Printf("  Started   : %s\n", s.StartedAt.Format("2006-01-02 15:04:05"))
	fmt.Printf("  Partition : %d%% personal / %d%% social\n\n",
		s.PersonalPartition, s.SocialPartition)
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
	node := NewNode(cfg)
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

	// Start contribution score ticker
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		for range ticker.C {
			node.mu.Lock()
			node.stats.ContributionScore += 0.1
			node.mu.Unlock()
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
			var incoming struct {
				Class         int     `json:"class"`
				Prompt        string  `json:"prompt"`
				Justification string  `json:"justification"`
				EstimatedCost float64 `json:"estimated_cost"`
			}
			if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
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

			respCh := make(chan tokenResult, 1)
			node.scheduler.Enqueue(&TokenRequest{
				Class:         incoming.Class,
				Prompt:        incoming.Prompt,
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
				Model:             ollamaModel,
				Response:          result.response,
				ResponseTimeMs:    result.responseTimeMs,
				ContributionDelta: result.contributionDelta,
				ContributionScore: s.ContributionScore,
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
		fmt.Printf("  [✓] Dashboard API listening on port %d\n", node.cfg.APIPort)
		http.ListenAndServe(fmt.Sprintf(":%d", node.cfg.APIPort), nil)
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
