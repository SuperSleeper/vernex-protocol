package ca

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// buildTestChain generates a root CA, intermediate CA, and writes both certs
// into a temp directory. Returns (configDir, rootCA, intCA).
func buildTestChain(t *testing.T) (string, *RootCA, *IntermediateCA) {
	t.Helper()
	dir := t.TempDir()

	root, err := GenerateRootCA(dir, "VRX-testroot", "single", 2, 3)
	if err != nil {
		t.Fatalf("GenerateRootCA: %v", err)
	}
	intCA, csr, err := GenerateIntermediateCA(dir, "VRX-testint")
	if err != nil {
		t.Fatalf("GenerateIntermediateCA: %v", err)
	}
	csrBytes, _ := json.Marshal(csr)
	intCert, err := root.SignIntermediateCSR(csrBytes)
	if err != nil {
		t.Fatalf("SignIntermediateCSR: %v", err)
	}
	intCertBytes, _ := json.MarshalIndent(intCert, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "intermediate.crt"), intCertBytes, 0644); err != nil {
		t.Fatalf("write intermediate.crt: %v", err)
	}
	intCA.Cert = intCert
	return dir, root, intCA
}

// buildNodeCert issues a compute-node cert from the given intermediate CA.
func buildNodeCert(t *testing.T, intCA *IntermediateCA, root *RootCA, dir, nodeID string) VernexCert {
	t.Helper()
	os.WriteFile(filepath.Join(dir, "used_tokens.json"), []byte("{}"), 0600) //nolint:errcheck
	token, err := GenerateEnrollmentToken("vernex-test", 30*24*time.Hour, root.PrivKey)
	if err != nil {
		t.Fatalf("GenerateEnrollmentToken: %v", err)
	}

	nodePub, nodePriv, err := scheme.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pubBytes, _ := nodePub.MarshalBinary()
	csr := &VernexCSR{
		Subject: CertSubject{
			CommonName:         nodeID,
			Organization:       "Vernex Protocol",
			OrganizationalUnit: "Compute Node",
		},
		PublicKey:  base64.StdEncoding.EncodeToString(pubBytes),
		Extensions: CertExtensions{NodeID: nodeID, Role: "Compute Node", CA: false, KeyUsage: "digital_signature"},
	}
	if err := csr.Sign(nodePriv); err != nil {
		t.Fatalf("csr.Sign: %v", err)
	}
	csrBytes, _ := json.Marshal(csr)
	certBytes, err := intCA.SignComputeNodeCSR(csrBytes, &token)
	if err != nil {
		t.Fatalf("SignComputeNodeCSR: %v", err)
	}
	var cert VernexCert
	if err := json.Unmarshal(certBytes, &cert); err != nil {
		t.Fatalf("unmarshal cert: %v", err)
	}
	return cert
}

// TestLoadTrustStore_NoFiles verifies that LoadTrustStore succeeds on an empty
// directory and returns a zero-state store (TOFU mode).
func TestLoadTrustStore_NoFiles(t *testing.T) {
	dir := t.TempDir()
	ts, err := LoadTrustStore(dir)
	if err != nil {
		t.Fatalf("expected no error on empty dir, got: %v", err)
	}
	if ts.RootCert != nil {
		t.Error("expected nil RootCert when no root.crt")
	}
	if len(ts.Intermediates) != 0 {
		t.Errorf("expected 0 intermediates, got %d", len(ts.Intermediates))
	}
}

// TestLoadTrustStore_WithChain verifies that root.crt and intermediate.crt are
// both loaded when present.
func TestLoadTrustStore_WithChain(t *testing.T) {
	dir, _, _ := buildTestChain(t)
	ts, err := LoadTrustStore(dir)
	if err != nil {
		t.Fatalf("LoadTrustStore: %v", err)
	}
	if ts.RootCert == nil {
		t.Error("expected non-nil RootCert after building chain")
	}
	if len(ts.Intermediates) != 1 {
		t.Errorf("expected 1 intermediate, got %d", len(ts.Intermediates))
	}
}

