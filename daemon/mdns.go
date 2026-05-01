package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

// registerMDNSViaAvahi registers this node as a _vernex._tcp service through the system
// avahi daemon using the org.freedesktop.Avahi D-Bus API. The returned connection must
// be kept open for the registration to remain active — it is tied to the D-Bus session.
func registerMDNSViaAvahi(cfg NodeConfig, pubKeyB64 string) (*dbus.Conn, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, fmt.Errorf("D-Bus connect: %w", err)
	}
	server := conn.Object("org.freedesktop.Avahi", "/")
	var groupPath dbus.ObjectPath
	if err := server.Call("org.freedesktop.Avahi.Server.EntryGroupNew", 0).Store(&groupPath); err != nil {
		conn.Close()
		return nil, fmt.Errorf("EntryGroupNew: %w", err)
	}
	group := conn.Object("org.freedesktop.Avahi", groupPath)
	txtRecords := [][]byte{
		[]byte("node_id=" + cfg.NodeID),
		[]byte("pub_key=" + pubKeyB64),
		[]byte("version=0.11.2"),
	}
	if err := group.Call("org.freedesktop.Avahi.EntryGroup.AddService", 0,
		int32(-1), int32(-1), uint32(0),
		cfg.NodeID, "_vernex._tcp", "", "",
		uint16(cfg.APIPort),
		txtRecords,
	).Err; err != nil {
		conn.Close()
		return nil, fmt.Errorf("AddService: %w", err)
	}
	if err := group.Call("org.freedesktop.Avahi.EntryGroup.Commit", 0).Err; err != nil {
		conn.Close()
		return nil, fmt.Errorf("Commit: %w", err)
	}
	return conn, nil
}

// avahiPeer holds a single result from discoverAvahiPeers.
type avahiPeer struct {
	nodeID string
	pubKey string
	addr   string
	port   int
}

// discoverAvahiPeers runs avahi-browse -r -t to find _vernex._tcp services on the LAN.
// The -t flag makes avahi-browse terminate after the initial scan completes.
// Output lines use the parsable format: =;iface;proto;name;type;domain;host;addr;port;txt
func discoverAvahiPeers(ownNodeID string) []avahiPeer {
	cmd := exec.Command("avahi-browse", "-r", "-t", "_vernex._tcp", "--parsable")
	// Kill if avahi-browse hangs longer than 8 seconds
	done := make(chan struct{})
	go func() {
		select {
		case <-done:
		case <-time.After(8 * time.Second):
			if cmd.Process != nil {
				cmd.Process.Kill() //nolint:errcheck
			}
		}
	}()
	out, _ := cmd.Output()
	close(done)

	var peers []avahiPeer
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "=") {
			continue // only process resolved entries (= prefix)
		}
		// =;iface;proto;name;type;domain;hostname;addr;port[;txt...]
		fields := strings.SplitN(line, ";", 10)
		if len(fields) < 9 {
			continue
		}
		addrStr := fields[7]
		port, err := strconv.Atoi(fields[8])
		if err != nil {
			continue
		}
		// TXT records are space-separated quoted strings: "key=value" "key2=value2"
		txt := make(map[string]string)
		if len(fields) == 10 {
			for _, part := range strings.Fields(fields[9]) {
				part = strings.Trim(part, "\"")
				if idx := strings.IndexByte(part, '='); idx > 0 {
					txt[part[:idx]] = part[idx+1:]
				}
			}
		}
		nodeID := txt["node_id"]
		if nodeID == "" || nodeID == ownNodeID {
			continue
		}
		peers = append(peers, avahiPeer{
			nodeID: nodeID,
			pubKey: txt["pub_key"],
			addr:   addrStr,
			port:   port,
		})
	}
	return peers
}

