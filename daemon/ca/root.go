package ca

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudflare/circl/sign"
	"github.com/cloudflare/circl/sign/mldsa/mldsa44"
)

var scheme = mldsa44.Scheme()

// --- Shared certificate types ---

type CertSubject struct {
	CommonName         string `json:"cn"`
	Organization       string `json:"o"`
	OrganizationalUnit string `json:"ou"`
}

type CertExtensions struct {
	NodeID   string `json:"node_id,omitempty"`
	Role     string `json:"role"`
	CA       bool   `json:"ca"`
	PathLen  int    `json:"path_len"`
	KeyUsage string `json:"key_usage"` // "cert_sign,crl_sign" or "digital_signature"
}

// VernexCert is a JSON-encoded ML-DSA-signed credential with X.509-like fields.
// Stored as .crt files (JSON format). Verified at the application layer.
// Full ML-DSA X.509 DER encoding deferred to Go stdlib ML-DSA support (Go 1.24+).
type VernexCert struct {
	Version    int            `json:"version"`
	Serial     string         `json:"serial"`
	Subject    CertSubject    `json:"subject"`
	Issuer     CertSubject    `json:"issuer"`
	NotBefore  time.Time      `json:"not_before"`
	NotAfter   time.Time      `json:"not_after"`
	PublicKey  string         `json:"public_key"` // base64 ML-DSA 44 pub key (1312 bytes)
	Extensions CertExtensions `json:"extensions"`
	Signature  string         `json:"signature"` // base64 ML-DSA sig over tbsJSON
}

// tbsJSON returns canonical JSON of the cert excluding Signature (the bytes that are signed).
func (c VernexCert) tbsJSON() ([]byte, error) {
	return json.Marshal(struct {
		Version    int            `json:"version"`
		Serial     string         `json:"serial"`
		Subject    CertSubject    `json:"subject"`
		Issuer     CertSubject    `json:"issuer"`
		NotBefore  time.Time      `json:"not_before"`
		NotAfter   time.Time      `json:"not_after"`
		PublicKey  string         `json:"public_key"`
		Extensions CertExtensions `json:"extensions"`
	}{c.Version, c.Serial, c.Subject, c.Issuer, c.NotBefore, c.NotAfter, c.PublicKey, c.Extensions})
}

// Sign computes and sets the ML-DSA signature on the cert using the given private key.
func (c *VernexCert) Sign(privKey sign.PrivateKey) error {
	tbs, err := c.tbsJSON()
	if err != nil {
		return err
	}
	c.Signature = base64.StdEncoding.EncodeToString(scheme.Sign(privKey, tbs, nil))
	return nil
}

// Verify checks the ML-DSA signature against issuerPubKey and the validity window.
func (c *VernexCert) Verify(issuerPubKey sign.PublicKey) error {
	tbs, err := c.tbsJSON()
	if err != nil {
		return err
	}
	sig, err := base64.StdEncoding.DecodeString(c.Signature)
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}
	if !scheme.Verify(issuerPubKey, tbs, sig, nil) {
		return fmt.Errorf("ML-DSA signature verification failed")
	}
	now := time.Now()
	if now.Before(c.NotBefore) {
		return fmt.Errorf("cert not yet valid (valid from %s)", c.NotBefore.Format(time.RFC3339))
	}
	if now.After(c.NotAfter) {
		return fmt.Errorf("cert expired at %s", c.NotAfter.Format(time.RFC3339))
	}
	return nil
}

// Fingerprint returns a short hex string derived from the cert's public key (for display).
func (c *VernexCert) Fingerprint() string {
	pubBytes, err := base64.StdEncoding.DecodeString(c.PublicKey)
	if err != nil {
		return "invalid"
	}
	h := sha256.Sum256(pubBytes)
	return fmt.Sprintf("%x", h[:8])
}

// UnmarshalPublicKey deserializes a base64-encoded ML-DSA 44 public key.
// Used by callers outside the package for chain verification.
func UnmarshalPublicKey(b64 string) (sign.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	return scheme.UnmarshalBinaryPublicKey(raw)
}

func newSerial() string {
	b := make([]byte, 16)
	rand.Read(b) //nolint:errcheck
	return new(big.Int).SetBytes(b).Text(16)
}

// --- Shamir Secret Sharing over GF(256) ---
// Uses the AES field polynomial x^8 + x^4 + x^3 + x + 1 (0x11B).
// Each secret byte is independently split; shares are [x_coord | y_0 | y_1 | ...].

func gf256Mul(a, b byte) byte {
	var p byte
	for i := 0; i < 8; i++ {
		if b&1 != 0 {
			p ^= a
		}
		hi := a & 0x80
		a <<= 1
		if hi != 0 {
			a ^= 0x1B
		}
		b >>= 1
	}
	return p
}

func gf256Inv(a byte) byte {
	if a == 0 {
		return 0
	}
	result, base, exp := byte(1), a, 254
	for exp > 0 {
		if exp&1 != 0 {
			result = gf256Mul(result, base)
		}
		base = gf256Mul(base, base)
		exp >>= 1
	}
	return result
}

