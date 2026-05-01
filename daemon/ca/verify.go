package ca

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// TrustStore holds verified CA certs for ML-DSA chain validation.
// Root + all known intermediates are loaded at startup; nodes with no CA files
// operate in TOFU mode until /ca-sync propagates certs.
type TrustStore struct {
	RootCert      *VernexCert
	Intermediates []VernexCert
	configDir     string
	trustedCNs    map[string]bool // peer node IDs whose VernexCert chain has been verified
	mu            sync.RWMutex   // guards trustedCNs only
}

// LoadTrustStore reads config/root.crt and config/trusted_intermediates.json.
// Also loads local config/intermediate.crt if present (bootstrap nodes).
// Returns a partially-populated store on partial load — callers must tolerate nil RootCert.
func LoadTrustStore(configDir string) (*TrustStore, error) {
	ts := &TrustStore{configDir: configDir}

	rootData, err := os.ReadFile(filepath.Join(configDir, "root.crt"))
	if err == nil {
		var root VernexCert
		if err := json.Unmarshal(rootData, &root); err != nil {
			return ts, fmt.Errorf("parse root.crt: %w", err)
		}
		ts.RootCert = &root
	}

	// Trusted intermediates from gossip / operator-imported list
	tiPath := filepath.Join(configDir, "trusted_intermediates.json")
	if data, err := os.ReadFile(tiPath); err == nil {
		var certs []VernexCert
		if err := json.Unmarshal(data, &certs); err != nil {
			return ts, fmt.Errorf("parse trusted_intermediates.json: %w", err)
		}
		ts.Intermediates = append(ts.Intermediates, certs...)
	}

	// Local intermediate.crt (bootstrap nodes that generated their own intermediate CA)
	localInt := filepath.Join(configDir, "intermediate.crt")
	if data, err := os.ReadFile(localInt); err == nil {
		var cert VernexCert
		if err := json.Unmarshal(data, &cert); err == nil {
			if !ts.hasIntermediate(cert.Subject.CommonName) {
				ts.Intermediates = append(ts.Intermediates, cert)
			}
		}
	}

	ts.loadTrustedCNs()
	return ts, nil
}

// loadTrustedCNs populates trustedCNs from config/trusted_certs/trusted_nodes.json.
// Called once during LoadTrustStore before the TrustStore is shared — no locking needed.
func (ts *TrustStore) loadTrustedCNs() {
	ts.trustedCNs = make(map[string]bool)
	path := filepath.Join(ts.configDir, "trusted_certs", "trusted_nodes.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return // file absent on first run — start empty
	}
	var cns []string
	if err := json.Unmarshal(data, &cns); err != nil {
		return
	}
	for _, cn := range cns {
		ts.trustedCNs[cn] = true
	}
	if len(cns) > 0 {
		log.Printf("[trust] loaded %d trusted peer CN(s) from trusted_nodes.json", len(cns))
	}
}

// TrustPeerCN marks cn as CA-verified and persists the updated set to
// config/trusted_certs/trusted_nodes.json (dir 0700, file 0600).
// No-op if cn is already trusted.
func (ts *TrustStore) TrustPeerCN(cn string) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.trustedCNs[cn] {
		return nil
	}
	ts.trustedCNs[cn] = true
	if err := ts.persistTrustedCNs(); err != nil {
		log.Printf("[trust] warn: failed to persist trusted CN %q: %v (configDir=%s)", cn, err, ts.configDir)
		return err
	}
	return nil
}

// IsTrustedCN reports whether cn has been CA-chain-verified in this TrustStore.
func (ts *TrustStore) IsTrustedCN(cn string) bool {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.trustedCNs[cn]
}

// persistTrustedCNs writes trustedCNs to disk. Must be called with mu held (write).
func (ts *TrustStore) persistTrustedCNs() error {
	dir := filepath.Join(ts.configDir, "trusted_certs")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create trusted_certs dir: %w", err)
	}
	cns := make([]string, 0, len(ts.trustedCNs))
	for cn := range ts.trustedCNs {
		cns = append(cns, cn)
	}
	sort.Strings(cns) // deterministic output
	data, err := json.MarshalIndent(cns, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "trusted_nodes.json"), data, 0600)
}

func (ts *TrustStore) hasIntermediate(cn string) bool {
	for _, c := range ts.Intermediates {
		if c.Subject.CommonName == cn {
			return true
		}
	}
	return false
}

