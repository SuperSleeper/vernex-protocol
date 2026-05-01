package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cloudflare/circl/sign"
	"github.com/cloudflare/circl/sign/mldsa/mldsa44"
)

var mldsaScheme = mldsa44.Scheme()

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

	pubKey, privKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, nil, fmt.Errorf("generating keypair: %w", err)
	}

	if err := os.MkdirAll(configDir, 0755); err != nil {
		return nil, nil, fmt.Errorf("creating config dir: %w", err)
	}
	if err := os.WriteFile(keyPath, privKey.Seed(), 0600); err != nil {
		return nil, nil, fmt.Errorf("writing node.key: %w", err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(pubKey) + "\n"
	if err := os.WriteFile(pubPath, []byte(pubB64), 0644); err != nil {
		return nil, nil, fmt.Errorf("writing node.pub: %w", err)
	}
	fmt.Printf("  [✓] Keypair generated and saved to %s\n", keyPath)
	fmt.Printf("  [✓] Public key: %s\n", strings.TrimSpace(pubB64))
	return privKey, pubKey, nil
}

// loadOrGenerateMLDSAKeypair loads an existing ML-DSA 44 keypair from configDir or
// generates and persists a new one. Private key is stored raw (2560 bytes, mode 0600);
// public key is stored base64-encoded for easy sharing with peer operators.
func loadOrGenerateMLDSAKeypair(configDir string) (sign.PublicKey, sign.PrivateKey, error) {
	keyPath := filepath.Join(configDir, "node.mldsa.key")
	pubPath := filepath.Join(configDir, "node.mldsa.pub")

	privBytes, err := os.ReadFile(keyPath)
	if err == nil && len(privBytes) == mldsaScheme.PrivateKeySize() {
		privKey, err := mldsaScheme.UnmarshalBinaryPrivateKey(privBytes)
		if err != nil {
			return nil, nil, fmt.Errorf("parsing ML-DSA private key: %w", err)
		}
		pubKey, ok := privKey.Public().(sign.PublicKey)
		if !ok {
			return nil, nil, fmt.Errorf("type assertion for ML-DSA public key failed")
		}
		fmt.Printf("  [✓] ML-DSA 44 keypair loaded from %s\n", keyPath)
		return pubKey, privKey, nil
	}

	pubKey, privKey, err := mldsaScheme.GenerateKey()
	if err != nil {
		return nil, nil, fmt.Errorf("generating ML-DSA keypair: %w", err)
	}
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return nil, nil, fmt.Errorf("creating config dir: %w", err)
	}
	privRaw, err := privKey.MarshalBinary()
	if err != nil {
		return nil, nil, fmt.Errorf("serializing ML-DSA private key: %w", err)
	}
	if err := os.WriteFile(keyPath, privRaw, 0600); err != nil {
		return nil, nil, fmt.Errorf("writing node.mldsa.key: %w", err)
	}
	pubRaw, err := pubKey.MarshalBinary()
	if err != nil {
		return nil, nil, fmt.Errorf("serializing ML-DSA public key: %w", err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(pubRaw) + "\n"
	if err := os.WriteFile(pubPath, []byte(pubB64), 0644); err != nil {
		return nil, nil, fmt.Errorf("writing node.mldsa.pub: %w", err)
	}
	fmt.Printf("  [✓] ML-DSA 44 keypair generated and saved to %s\n", keyPath)
	return pubKey, privKey, nil
}

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

// signRequest adds hybrid signing headers to an outgoing inter-node HTTP request.
// Message signed: nodeID + "|" + timestamp + "|" + hex(SHA256(body))
// Both ed25519 (classical) and ML-DSA 44 (post-quantum) signatures are attached.
// Peers enforce ML-DSA only when mldsa_public_key is configured (rolling upgrade path).
func (n *Node) signRequest(req *http.Request, body []byte) {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	h := sha256.Sum256(body)
	bodyHash := hex.EncodeToString(h[:])
	msg := n.cfg.NodeID + "|" + ts + "|" + bodyHash

	sig := ed25519.Sign(n.privateKey, []byte(msg))
	req.Header.Set("X-Vernex-Node-ID", n.cfg.NodeID)
	req.Header.Set("X-Vernex-Timestamp", ts)
	req.Header.Set("X-Vernex-Signature", base64.StdEncoding.EncodeToString(sig))

	if n.mldsaPrivKey != nil {
		mldsaSig := mldsaScheme.Sign(n.mldsaPrivKey, []byte(msg), nil)
		req.Header.Set("X-Vernex-Signature-MLDSA", base64.StdEncoding.EncodeToString(mldsaSig))
	}
}

// verifyPeerRequest verifies the hybrid signature on an incoming inter-node request.
// Requests without X-Vernex-Node-ID pass through (local UI / Flask proxy).
// ed25519 is always verified for signed requests. ML-DSA 44 is additionally enforced
// when mldsa_public_key is configured for the peer (rolling upgrade: absent = not yet enrolled).
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
		return fmt.Errorf("invalid ed25519 signature encoding")
	}

	h := sha256.Sum256(body)
	bodyHash := hex.EncodeToString(h[:])
	msg := nodeID + "|" + tsStr + "|" + bodyHash

	if !ed25519.Verify(pubKey, []byte(msg), sig) {
		return fmt.Errorf("ed25519 signature mismatch")
	}

	// ML-DSA 44 hybrid check — only enforced when peer has mldsa_public_key configured.
	mldsaPub, err := n.peerMLDSAPublicKey(nodeID)
	if err == nil {
		mldsaSigB64 := r.Header.Get("X-Vernex-Signature-MLDSA")
		if mldsaSigB64 == "" {
			return fmt.Errorf("ML-DSA signature required for hybrid-enrolled peer %s", nodeID)
		}
		mldsaSig, err := base64.StdEncoding.DecodeString(mldsaSigB64)
		if err != nil {
			return fmt.Errorf("invalid ML-DSA signature encoding")
		}
		if !mldsaScheme.Verify(mldsaPub, []byte(msg), mldsaSig, nil) {
			return fmt.Errorf("ML-DSA signature mismatch")
		}
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

// peerMLDSAPublicKey looks up a peer's ML-DSA 44 public key by matching their ed25519-derived
// node ID against configured peers. Returns an error if no ML-DSA key is configured for the peer —
// callers treat this as "ML-DSA not yet enrolled, skip hybrid check" (rolling upgrade path).
func (n *Node) peerMLDSAPublicKey(nodeID string) (sign.PublicKey, error) {
	for _, peer := range n.cfg.PeerNodes {
		if peer.MLDSAPublicKey == "" {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(peer.PublicKey)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			continue
		}
		if nodeIDFromPublicKey(ed25519.PublicKey(raw)) != nodeID {
			continue
		}
		mldsaRaw, err := base64.StdEncoding.DecodeString(peer.MLDSAPublicKey)
		if err != nil {
			return nil, fmt.Errorf("invalid ML-DSA public key encoding for peer %s", peer.Name)
		}
		pub, err := mldsaScheme.UnmarshalBinaryPublicKey(mldsaRaw)
		if err != nil {
			return nil, fmt.Errorf("parsing ML-DSA public key for peer %s: %w", peer.Name, err)
		}
		return pub, nil
	}
	return nil, fmt.Errorf("no ML-DSA public key configured for node %s", nodeID)
}
