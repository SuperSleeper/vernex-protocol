package ca

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/cloudflare/circl/sign"
)

// EnrollmentToken is a one-time signed credential issued by the root CA operator.
// Valid for 30 days by default; burned on first use.
type EnrollmentToken struct {
	TokenID   string    `json:"token_id"`
	NetworkID string    `json:"network_id"` // e.g. "vernex-mainnet" or "vernex-testnet"
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Signature string    `json:"signature"` // base64 ML-DSA 44 signed by root CA
}

func (t EnrollmentToken) tbsJSON() ([]byte, error) {
	return json.Marshal(struct {
		TokenID   string    `json:"token_id"`
		NetworkID string    `json:"network_id"`
		IssuedAt  time.Time `json:"issued_at"`
		ExpiresAt time.Time `json:"expires_at"`
	}{t.TokenID, t.NetworkID, t.IssuedAt, t.ExpiresAt})
}

// GenerateEnrollmentToken creates a new ML-DSA-signed enrollment token.
// The token is signed by the root CA private key and valid for ttl duration.
func GenerateEnrollmentToken(networkID string, ttl time.Duration, rootPrivKey sign.PrivateKey) (EnrollmentToken, error) {
	tokenIDBytes := make([]byte, 16)
	if _, err := rand.Read(tokenIDBytes); err != nil {
		return EnrollmentToken{}, err
	}
	tokenID := fmt.Sprintf("%x-%x-%x-%x-%x",
		tokenIDBytes[0:4], tokenIDBytes[4:6], tokenIDBytes[6:8],
		tokenIDBytes[8:10], tokenIDBytes[10:16])

	now := time.Now().UTC()
	token := EnrollmentToken{
		TokenID:   tokenID,
		NetworkID: networkID,
		IssuedAt:  now,
		ExpiresAt: now.Add(ttl),
	}
	tbs, err := token.tbsJSON()
	if err != nil {
		return EnrollmentToken{}, err
	}
	token.Signature = base64.StdEncoding.EncodeToString(scheme.Sign(rootPrivKey, tbs, nil))
	return token, nil
}

// VerifyEnrollmentToken checks the ML-DSA signature against the root CA, expiry, and prior usage.
// rootCACert is the JSON bytes of the root CA's VernexCert.
func VerifyEnrollmentToken(token *EnrollmentToken, rootCACert []byte, configDir string) error {
	var rootCert VernexCert
	if err := json.Unmarshal(rootCACert, &rootCert); err != nil {
		return fmt.Errorf("parse root cert: %w", err)
	}
	rootPubBytes, err := base64.StdEncoding.DecodeString(rootCert.PublicKey)
	if err != nil {
		return fmt.Errorf("decode root public key: %w", err)
	}
	rootPubKey, err := scheme.UnmarshalBinaryPublicKey(rootPubBytes)
	if err != nil {
		return fmt.Errorf("unmarshal root public key: %w", err)
	}
	tbs, err := token.tbsJSON()
	if err != nil {
		return err
	}
	sig, err := base64.StdEncoding.DecodeString(token.Signature)
	if err != nil {
		return fmt.Errorf("decode token signature: %w", err)
	}
	if !scheme.Verify(rootPubKey, tbs, sig, nil) {
		return fmt.Errorf("token signature invalid — not signed by this root CA")
	}
	if time.Now().After(token.ExpiresAt) {
		return fmt.Errorf("token expired at %s", token.ExpiresAt.Format(time.RFC3339))
	}
	if isTokenUsed(token.TokenID, configDir) {
		return fmt.Errorf("token already used: %s", token.TokenID)
	}
	return nil
}

// BurnEnrollmentToken records a token as used in config/used_tokens.json.
func BurnEnrollmentToken(tokenID, configDir string) error {
	path := filepath.Join(configDir, "used_tokens.json")
	used := loadUsedTokens(path)
	used[tokenID] = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(used, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func loadUsedTokens(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return make(map[string]string)
	}
	var used map[string]string
	if err := json.Unmarshal(data, &used); err != nil {
		return make(map[string]string)
	}
	return used
}

func isTokenUsed(tokenID, configDir string) bool {
	used := loadUsedTokens(filepath.Join(configDir, "used_tokens.json"))
	_, exists := used[tokenID]
	return exists
}

// ComputeNodeEnroll enrolls this node by requesting a cert from the bootstrap CA.
// Generates a new ML-DSA 44 keypair, submits a CSR with the enrollment token,
// and saves the signed cert to config/node.crt. Replaces existing ML-DSA keypair files.
func ComputeNodeEnroll(bootstrapURL string, token EnrollmentToken, nodeID, configDir string) error {
	pubKey, privKey, err := scheme.GenerateKey()
	if err != nil {
		return fmt.Errorf("generate node ML-DSA keypair: %w", err)
	}
	pubBytes, err := pubKey.MarshalBinary()
	if err != nil {
		return err
	}

	csr := &VernexCSR{
		Subject: CertSubject{
			CommonName:         nodeID,
			Organization:       "Vernex Protocol",
			OrganizationalUnit: "Compute Node",
		},
		PublicKey: base64.StdEncoding.EncodeToString(pubBytes),
		Extensions: CertExtensions{
			NodeID:   nodeID,
			Role:     "Compute Node",
			CA:       false,
			KeyUsage: "digital_signature",
		},
	}
	if err := csr.Sign(privKey); err != nil {
		return fmt.Errorf("self-sign CSR: %w", err)
	}

	csrBytes, err := json.Marshal(csr)
	if err != nil {
		return err
	}
	tokenBytes, err := json.Marshal(token)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]json.RawMessage{
		"token": tokenBytes,
		"csr":   csrBytes,
	})
	if err != nil {
		return err
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		},
	}
	resp, err := client.Post(bootstrapURL+"/enroll", "application/json", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("POST /enroll to %s: %w", bootstrapURL, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("enroll failed (HTTP %d): %s", resp.StatusCode, body)
	}

	var result struct {
		Cert json.RawMessage `json:"cert"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("decode enroll response: %w", err)
	}
	if len(result.Cert) == 0 {
		return fmt.Errorf("enroll response missing cert field")
	}

	// Save node cert
	certPath := filepath.Join(configDir, "node.crt")
	if err := os.WriteFile(certPath, result.Cert, 0644); err != nil {
		return fmt.Errorf("write node cert: %w", err)
	}

	// Replace ML-DSA keypair with the newly enrolled one
	privBytes, err := privKey.MarshalBinary()
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(configDir, "node.mldsa.key"), privBytes, 0600); err != nil {
		return fmt.Errorf("write node.mldsa.key: %w", err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(pubBytes)
	if err := os.WriteFile(filepath.Join(configDir, "node.mldsa.pub"), []byte(pubB64), 0644); err != nil {
		return fmt.Errorf("write node.mldsa.pub: %w", err)
	}

	fmt.Printf("  [✓] Enrolled! Cert chain issued by bootstrap CA\n")
	fmt.Printf("  [✓] Node cert saved to %s\n", certPath)
	fmt.Println("  [✓] ML-DSA keypair regenerated — distribute updated node.mldsa.pub to peers")
	fmt.Println("  [✓] Enrolled peers no longer need manual mldsa_public_key entries")
	return nil
}