// connectionType returns "local", "direct", or "relayed" for a registered peer.
// "local"   — peer API URL resolves to a private/loopback address, OR discovered via mDNS.
// "direct"  — a VERNEX-PUNCH UDP packet was received from this peer's external address.
// "relayed" — connection falls through the TCP daemon relay; hole punch not yet confirmed.
func (n *Node) connectionType(peer PeerEntry) string {
	if u, err := url.Parse(peer.APIURL); err == nil && isPrivateIP(u.Hostname()) {
		return "local"
	}
	n.mdnsDiscoveredMu.RLock()
	_, viaLAN := n.mdnsDiscovered[peer.NodeID]
	n.mdnsDiscoveredMu.RUnlock()
	if viaLAN {
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

// startMDNS registers this node via avahi D-Bus and starts the mDNS discovery goroutine.
func startMDNS(node *Node) {
	cfg := node.cfg

	// mDNS service registration via avahi D-Bus.
	// Integrates with the system avahi daemon already running on Pop!_OS — no port conflict.
	pubKeyB64 := base64.StdEncoding.EncodeToString(node.publicKey)
	avahiConn, merr := registerMDNSViaAvahi(cfg, pubKeyB64)
	if merr != nil {
		fmt.Printf("  [!] mDNS registration via avahi failed: %v (continuing without LAN advertisement)\n", merr)
	} else {
		fmt.Println("  [✓] mDNS service registered via avahi (_vernex._tcp.local)")
		// Hold the D-Bus connection open for process lifetime — registration is session-scoped.
		go func() { defer avahiConn.Close(); select {} }()
	}

	// mDNS discovery goroutine — uses avahi-browse every 30 seconds.
	// Finds peers on the same LAN without bootstrap involvement.
	go func() {
		isTrustedPeer := func(nodeID string) bool {
			for _, p := range node.cfg.PeerNodes {
				raw, err := base64.StdEncoding.DecodeString(p.PublicKey)
				if err != nil || len(raw) != 32 {
					continue
				}
				if nodeIDFromPublicKey(ed25519.PublicKey(raw)) == nodeID {
					return true
				}
			}
			return false
		}

		for {
			for _, peer := range discoverAvahiPeers(cfg.NodeID) {
				peerAPIURL := fmt.Sprintf("https://%s:%d", peer.addr, peer.port)

				node.mdnsDiscoveredMu.Lock()
				alreadyKnown := node.mdnsDiscovered[peer.nodeID]
				node.mdnsDiscovered[peer.nodeID] = true
				node.mdnsDiscoveredMu.Unlock()

				if isTrustedPeer(peer.nodeID) {
					node.peerRegistry.Register(PeerEntry{
						NodeID:   peer.nodeID,
						APIURL:   peerAPIURL,
						LastSeen: time.Now(),
					})
					// Add to dynamic heartbeat list so registerWithPeers reaches this peer.
					node.dynamicPeersMu.Lock()
					node.dynamicPeers[peer.nodeID] = peerAPIURL
					node.dynamicPeersMu.Unlock()
					if !alreadyKnown {
						fmt.Printf("  [✓] mDNS discovered trusted peer: %s at %s\n", peer.nodeID, peerAPIURL)
					}
				} else {
					tr := TrustRequest{
						NodeID:      peer.nodeID,
						PublicKey:   peer.pubKey,
						APIUrl:      peerAPIURL,
						RequestedAt: time.Now(),
						SourceIP:    peer.addr,
					}
					node.trustMu.Lock()
					replaced := false
					for i := range node.trustRequests {
						if node.trustRequests[i].NodeID == peer.nodeID {
							node.trustRequests[i] = tr
							replaced = true
							break
						}
					}
					if !replaced {
						node.trustRequests = append(node.trustRequests, tr)
					}
					node.trustMu.Unlock()
					if !alreadyKnown {
						fmt.Printf("  [↑] mDNS discovered unknown peer: %s at %s — trust request queued\n", peer.nodeID, peerAPIURL)
					}
				}
			}

			time.Sleep(30 * time.Second)
		}
	}()
	fmt.Println("  [✓] mDNS discovery goroutine started (avahi-browse _vernex._tcp)")
}