// TestVerifyCert_ValidChain checks that a properly-issued compute-node cert
// passes VerifyCert when the signing intermediate is in the trust store.
func TestVerifyCert_ValidChain(t *testing.T) {
	dir, root, intCA := buildTestChain(t)
	cert := buildNodeCert(t, intCA, root, dir, "VRX-testnode")

	ts, err := LoadTrustStore(dir)
	if err != nil {
		t.Fatalf("LoadTrustStore: %v", err)
	}
	if err := ts.VerifyCert(cert); err != nil {
		t.Errorf("expected valid chain, got: %v", err)
	}
}

// TestVerifyCert_UnknownIssuer checks that VerifyCert returns an error when
// the cert's issuer CN is not in the trust store.
func TestVerifyCert_UnknownIssuer(t *testing.T) {
	dir, root, intCA := buildTestChain(t)
	cert := buildNodeCert(t, intCA, root, dir, "VRX-testnode2")
	cert.Issuer.CommonName = "VRX-impostor"

	ts, err := LoadTrustStore(dir)
	if err != nil {
		t.Fatalf("LoadTrustStore: %v", err)
	}
	if err := ts.VerifyCert(cert); err == nil {
		t.Error("expected error for unknown issuer, got nil")
	}
}

// TestVerifyCert_TamperedSignature checks that a cert with a corrupted ML-DSA
// signature is rejected.
func TestVerifyCert_TamperedSignature(t *testing.T) {
	dir, root, intCA := buildTestChain(t)
	cert := buildNodeCert(t, intCA, root, dir, "VRX-testnode3")
	sig := []byte(cert.Signature)
	for i := len(sig) - 4; i < len(sig); i++ {
		sig[i] ^= 0xFF
	}
	cert.Signature = string(sig)

	ts, err := LoadTrustStore(dir)
	if err != nil {
		t.Fatalf("LoadTrustStore: %v", err)
	}
	if err := ts.VerifyCert(cert); err == nil {
		t.Error("expected error for tampered signature, got nil")
	}
}

// TestVerifyCert_ExpiredCert checks that an expired cert is rejected.
func TestVerifyCert_ExpiredCert(t *testing.T) {
	dir, root, intCA := buildTestChain(t)
	cert := buildNodeCert(t, intCA, root, dir, "VRX-testnode4")
	cert.NotAfter = time.Now().Add(-time.Hour)

	ts, err := LoadTrustStore(dir)
	if err != nil {
		t.Fatalf("LoadTrustStore: %v", err)
	}
	if err := ts.VerifyCert(cert); err == nil {
		t.Error("expected error for expired cert, got nil")
	}
}

// TestAddIntermediate verifies that AddIntermediate accepts a root-signed cert
// and persists it to trusted_intermediates.json so it survives a reload.
func TestAddIntermediate(t *testing.T) {
	dir, _, intCA := buildTestChain(t)

	// Remove intermediate.crt so the store loads without it
	if err := os.Remove(filepath.Join(dir, "intermediate.crt")); err != nil {
		t.Fatalf("remove intermediate.crt: %v", err)
	}
	ts, err := LoadTrustStore(dir)
	if err != nil {
		t.Fatalf("LoadTrustStore: %v", err)
	}
	if len(ts.Intermediates) != 0 {
		t.Fatalf("expected 0 intermediates before AddIntermediate, got %d", len(ts.Intermediates))
	}

	if err := ts.AddIntermediate(*intCA.Cert); err != nil {
		t.Fatalf("AddIntermediate: %v", err)
	}
	if len(ts.Intermediates) != 1 {
		t.Errorf("expected 1 intermediate after AddIntermediate, got %d", len(ts.Intermediates))
	}

	ts2, err := LoadTrustStore(dir)
	if err != nil {
		t.Fatalf("LoadTrustStore after persist: %v", err)
	}
	if len(ts2.Intermediates) != 1 {
		t.Errorf("expected 1 intermediate after reload, got %d", len(ts2.Intermediates))
	}
}
