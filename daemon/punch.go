package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	vernexca "vernex/daemon/ca"
)

// sendHolePunchPackets fires count UDP packets toward addr to open a NAT hole.
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
		if err := signalPunch(peer.APIURL, extIP, extPort, n.trustStore); err != nil {
			fmt.Printf("  [!] punch-signal → %s failed: %v\n", peer.NodeID, err)
		}
	}()
	n.sendHolePunchPackets(target, 5)
}

// stunResponse is the payload returned by the /stun endpoint.
type stunResponse struct {
	ExternalIP   string `json:"external_ip"`
	ExternalPort int    `json:"external_port"`
	NodeID       string `json:"node_id"`
}

// discoverExternalEndpoint calls /stun on each configured peer in turn and returns
// the first successful response. Returns ("", 0) if no peer responds.
func discoverExternalEndpoint(cfg NodeConfig, ts *vernexca.TrustStore) (string, int) {
	client := ts.NewTLSClient(5 * time.Second)
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

// signalPunch sends a /punch-signal to targetAPIURL, instructing that node to initiate
// UDP hole punching toward punchIP:punchPort simultaneously.
func signalPunch(targetAPIURL, punchIP string, punchPort int, ts *vernexca.TrustStore) error {
	payload, _ := json.Marshal(map[string]any{
		"punch_ip":   punchIP,
		"punch_port": punchPort,
	})
	client := ts.NewTLSClient(5 * time.Second)
	resp, err := client.Post(targetAPIURL+"/punch-signal", "application/json", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// isLocalhost returns true only if the request came from 127.0.0.1 or ::1.
func isLocalhost(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	return host == "127.0.0.1" || host == "::1"
}

// startUDPListener opens the UDP socket on daemonPort for hole-punch packet handling.
func startUDPListener(node *Node) {
	udpAddr := &net.UDPAddr{Port: node.cfg.DaemonPort}
	udpConn, uerr := net.ListenUDP("udp", udpAddr)
	if uerr != nil {
		fmt.Printf("  [!] UDP listener failed on port %d: %v (hole punching disabled)\n", node.cfg.DaemonPort, uerr)
		return
	}
	node.udpConn = udpConn
	fmt.Printf("  [✓] UDP listener on port %d (hole punching)\n", node.cfg.DaemonPort)
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

// startIPWatchdog compares LAN and public IPs every 30s; on change re-runs STUN,
// updates the external endpoint, re-registers with all peers, and clears peerHoles.
func startIPWatchdog(node *Node) {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		for range ticker.C {
			curLAN := outboundIP("8.8.8.8")
			curPublic, _ := node.cachedPublicIP.Load().(string)
			prevLAN, _ := node.lastLANIP.Load().(string)
			prevPublic, _ := node.lastPublicIP.Load().(string)

			if curLAN == prevLAN && curPublic == prevPublic {
				continue
			}

			fmt.Printf("  [→] IP change detected:")
			if curLAN != prevLAN {
				fmt.Printf(" LAN %s → %s", prevLAN, curLAN)
			}
			if curPublic != prevPublic {
				fmt.Printf(" public %s → %s", prevPublic, curPublic)
			}
			fmt.Println()

			node.lastLANIP.Store(curLAN)
			node.lastPublicIP.Store(curPublic)

			extIP, extPort := discoverExternalEndpoint(node.cfg, node.trustStore)
			node.externalIP.Store(extIP)
			node.externalPort.Store(int32(extPort))

			registerWithPeers(node, extIP, extPort)

			// Old UDP holes are tied to the previous IP — clear them so connectionType
			// falls back to "relayed" until new holes are punched.
			node.peerHolesMu.Lock()
			node.peerHoles = make(map[string]*net.UDPAddr)
			node.peerHolesMu.Unlock()

			fmt.Println("  [✓] Re-registered with all peers after IP change")
		}
	}()
}

// startAutoPunch attempts direct connections toward RELAYED peers every 5 minutes.
func startAutoPunch(node *Node) {
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
}
