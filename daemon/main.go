package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

type NodeStats struct {
	NodeID          string    `json:"node_id"`
	Hostname        string    `json:"hostname"`
	StartedAt       time.Time `json:"started_at"`
	Port            int       `json:"port"`
	Version         string    `json:"version"`
	UptimeSeconds   int64     `json:"uptime_seconds"`
	TotalConnections int      `json:"total_connections"`
	ContributionScore float64 `json:"contribution_score"`
	SocialPartition  int      `json:"social_partition_pct"`
	PersonalPartition int     `json:"personal_partition_pct"`
}

type Node struct {
	stats NodeStats
	mu    sync.RWMutex
}

func generateNodeID() string {
	bytes := make([]byte, 8)
	rand.Read(bytes)
	return "VRX-" + hex.EncodeToString(bytes)
}

func NewNode() *Node {
	hostname, _ := os.Hostname()
	return &Node{
		stats: NodeStats{
			NodeID:            generateNodeID(),
			Hostname:          hostname,
			StartedAt:         time.Now(),
			Port:              7700,
			Version:           "0.2.0",
			SocialPartition:   30,
			PersonalPartition: 70,
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
	fmt.Printf("  HTTP      : 7701 (dashboard API)\n")
	fmt.Printf("  Started   : %s\n", s.StartedAt.Format("2006-01-02 15:04:05"))
	fmt.Printf("  Partition : %d%% personal / %d%% social\n\n",
		s.PersonalPartition, s.SocialPartition)
}

func main() {
	node := NewNode()
	node.printBanner()

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
		fmt.Println("  [✓] Dashboard API listening on port 7701")
		http.ListenAndServe(":7701", nil)
	}()

	// Start P2P listener on port 7700
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", 7700))
	if err != nil {
		fmt.Printf("  ERROR: Could not bind to port 7700 — %v\n", err)
		os.Exit(1)
	}
	defer listener.Close()

	fmt.Println("  [✓] P2P listener on port 7700")
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