// AddIntermediate inserts a peer-provided intermediate cert after verifying it is
// root-signed. No-op if already present. Persists to config/trusted_intermediates.json.
func (ts *TrustStore) AddIntermediate(cert VernexCert) error {
	if ts.RootCert == nil {
		return fmt.Errorf("no root cert in trust store — cannot verify intermediate")
	}
	if ts.hasIntermediate(cert.Subject.CommonName) {
		return nil
	}
	rootPub, err := UnmarshalPublicKey(ts.RootCert.PublicKey)
	if err != nil {
		return fmt.Errorf("unmarshal root public key: %w", err)
	}
	if err := cert.Verify(rootPub); err != nil {
		return fmt.Errorf("intermediate cert failed root signature check: %w", err)
	}
	ts.Intermediates = append(ts.Intermediates, cert)
	return ts.persistIntermediates()
}

func (ts *TrustStore) persistIntermediates() error {
	data, err := json.MarshalIndent(ts.Intermediates, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(ts.configDir, "trusted_intermediates.json"), data, 0644)
}

// VerifyCert checks that cert is signed by a known intermediate and within its validity
// window. Returns a descriptive error if the chain cannot be established.
func (ts *TrustStore) VerifyCert(cert VernexCert) error {
	now := time.Now()
	if now.Before(cert.NotBefore) {
		return fmt.Errorf("cert not yet valid (valid from %s)", cert.NotBefore.Format(time.RFC3339))
	}
	if now.After(cert.NotAfter) {
		return fmt.Errorf("cert expired at %s", cert.NotAfter.Format(time.RFC3339))
	}

	issuerCN := cert.Issuer.CommonName
	for _, intermediate := range ts.Intermediates {
		if intermediate.Subject.CommonName != issuerCN {
			continue
		}
		intPub, err := UnmarshalPublicKey(intermediate.PublicKey)
		if err != nil {
			return fmt.Errorf("unmarshal intermediate public key: %w", err)
		}
		tbs, err := cert.tbsJSON()
		if err != nil {
			return fmt.Errorf("compute cert TBS: %w", err)
		}
		sig, err := base64.StdEncoding.DecodeString(cert.Signature)
		if err != nil {
			return fmt.Errorf("decode cert signature: %w", err)
		}
		if !scheme.Verify(intPub, tbs, sig, nil) {
			return fmt.Errorf("cert ML-DSA signature invalid (issuer CN=%s)", issuerCN)
		}
		return nil
	}
	return fmt.Errorf("no trusted intermediate found with CN=%q — run ca-sync or enroll bootstrap first", issuerCN)
}

// VerifyTLSPeerCert implements the tls.Config.VerifyPeerCertificate callback signature.
// Vernex nodes use ed25519 self-signed TLS certs (not ML-DSA), so standard X.509 chain
// verification does not apply — trust is enforced at the payload layer via
// X-Vernex-Signature-MLDSA headers on every inter-node request.
// This function implements TOFU: logs the peer cert serial and returns nil (allow always).
// When nodes gain CA-signed TLS certs this hook can enforce full chain verification.
func (ts *TrustStore) VerifyTLSPeerCert(rawCerts [][]byte, _ [][]*x509.Certificate) error {
	if len(rawCerts) == 0 {
		log.Println("[tls] peer sent no certificate — TOFU: allowing (ML-DSA payload sigs enforce trust)")
		return nil
	}
	cert, err := x509.ParseCertificate(rawCerts[0])
	if err != nil {
		log.Printf("[tls] could not parse peer TLS cert: %v — TOFU: allowing", err)
		return nil
	}
	cn := cert.Subject.CommonName
	if ts.IsTrustedCN(cn) {
		return nil // CA-chain already verified — suppress TOFU log
	}
	log.Printf("[tls] TOFU: peer cert CN=%q serial=%s", cn, cert.SerialNumber)
	return nil
}

// NewTLSClient builds an http.Client for inter-node calls. TLS standard chain
// verification is skipped because nodes use ed25519 self-signed certs with no CA;
// VerifyTLSPeerCert is installed for TOFU logging and future enforcement.
// Pass timeout=0 for no timeout.
func (ts *TrustStore) NewTLSClient(timeout time.Duration) *http.Client {
	tlsCfg := &tls.Config{
		VerifyPeerCertificate: ts.VerifyTLSPeerCert,
		MinVersion:            tls.VersionTLS12,
	}
	// Self-signed ed25519 TLS certs have no CA chain — skip standard verification.
	// ML-DSA payload signatures (X-Vernex-Signature-MLDSA) enforce application-layer trust.
	// Remove once buildTLSConfig is upgraded to issue CA-signed TLS certs.
	tlsCfg.InsecureSkipVerify = true //nolint:gosec
	return &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
	}
}
