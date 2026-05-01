package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

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
	BraveAPIKey          string     `json:"brave_api_key,omitempty"`  // Brave Search API key; omitted from config if empty
	PeerNodes            []PeerNode `json:"peer_nodes,omitempty"`
	IsBootstrap          bool       `json:"is_bootstrap,omitempty"`   // true on bootstrap/root CA nodes
	CAMode               string     `json:"ca_mode,omitempty"`        // "single" or "threshold" — default "single"
	CAThresholdK         int        `json:"ca_threshold_k,omitempty"` // Shamir shares required (default 3)
	CAThresholdN         int        `json:"ca_threshold_n,omitempty"` // Shamir total shares (default 5)
}

func generateNodeID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "VRX-" + hex.EncodeToString(b)
}

func loadConfig() NodeConfig {
	cfg := NodeConfig{
		SocialPartitionPct:   30,
		PersonalPartitionPct: 70,
		DaemonPort:           7700,
		APIPort:              7701,
		DashboardPort:        5000,
		RateLimitPerMin:      60,
		CAMode:               "single",
		CAThresholdK:         3,
		CAThresholdN:         5,
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