// ShamirSplit splits secret into N shares requiring K to reconstruct (2 ≤ k ≤ n ≤ 255).
func ShamirSplit(secret []byte, k, n int) ([][]byte, error) {
	if k < 2 || k > n || n > 255 {
		return nil, fmt.Errorf("invalid shamir params: k=%d n=%d (need 2≤k≤n≤255)", k, n)
	}
	shares := make([][]byte, n)
	for i := range shares {
		shares[i] = make([]byte, len(secret)+1)
		shares[i][0] = byte(i + 1) // x coordinate (1..n)
	}
	coeffs := make([]byte, k)
	for byteIdx, secretByte := range secret {
		coeffs[0] = secretByte
		if _, err := rand.Read(coeffs[1:]); err != nil {
			return nil, err
		}
		for shareIdx := range shares {
			x := byte(shareIdx + 1)
			var y byte
			xPow := byte(1)
			for _, c := range coeffs {
				y ^= gf256Mul(c, xPow)
				xPow = gf256Mul(xPow, x)
			}
			shares[shareIdx][byteIdx+1] = y
		}
	}
	return shares, nil
}

// ShamirCombine reconstructs the secret from K or more shares via Lagrange interpolation at x=0.
func ShamirCombine(shares [][]byte) ([]byte, error) {
	if len(shares) == 0 {
		return nil, fmt.Errorf("no shares provided")
	}
	secretLen := len(shares[0]) - 1
	for _, s := range shares {
		if len(s) != secretLen+1 {
			return nil, fmt.Errorf("inconsistent share lengths")
		}
	}
	secret := make([]byte, secretLen)
	for byteIdx := range secret {
		var result byte
		for i, si := range shares {
			xi, yi := si[0], si[byteIdx+1]
			num, den := byte(1), byte(1)
			for j, sj := range shares {
				if i == j {
					continue
				}
				xj := sj[0]
				num = gf256Mul(num, xj)    // numerator: x-xj evaluated at x=0 → 0^xj = xj
				den = gf256Mul(den, xi^xj) // denominator: xi-xj (XOR in GF(2))
			}
			result ^= gf256Mul(yi, gf256Mul(num, gf256Inv(den)))
		}
		secret[byteIdx] = result
	}
	return secret, nil
}

// --- Root CA ---

type RootCA struct {
	PrivKey   sign.PrivateKey // nil in threshold mode after key zeroing
	PubKey    sign.PublicKey
	Cert      *VernexCert
	ConfigDir string
}

// GenerateRootCA creates a new root CA.
// Single mode: saves private key to config/root.key (mode 0600).
// Threshold mode: prints K-of-N Shamir shares to stdout only, zeros key in RAM.
// Always saves the public cert to config/root.crt.
func GenerateRootCA(configDir, nodeID, mode string, k, n int) (*RootCA, error) {
	pubKey, privKey, err := scheme.GenerateKey()
	if err != nil {
		return nil, fmt.Errorf("generate ML-DSA 44 root keypair: %w", err)
	}
	pubBytes, err := pubKey.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("marshal root public key: %w", err)
	}

	now := time.Now().UTC()
	subject := CertSubject{
		CommonName:         nodeID,
		Organization:       "Vernex Protocol",
		OrganizationalUnit: "Root CA",
	}
	cert := &VernexCert{
		Version:   1,
		Serial:    newSerial(),
		Subject:   subject,
		Issuer:    subject, // self-signed
		NotBefore: now,
		NotAfter:  now.AddDate(10, 0, 0),
		PublicKey: base64.StdEncoding.EncodeToString(pubBytes),
		Extensions: CertExtensions{
			NodeID:   nodeID,
			Role:     "Root CA",
			CA:       true,
			PathLen:  2,
			KeyUsage: "cert_sign,crl_sign",
		},
	}
	if err := cert.Sign(privKey); err != nil {
		return nil, fmt.Errorf("self-sign root cert: %w", err)
	}

	certData, err := json.MarshalIndent(cert, "", "  ")
	if err != nil {
		return nil, err
	}
	certPath := filepath.Join(configDir, "root.crt")
	if err := os.WriteFile(certPath, certData, 0644); err != nil {
		return nil, fmt.Errorf("write root cert: %w", err)
	}
	fmt.Printf("  [✓] Root CA cert saved to %s\n", certPath)

	privBytes, err := privKey.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("marshal root private key: %w", err)
	}

	if mode == "threshold" {
		shares, err := ShamirSplit(privBytes, k, n)
		if err != nil {
			return nil, fmt.Errorf("shamir split: %w", err)
		}
		fmt.Printf("\n  ╔══════════════════════════════════════════════════════════╗\n")
		fmt.Printf("  ║  ROOT CA KEY — THRESHOLD MODE (%d-of-%d Shamir shares)    ║\n", k, n)
		fmt.Printf("  ║  Distribute each share to a separate trusted custodian.  ║\n")
		fmt.Printf("  ║  Shares are NOT written to disk. Copy them NOW.           ║\n")
		fmt.Printf("  ╚══════════════════════════════════════════════════════════╝\n\n")
		for i, share := range shares {
			fmt.Printf("  Share %d/%d:\n  %s\n\n", i+1, n, base64.StdEncoding.EncodeToString(share))
		}
		for i := range privBytes {
			privBytes[i] = 0
		}
		return &RootCA{Cert: cert, ConfigDir: configDir}, nil
	}

	// single mode: save key to disk
	keyPath := filepath.Join(configDir, "root.key")
	if err := os.WriteFile(keyPath, privBytes, 0600); err != nil {
		return nil, fmt.Errorf("write root key: %w", err)
	}
	fmt.Printf("  [✓] Root CA key saved to %s (mode 0600)\n", keyPath)
	return &RootCA{PrivKey: privKey, PubKey: pubKey, Cert: cert, ConfigDir: configDir}, nil
}

