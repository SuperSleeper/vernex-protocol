package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
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
		fmt.Printf("\n  Enrollment Token (valid until %s):\n\n%s\n\n", token.ExpiresAt.Format("2006-01-02"), tokenJSON)
		fmt.Println("  Share this with the new node operator.")
		fmt.Println("  They run: vernex-node ca enroll --bootstrap <this_url> --token '<json>'")

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

	// Pull CA certs from peers if not yet bootstrapped locally.
	// Runs synchronously so TrustStore is populated before the first heartbeat.
	if _, statErr := os.Stat(filepath.Join(configDir, "root.crt")); os.IsNotExist(statErr) && len(cfg.PeerNodes) > 0 {
		fmt.Println("  [→] No local root.crt — pulling CA certs from peers...")
		caClient := node.buildPeerTLSClient(10 * time.Second)
		for _, peer := range cfg.PeerNodes {
			apiURL, err := peerAPIURL(peer)
			if err != nil {
				fmt.Printf("  [!] CA sync: bad peer URL %q: %v\n", peer.Name, err)
				continue
			}
			if err := vernexca.PullCASync(apiURL, configDir, caClient); err != nil {
				fmt.Printf("  [!] CA certs could not pull from %s: %v\n", peer.Name, err)
			} else {
				fmt.Printf("  [✓] CA certs pulled from %s\n", peer.Name)
			}
		}
	}

	// Start token scheduler worker
	go node.scheduler.run(node)
	fmt.Println("  [✓] Token scheduler running (Class 1 > Class 2, FIFO)")

	startCommonsReviewExpiry(node)

	startContributionTicker(node)

	startRateLimiterPrune(node)

	startPublicIPRefresher(node)

	startIPWatchdog(node)

	startHeartbeatLoop(node)

	startMDNS(node)

	startUDPListener(node)

	startAutoPunch(node)

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
		// Accepts node_id, api_url, external endpoint, and optional status (full /status payload).
		// No signature required: registration is informational; trust is enforced at /submit.
		http.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "POST required", http.StatusMethodNotAllowed)
				return
			}
			var req struct {
				NodeID       string          `json:"node_id"`
				APIURL       string          `json:"api_url"`
				ExternalIP   string          `json:"external_ip,omitempty"`
				ExternalPort int             `json:"external_port,omitempty"`
				Status       json.RawMessage `json:"status,omitempty"` // full /status payload pushed by peer
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "invalid JSON", http.StatusBadRequest)
				return
			}
			if req.NodeID == "" || req.APIURL == "" {
				http.Error(w, "node_id and api_url required", http.StatusBadRequest)
				return
			}
			entry := PeerEntry{
				NodeID:       req.NodeID,
				APIURL:       req.APIURL,
				ExternalIP:   req.ExternalIP,
				ExternalPort: req.ExternalPort,
				LastSeen:     time.Now(),
				PushedStatus: req.Status,
			}
			// Preserve verified state from async cert-verify — re-register must not reset it.
			if existing, ok := node.peerRegistry.GetByNodeID(req.NodeID); ok && existing.CertVerified {
				entry.CertVerified = true
			}
			node.peerRegistry.Register(entry)
			fmt.Printf("  [↔] registered peer  id=%s  ext=%s:%d\n", req.NodeID, req.ExternalIP, req.ExternalPort)
			// Async cert verification — does not block registration.
			go func(apiURL, nodeID string) {
				cert, err := vernexca.FetchPeerCert(apiURL, node.buildPeerTLSClient(5*time.Second))
				if err != nil {
					fmt.Printf("  [~] cert-verify: no cert from %s (%v)\n", nodeID, err)
					return
				}
				if err := node.trustStore.VerifyCert(*cert); err != nil {
					fmt.Printf("  [!] cert-verify: UNVERIFIED %s — %v\n", nodeID, err)
					return
				}
				// Update CertVerified directly under registry lock — avoids re-register race.
				node.peerRegistry.SetCertVerified(nodeID, true)
				fmt.Printf("  [✓] cert-verify: VERIFIED %s\n", nodeID)
			}(req.APIURL, req.NodeID)
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
				CertVerified   bool   `json:"cert_verified"`
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
					CertVerified:   p.CertVerified,
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

		// /peer-status/{node_id} — proxy /status to a registered peer's api_url.
		// Lets the dashboard fetch remote node status through the bootstrap node
		// when the remote node isn't directly reachable inbound (behind NAT).
		http.HandleFunc("/peer-status/", func(w http.ResponseWriter, r *http.Request) {
			nodeID := strings.TrimPrefix(r.URL.Path, "/peer-status/")
			if nodeID == "" {
				http.Error(w, "node_id required in path", http.StatusBadRequest)
				return
			}
			peer, ok := node.peerRegistry.GetByNodeID(nodeID)
			if !ok {
				http.Error(w, "peer not registered", http.StatusNotFound)
				return
			}
			client := node.buildPeerTLSClient(3 * time.Second)
			resp, err := client.Get(peer.APIURL + "/status")
			if err == nil {
				defer resp.Body.Close()
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Access-Control-Allow-Origin", "*")
				io.Copy(w, resp.Body) //nolint:errcheck
				return
			}
			// Direct fetch failed — serve the pushed status cached on last heartbeat.
			if len(peer.PushedStatus) > 0 && time.Since(peer.LastSeen) < peerLiveTTL {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Access-Control-Allow-Origin", "*")
				w.Write(peer.PushedStatus) //nolint:errcheck
				return
			}
			http.Error(w, fmt.Sprintf("peer unreachable and no cached status: %v", err), http.StatusServiceUnavailable)
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
				if err := signalPunch(initiator.APIURL, target.ExternalIP, target.ExternalPort, node.trustStore); err != nil {
					fmt.Printf("  [!] punch-request: signal to initiator %s failed: %v\n", req.InitiatorID, err)
				}
			}()
			go func() {
				if err := signalPunch(target.APIURL, initiator.ExternalIP, initiator.ExternalPort, node.trustStore); err != nil {
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

		// /ca-sync — returns known CA certs for gossip propagation (public, no auth).
		http.HandleFunc("/ca-sync", vernexca.HandleCASync(configDir))

		if node.cfg.IsBootstrap {
			// /sign-intermediate — root CA signs an intermediate CSR (bootstrap only).
			// Requires config/root.key to be present (single mode).
			http.HandleFunc("/sign-intermediate", func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					http.Error(w, "POST required", http.StatusMethodNotAllowed)
					return
				}
				rca, err := vernexca.LoadRootCA(configDir)
				if err != nil {
					http.Error(w, "root CA not available: "+err.Error(), http.StatusServiceUnavailable)
					return
				}
				csrBytes, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, "read body failed", http.StatusBadRequest)
					return
				}
				cert, err := rca.SignIntermediateCSR(csrBytes)
				if err != nil {
					http.Error(w, "signing failed: "+err.Error(), http.StatusBadRequest)
					return
				}
				certBytes, _ := json.Marshal(cert)
				w.Header().Set("Content-Type", "application/json")
				w.Write(certBytes) //nolint:errcheck
				fmt.Printf("  [✓] signed intermediate CSR: cn=%s\n", cert.Subject.CommonName)
			})

			// /enroll — intermediate CA signs a compute node CSR using enrollment token.
			http.HandleFunc("/enroll", func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					http.Error(w, "POST required", http.StatusMethodNotAllowed)
					return
				}
				var req struct {
					Token json.RawMessage `json:"token"`
					CSR   json.RawMessage `json:"csr"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					http.Error(w, "invalid JSON", http.StatusBadRequest)
					return
				}
				var token vernexca.EnrollmentToken
				if err := json.Unmarshal(req.Token, &token); err != nil {
					http.Error(w, "invalid token JSON: "+err.Error(), http.StatusBadRequest)
					return
				}
				ica, err := vernexca.LoadIntermediateCA(configDir)
				if err != nil {
					http.Error(w, "intermediate CA not available: "+err.Error(), http.StatusServiceUnavailable)
					return
				}
				certBytes, err := ica.SignComputeNodeCSR(req.CSR, &token)
				if err != nil {
					http.Error(w, "enrollment failed: "+err.Error(), http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]json.RawMessage{"cert": certBytes}) //nolint:errcheck
			})

			// /token-gen — localhost only — generate a signed enrollment token.
			http.HandleFunc("/token-gen", func(w http.ResponseWriter, r *http.Request) {
				if !isLocalhost(r) {
					http.Error(w, "localhost only", http.StatusForbidden)
					return
				}
				if r.Method != http.MethodPost {
					http.Error(w, "POST required", http.StatusMethodNotAllowed)
					return
				}
				var req struct {
					NetworkID string `json:"network_id"`
				}
				json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
				if req.NetworkID == "" {
					req.NetworkID = "vernex-mainnet"
				}
				rca, err := vernexca.LoadRootCA(configDir)
				if err != nil {
					http.Error(w, "root CA not available: "+err.Error(), http.StatusServiceUnavailable)
					return
				}
				token, err := vernexca.GenerateEnrollmentToken(req.NetworkID, 30*24*time.Hour, rca.PrivKey)
				if err != nil {
					http.Error(w, "token generation failed: "+err.Error(), http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(token) //nolint:errcheck
				fmt.Printf("  [✓] enrollment token generated  network=%s  expires=%s\n",
					req.NetworkID, token.ExpiresAt.Format("2006-01-02"))
			})
		}

		fmt.Printf("  [✓] Dashboard API (HTTPS) listening on port %d\n", node.cfg.APIPort)
		if node.cfg.IsBootstrap {
			fmt.Println("  [✓] Bootstrap endpoints active: /sign-intermediate  /enroll  /token-gen")
		}
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
