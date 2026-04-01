package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
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
}

type Node struct {
	cfg   NodeConfig
	stats NodeStats
	mu    sync.RWMutex
}

func generateNodeID() string {
	bytes := make([]byte, 8)
	rand.Read(bytes)
	return "VRX-" + hex.EncodeToString(bytes)
}

func NewNode(cfg NodeConfig) *Node {
	hostname, _ := os.Hostname()
	return &Node{
		cfg: cfg,
		stats: NodeStats{
			NodeID:            cfg.NodeID,
			Hostname:          hostname,
			StartedAt:         time.Now(),
			Port:              cfg.DaemonPort,
			APIPort:           cfg.APIPort,
			Version:           "0.2.0",
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
	return s
}

func (n *Node) printBanner() {
	s := n.getStats()
	fmt.Println("╔══════════════════════════════════════╗")
	fmt.Println("║       VERNEX PROTOCOL NODE           ║")
	fmt.Println("║       v0.2.0  — Patent Pending       ║")
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