// LoadRootCA loads the root CA from disk (single mode only).
func LoadRootCA(configDir string) (*RootCA, error) {
	keyBytes, err := os.ReadFile(filepath.Join(configDir, "root.key"))
	if err != nil {
		return nil, fmt.Errorf("read root.key: %w", err)
	}
	privKey, err := scheme.UnmarshalBinaryPrivateKey(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("unmarshal root private key: %w", err)
	}
	pubKey := privKey.Public().(sign.PublicKey)

	certData, err := os.ReadFile(filepath.Join(configDir, "root.crt"))
	if err != nil {
		return nil, fmt.Errorf("read root.crt: %w", err)
	}
	var cert VernexCert
	if err := json.Unmarshal(certData, &cert); err != nil {
		return nil, fmt.Errorf("parse root cert: %w", err)
	}
	return &RootCA{PrivKey: privKey, PubKey: pubKey, Cert: &cert, ConfigDir: configDir}, nil
}

// LoadRootCAFromShares reconstructs the root CA in RAM from K base64-encoded Shamir shares.
// The private key is never written to disk; raw bytes are zeroed after unmarshaling.
func LoadRootCAFromShares(configDir string, shareStrings []string) (*RootCA, error) {
	shares := make([][]byte, len(shareStrings))
	for i, s := range shareStrings {
		b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
		if err != nil {
			return nil, fmt.Errorf("decode share %d: %w", i+1, err)
		}
		shares[i] = b
	}
	privBytes, err := ShamirCombine(shares)
	if err != nil {
		return nil, fmt.Errorf("shamir combine: %w", err)
	}
	privKey, err := scheme.UnmarshalBinaryPrivateKey(privBytes)
	for i := range privBytes {
		privBytes[i] = 0 // zero raw bytes regardless of error
	}
	if err != nil {
		return nil, fmt.Errorf("unmarshal reconstructed root key: %w", err)
	}
	pubKey := privKey.Public().(sign.PublicKey)

	certData, err := os.ReadFile(filepath.Join(configDir, "root.crt"))
	if err != nil {
		return nil, fmt.Errorf("read root.crt: %w", err)
	}
	var cert VernexCert
	if err := json.Unmarshal(certData, &cert); err != nil {
		return nil, fmt.Errorf("parse root cert: %w", err)
	}
	return &RootCA{PrivKey: privKey, PubKey: pubKey, Cert: &cert, ConfigDir: configDir}, nil
}

// SignIntermediateCSR verifies the self-signed CSR and issues a 3-year intermediate CA cert.
// BasicConstraints: CA:true, pathLen:1.
func (r *RootCA) SignIntermediateCSR(csrBytes []byte) (*VernexCert, error) {
	if r.PrivKey == nil {
		return nil, fmt.Errorf("root CA private key not loaded (use LoadRootCA or LoadRootCAFromShares)")
	}
	var csr VernexCSR
	if err := json.Unmarshal(csrBytes, &csr); err != nil {
		return nil, fmt.Errorf("parse intermediate CSR: %w", err)
	}
	if err := csr.Verify(); err != nil {
		return nil, fmt.Errorf("CSR self-signature invalid: %w", err)
	}
	now := time.Now().UTC()
	cert := &VernexCert{
		Version:   1,
		Serial:    newSerial(),
		Subject:   csr.Subject,
		Issuer:    r.Cert.Subject,
		NotBefore: now,
		NotAfter:  now.AddDate(3, 0, 0),
		PublicKey: csr.PublicKey,
		Extensions: CertExtensions{
			NodeID:   csr.Extensions.NodeID,
			Role:     "Bootstrap Intermediate CA",
			CA:       true,
			PathLen:  1,
			KeyUsage: "cert_sign,crl_sign",
		},
	}
	if err := cert.Sign(r.PrivKey); err != nil {
		return nil, fmt.Errorf("sign intermediate cert: %w", err)
	}
	return cert, nil
}
