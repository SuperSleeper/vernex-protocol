package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"
)

type NodeInfo struct {
	NodeID    string    `json:"node_id"`
	Hostname  string    `json:"hostname"`
	StartedAt time.Time `json:"started_at"`
	Port      int       `json:"port"`
	Version   string    `json:"version"`
}

func generateNodeID() string {
	bytes := make([]byte, 8)
	rand.Read(bytes)
	return "VRX-" + hex.EncodeToString(bytes)
}

func main() {
	hostname, _ := os.Hostname()

	node := NodeInfo{
		NodeID:    generateNodeID(),
		Hostname:  hostname,
		StartedAt: time.Now(),
		Port:      7700,
		Version:   "0.1.0",
	}

	fmt.Println("╔══════════════════════════════════════╗")
	fmt.Println("║       VERNEX PROTOCOL NODE           ║")
	fmt.Println("║       v0.1.0  — Patent Pending       ║")
	fmt.Println("╚══════════════════════════════════════╝")
	fmt.Printf("\n  Node ID  : %s\n", node.NodeID)
	fmt.Printf("  Hostname : %s\n", node.Hostname)
	fmt.Printf("  Port     : %d\n", node.Port)
	fmt.Printf("  Started  : %s\n\n", node.StartedAt.Format("2006-01-02 15:04:05"))

	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", node.Port))
	if err != nil {
		fmt.Printf("  ERROR: Could not bind to port %d — %v\n", node.Port, err)
		os.Exit(1)
	}
	defer listener.Close()

	fmt.Printf("  [✓] Listening on port %d\n", node.Port)
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

func handleConnection(conn net.Conn, node NodeInfo) {
	defer conn.Close()
	remote := conn.RemoteAddr().String()
	fmt.Printf("  [→] Connection from %s\n", remote)

	response, _ := json.MarshalIndent(node, "  ", "  ")
	conn.Write(response)
	conn.Write([]byte("\n"))

	fmt.Printf("  [✓] Sent node info to %s\n", remote)
}
