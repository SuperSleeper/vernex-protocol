package ca

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cloudflare/circl/sign"
)

// VernexCSR is a certificate signing request, self-signed by the requesting node.
type VernexCSR struct {
	Subject    CertSubject    `json:"subject"`
	PublicKey  string         `json:"public_key"` // base64 ML-DSA 44 pub key
	Extensions CertExtensions `json:"extensions"`
	Signature  string         `json:"signature"` // ML-DSA self-signature over tbsJSON
}

func (c VernexCSR) tbsJSON() ([]byte, error) {
	return json.Marshal(struct {
		Subject    CertSubject    `json:"subject"`
		PublicKey  string         `json:"public_key"`
		Extensions CertExtensions `json:"extensions"`
	}{c.Subject, c.PublicKey, c.Extensions})
}

// Sign self-signs the CSR using the private key corresponding to the embedded public key.
func (c *VernexCSR) Sign(privKey sign.PrivateKey) error {
	tbs, err := c.tbsJSON()
	if err != nil {
		return err
	}
	c.Signature = base64.StdEncoding.EncodeToString(scheme.Sign(privKey, tbs, nil))
	return nil
}

// Verify checks the CSR's self-signature using the embedded public key.
func (c *VernexCSR) Verify() error {
	pubBytes, err := base64.StdEncoding.DecodeString(c.PublicKey)
	if err != nil {
		return fmt.Errorf("decode public key: %w", err)
	}
	pubKey, err := scheme.UnmarshalBinaryPublicKey(pubBytes)
	if err != nil {
		return fmt.Errorf("unmarshal public key: %w", err)
	}
	tbs, err := c.tbsJSON()
	if err != nil {
		return err
	}
	sig, err := base64.StdEncoding.DecodeString(c.Signature)
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}
	if !scheme.Verify(pubKey, tbs, sig, nil) {
		return fmt.Errorf("CSR self-signature verification failed")
	}
	return nil
}

// IntermediateCA holds the intermediate CA keypair and cert (signed by root CA).
// Issues 1-year compute node certs against verified enrollment tokens.
type IntermediateCA struct {
	PrivKey   sign.PrivateKey
	PubKey    sign.PublicKey
	Cert      *VernexCert
	ConfigDir string
}

