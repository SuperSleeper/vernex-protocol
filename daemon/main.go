package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"

	vernexca "vernex/daemon/ca"
)

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


func runCACommand(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: vernex-node ca <subcommand>")
		fmt.Fprintln(os.Stderr, "  init                — generate root CA (run once on bootstrap node)")
		fmt.Fprintln(os.Stderr, "  init-intermediate   — generate + sign intermediate CA (requires root)")
		fmt.Fprintln(os.Stderr, "  token [network-id]  — generate enrollment token (bootstrap only)")
		fmt.Fprintln(os.Stderr, "  enroll --bootstrap <url> --token '<json>'  — enroll this node")
		os.Exit(1)
	}

	home, _ := os.UserHomeDir()
	configDir := filepath.Join(home, "vernex", "config")
	cfg := loadConfig()

	switch args[0] {
	case "init":
		if _, err := os.Stat(filepath.Join(configDir, "root.crt")); err == nil {
			fmt.Fprintln(os.Stderr, "  [!] Root CA already exists (config/root.crt). Delete it to regenerate.")
			os.Exit(1)
		}
		_, pubKey, err := loadOrGenerateKeypair(configDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [!] keypair error: %v\n", err)
			os.Exit(1)
		}
		nodeID := nodeIDFromPublicKey(pubKey)
		mode := cfg.CAMode
		if mode == "" {
			mode = "single"
		}
		k, n := cfg.CAThresholdK, cfg.CAThresholdN
		if k == 0 {
			k = 3
		}
		if n == 0 {
			n = 5
		}
		rca, err := vernexca.GenerateRootCA(configDir, nodeID, mode, k, n)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [!] Root CA generation failed: %v\n", err)
			os.Exit(1)
		}
		if rca.Cert != nil {
			fmt.Printf("  [✓] Fingerprint (SHA-256 prefix): %s\n", rca.Cert.Fingerprint())
		}
		if mode == "single" {
			fmt.Println("  [→] Next: run 'vernex-node ca init-intermediate' to create the signing CA")
		}

	case "init-intermediate":
		if _, err := os.Stat(filepath.Join(configDir, "intermediate.crt")); err == nil {
			fmt.Fprintln(os.Stderr, "  [!] Intermediate CA already exists (config/intermediate.crt).")
			os.Exit(1)
		}
		_, pubKey, err := loadOrGenerateKeypair(configDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [!] keypair error: %v\n", err)
			os.Exit(1)
		}
		nodeID := nodeIDFromPublicKey(pubKey)
		_, csr, err := vernexca.GenerateIntermediateCA(configDir, nodeID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [!] Intermediate CA key gen failed: %v\n", err)
			os.Exit(1)
		}
		rca, err := vernexca.LoadRootCA(configDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [!] Root CA not found — run 'vernex-node ca init' first: %v\n", err)
			os.Exit(1)
		}
		csrBytes, _ := json.Marshal(csr)
		cert, err := rca.SignIntermediateCSR(csrBytes)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [!] Signing intermediate CSR failed: %v\n", err)
			os.Exit(1)
		}
		certData, _ := json.MarshalIndent(cert, "", "  ")
		certPath := filepath.Join(configDir, "intermediate.crt")
		if err := os.WriteFile(certPath, certData, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "  [!] Write intermediate cert failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("  [✓] Intermediate CA cert saved to %s\n", certPath)
		fmt.Printf("  [✓] Fingerprint: %s\n", cert.Fingerprint())
		fmt.Println("  [→] Next: run 'vernex-node ca token' to generate enrollment tokens")

	case "token":
		if !cfg.IsBootstrap {
			fmt.Fprintln(os.Stderr, "  [!] Only bootstrap nodes can issue tokens (set \"is_bootstrap\": true in config/node.json)")
			os.Exit(1)
		}
		rca, err := vernexca.LoadRootCA(configDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [!] Load root CA failed: %v\n", err)
			os.Exit(1)
		}
		networkID := "vernex-mainnet"
		if len(args) > 1 {
			networkID = args[1]
		}
		token, err := vernexca.GenerateEnrollmentToken(networkID, 30*24*time.Hour, rca.PrivKey)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [!] Token generation failed: %v\n", err)
			os.Exit(1)
		}
		tokenJSON, _ := json.MarshalIndent(token, "", "  ")
		tokenPath := filepath.Join(configDir, "token-"+token.TokenID+".json")
		if err := os.WriteFile(tokenPath, tokenJSON, 0600); err != nil {
			fmt.Fprintf(os.Stderr, "  [!] Token save failed: %v\n", err)
			os.Exit(1)
		}
		// Never print signature to stdout — confirmation only.
		fmt.Printf("  [✓] token_id  : %s\n", token.TokenID)
		fmt.Printf("      expires_at: %s\n", token.ExpiresAt.Format("2006-01-02"))
		fmt.Printf("      path      : %s\n", tokenPath)

	case "enroll":
		fs := flag.NewFlagSet("ca enroll", flag.ExitOnError)
		bootstrapURL := fs.String("bootstrap", "", "Bootstrap node HTTPS URL (e.g. https://76.244.40.49:7701)")
		tokenStr := fs.String("token", "", "Enrollment token JSON (from bootstrap operator)")
		fs.Parse(args[1:]) //nolint:errcheck
		if *bootstrapURL == "" || *tokenStr == "" {
			fmt.Fprintln(os.Stderr, "Usage: vernex-node ca enroll --bootstrap <url> --token '<json>'")
			os.Exit(1)
		}
		var token vernexca.EnrollmentToken
		if err := json.Unmarshal([]byte(*tokenStr), &token); err != nil {
			fmt.Fprintf(os.Stderr, "  [!] Token parse error: %v\n", err)
			os.Exit(1)
		}
		_, pubKey, err := loadOrGenerateKeypair(configDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [!] keypair error: %v\n", err)
			os.Exit(1)
		}
		nodeID := nodeIDFromPublicKey(pubKey)
		if err := vernexca.ComputeNodeEnroll(*bootstrapURL, token, nodeID, configDir); err != nil {
			fmt.Fprintf(os.Stderr, "  [!] Enrollment failed: %v\n", err)
			os.Exit(1)
		}

	default:
		fmt.Fprintf(os.Stderr, "  [!] Unknown ca subcommand: %s\n", args[0])
		fmt.Fprintln(os.Stderr, "  Usage: vernex-node ca <init|init-intermediate|token|enroll>")
		os.Exit(1)
	}
}

