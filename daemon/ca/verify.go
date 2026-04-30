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
	"time"
)

// TrustStore holds verified CA certs for ML-DSA chain validation.
// Root + all known intermediates are loaded at startup; nodes with no CA files
// operate in TOFU mode until /ca-sync propagates certs.
type TrustStore struct {
	RootCert      *VernexCert
	Intermediates []VernexCert
	configDir     string
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

	return ts, nil
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
	log.Printf("[tls] TOFU: peer cert CN=%q serial=%s", cert.Subject.CommonName, cert.SerialNumber)
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