// GenerateIntermediateCA creates a new intermediate CA keypair and self-signed CSR.
// Saves config/intermediate.key (mode 0600) and config/intermediate.csr.
// Submit the returned CSR to a root CA (POST /sign-intermediate or vernex-node ca init-intermediate).
func GenerateIntermediateCA(configDir, nodeID string) (*IntermediateCA, *VernexCSR, error) {
	pubKey, privKey, err := scheme.GenerateKey()
	if err != nil {
		return nil, nil, fmt.Errorf("generate ML-DSA 44 intermediate keypair: %w", err)
	}
	pubBytes, err := pubKey.MarshalBinary()
	if err != nil {
		return nil, nil, err
	}

	csr := &VernexCSR{
		Subject: CertSubject{
			CommonName:         nodeID,
			Organization:       "Vernex Protocol",
			OrganizationalUnit: "Bootstrap Intermediate CA",
		},
		PublicKey: base64.StdEncoding.EncodeToString(pubBytes),
		Extensions: CertExtensions{
			NodeID:   nodeID,
			Role:     "Bootstrap Intermediate CA",
			CA:       true,
			PathLen:  1,
			KeyUsage: "cert_sign,crl_sign",
		},
	}
	if err := csr.Sign(privKey); err != nil {
		return nil, nil, fmt.Errorf("self-sign intermediate CSR: %w", err)
	}

	privBytes, err := privKey.MarshalBinary()
	if err != nil {
		return nil, nil, err
	}
	keyPath := filepath.Join(configDir, "intermediate.key")
	if err := os.WriteFile(keyPath, privBytes, 0600); err != nil {
		return nil, nil, fmt.Errorf("write intermediate key: %w", err)
	}
	csrBytes, err := json.MarshalIndent(csr, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	csrPath := filepath.Join(configDir, "intermediate.csr")
	if err := os.WriteFile(csrPath, csrBytes, 0644); err != nil {
		return nil, nil, fmt.Errorf("write intermediate CSR: %w", err)
	}
	fmt.Printf("  [✓] Intermediate CA key saved to %s\n", keyPath)
	fmt.Printf("  [✓] Intermediate CA CSR saved to %s\n", csrPath)
	return &IntermediateCA{PrivKey: privKey, PubKey: pubKey, ConfigDir: configDir}, csr, nil
}

// LoadIntermediateCA loads the intermediate CA key and cert from disk.
// Requires both config/intermediate.key and config/intermediate.crt to exist.
func LoadIntermediateCA(configDir string) (*IntermediateCA, error) {
	keyBytes, err := os.ReadFile(filepath.Join(configDir, "intermediate.key"))
	if err != nil {
		return nil, fmt.Errorf("read intermediate.key: %w", err)
	}
	privKey, err := scheme.UnmarshalBinaryPrivateKey(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("unmarshal intermediate private key: %w", err)
	}
	pubKey := privKey.Public().(sign.PublicKey)

	certData, err := os.ReadFile(filepath.Join(configDir, "intermediate.crt"))
	if err != nil {
		return nil, fmt.Errorf("read intermediate.crt: %w", err)
	}
	var cert VernexCert
	if err := json.Unmarshal(certData, &cert); err != nil {
		return nil, fmt.Errorf("parse intermediate cert: %w", err)
	}
	return &IntermediateCA{PrivKey: privKey, PubKey: pubKey, Cert: &cert, ConfigDir: configDir}, nil
}

// SignComputeNodeCSR verifies the enrollment token against config/root.crt and issues a 1-year cert.
// The token is burned (marked used) before cert issuance to prevent replay.
// BasicConstraints: CA:false.
func (ica *IntermediateCA) SignComputeNodeCSR(csrBytes []byte, token *EnrollmentToken) ([]byte, error) {
	if ica.PrivKey == nil {
		return nil, fmt.Errorf("intermediate CA private key not loaded")
	}
	rootCertData, err := os.ReadFile(filepath.Join(ica.ConfigDir, "root.crt"))
	if err != nil {
		return nil, fmt.Errorf("load root.crt for token verification: %w", err)
	}
	if err := VerifyEnrollmentToken(token, rootCertData, ica.ConfigDir); err != nil {
		return nil, fmt.Errorf("enrollment token rejected: %w", err)
	}

	var csr VernexCSR
	if err := json.Unmarshal(csrBytes, &csr); err != nil {
		return nil, fmt.Errorf("parse compute node CSR: %w", err)
	}
	if err := csr.Verify(); err != nil {
		return nil, fmt.Errorf("compute node CSR self-signature invalid: %w", err)
	}

	// Burn token before signing — even if signing fails, the token cannot be reused
	if err := BurnEnrollmentToken(token.TokenID, ica.ConfigDir); err != nil {
		return nil, fmt.Errorf("burn token: %w", err)
	}

	now := time.Now().UTC()
	cert := &VernexCert{
		Version:   1,
		Serial:    newSerial(),
		Subject:   csr.Subject,
		Issuer:    ica.Cert.Subject,
		NotBefore: now,
		NotAfter:  now.AddDate(1, 0, 0),
		PublicKey: csr.PublicKey,
		Extensions: CertExtensions{
			NodeID:   csr.Extensions.NodeID,
			Role:     "Compute Node",
			CA:       false,
			PathLen:  0,
			KeyUsage: "digital_signature",
		},
	}
	if err := cert.Sign(ica.PrivKey); err != nil {
		return nil, fmt.Errorf("sign compute node cert: %w", err)
	}
	certBytes, err := json.MarshalIndent(cert, "", "  ")
	if err != nil {
		return nil, err
	}
	fmt.Printf("  [✓] Compute node cert issued: cn=%s  expires=%s\n",
		cert.Subject.CommonName, cert.NotAfter.Format("2006-01-02"))
	return certBytes, nil
}