// resolveBootstrapNodes returns HTTPS API URLs for all configured peer nodes.
// These are passed to CheckSystemClock for Step D bootstrap time consensus.
func resolveBootstrapNodes(cfg NodeConfig) []string {
	var urls []string
	for _, peer := range cfg.PeerNodes {
		apiURL, err := peerAPIURL(peer)
		if err == nil {
			urls = append(urls, apiURL)
		}
	}
	return urls
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "ca" {
		runCACommand(os.Args[2:])
		return
	}

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

	node := NewNode(cfg, configDir, privKey, pubKey, mldsaPrivKey, mldsaPubKey)
	node.printBanner()

	// System clock verification — gates CA operations on /enroll, /sign-intermediate, /token-gen.
	// Runs synchronously so the result is visible in the banner before any goroutines start.
	bootstrapURLs := resolveBootstrapNodes(cfg)
	clockStatus := vernexca.CheckSystemClock(configDir, bootstrapURLs)
	node.mu.Lock()
	node.clockStatus = clockStatus
	node.mu.Unlock()
	if clockStatus.BlockCAOps {
		fmt.Println("  [!] CLOCK ERROR — CA operations blocked")
		fmt.Printf("      %s\n", clockStatus.Message)
	} else if clockStatus.Drift > time.Minute {
		fmt.Printf("  [~] Clock drift warning: %s (source: %s)\n",
			clockStatus.Drift.Round(time.Second), clockStatus.Source)
	} else {
		fmt.Printf("  [✓] Clock verified  drift=%s  source=%s\n",
			clockStatus.Drift.Round(time.Millisecond), clockStatus.Source)
	}

	// Background clock re-check — refreshes clockStatus every 30 minutes.
	go func() {
		ticker := time.NewTicker(30 * time.Minute)
		for range ticker.C {
			cs := vernexca.CheckSystemClock(configDir, bootstrapURLs)
			node.mu.Lock()
			node.clockStatus = cs
			node.mu.Unlock()
			if cs.BlockCAOps {
				fmt.Printf("  [!] Clock drift exceeded — CA ops now blocked\n")
			}
		}
	}()

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
		sendDeregisterToBootstrap(node)
		if inhibitor != nil {
			inhibitor.Close()
		}
		os.Exit(0)
	}()

	// Start token scheduler worker
	go node.scheduler.run(node)
	fmt.Println("  [✓] Token scheduler running (Class 1 > Class 2, FIFO)")

	startCommonsReviewExpiry(node)

	startContributionTicker(node)

	startRateLimiterPrune(node)

	startPublicIPRefresher(node)

	startIPWatchdog(node)

	startMDNS(node)

	startHeartbeatLoop(node)

	startUDPListener(node)

	startAutoPunch(node)

	startHTTPServer(node, tlsCfg, configDir)


	// Start P2P listener
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", node.cfg.DaemonPort))
	if err != nil {
		fmt.Printf("  ERROR: Could not bind to port %d — %v\n", node.cfg.DaemonPort, err)
		os.Exit(1)
	}
	defer listener.Close()

	fmt.Printf("  [✓] P2P listener on port %d\n", node.cfg.DaemonPort)
	fmt.Println("  [✓] Node is online — waiting for connections...")
	fmt.Print("  Press Ctrl+C to stop\n\n")

	for {
		conn, err := listener.Accept()
		if err != nil {
			continue
		}
		go handleConnection(conn, node)
	}
}

