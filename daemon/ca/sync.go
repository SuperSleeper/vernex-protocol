package ca

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// CASyncPayload is the gossip payload returned by /ca-sync.
type CASyncPayload struct {
	NodeID           string          `json:"node_id,omitempty"`
	Timestamp        time.Time       `json:"timestamp"`
	RootCert         json.RawMessage `json:"root_cert,omitempty"`
	IntermediateCert json.RawMessage `json:"intermediate_cert,omitempty"`
}

// HandleCASync returns an HTTP handler that serves known CA certs for gossip propagation.
// Safe to expose publicly — only distributes public cert material, no private keys.
func HandleCASync(configDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		payload := CASyncPayload{Timestamp: time.Now().UTC()}
		if data, err := os.ReadFile(filepath.Join(configDir, "root.crt")); err == nil {
			payload.RootCert = json.RawMessage(data)
		}
		if data, err := os.ReadFile(filepath.Join(configDir, "intermediate.crt")); err == nil {
			payload.IntermediateCert = json.RawMessage(data)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(payload) //nolint:errcheck
	}
}

// PullCASync fetches CA certs from a peer and stores any new ones locally.
// Verifies chain before accepting: root must be self-signed, intermediate must be root-signed.
// No-op if the local cert file already exists (never overwrites).
func PullCASync(peerURL, configDir string, client *http.Client) error {
	resp, err := client.Get(peerURL + "/ca-sync")
	if err != nil {
		return fmt.Errorf("GET /ca-sync from %s: %w", peerURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ca-sync returned HTTP %d", resp.StatusCode)
	}

	var payload CASyncPayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return fmt.Errorf("decode ca-sync payload: %w", err)
	}

	updated := false
	rootPath := filepath.Join(configDir, "root.crt")

	// Accept root cert only if we don't have one AND it is valid self-signed
	if len(payload.RootCert) > 0 {
		if _, err := os.Stat(rootPath); os.IsNotExist(err) {
			var cert VernexCert
			if err := json.Unmarshal(payload.RootCert, &cert); err == nil {
				if err := verifySelfSigned(&cert); err == nil {
					if err := os.WriteFile(rootPath, payload.RootCert, 0644); err == nil {
						fmt.Println("  [✓] CA sync: saved root cert from peer")
						updated = true
					}
				} else {
					fmt.Printf("  [!] CA sync: rejected root cert — %v\n", err)
				}
			}
		}
	}

	// Accept intermediate cert only if we have root AND the intermediate is root-signed
	intPath := filepath.Join(configDir, "intermediate.crt")
	if len(payload.IntermediateCert) > 0 {
		if _, err := os.Stat(intPath); os.IsNotExist(err) {
			rootData, err := os.ReadFile(rootPath)
			if err == nil {
				var rootCert VernexCert
				if err := json.Unmarshal(rootData, &rootCert); err == nil {
					var intCert VernexCert
					if err := json.Unmarshal(payload.IntermediateCert, &intCert); err == nil {
						rootPubBytes, _ := base64.StdEncoding.DecodeString(rootCert.PublicKey)
						rootPubKey, err := scheme.UnmarshalBinaryPublicKey(rootPubBytes)
						if err == nil {
							if err := intCert.Verify(rootPubKey); err == nil {
								if err := os.WriteFile(intPath, payload.IntermediateCert, 0644); err == nil {
									fmt.Println("  [✓] CA sync: saved intermediate cert from peer")
									updated = true
								}
							} else {
								fmt.Printf("  [!] CA sync: rejected intermediate cert — chain validation failed: %v\n", err)
							}
						}
					}
				}
			}
		}
	}

	if !updated {
		fmt.Println("  [~] CA sync: no new certs from peer")
	}
	return nil
}

// verifySelfSigned checks that a cert is self-signed and signature-valid.
func verifySelfSigned(cert *VernexCert) error {
	if cert.Subject.CommonName != cert.Issuer.CommonName {
		return fmt.Errorf("subject CN=%s differs from issuer CN=%s (not self-signed)",
			cert.Subject.CommonName, cert.Issuer.CommonName)
	}
	pubBytes, err := base64.StdEncoding.DecodeString(cert.PublicKey)
	if err != nil {
		return fmt.Errorf("decode public key: %w", err)
	}
	pubKey, err := scheme.UnmarshalBinaryPublicKey(pubBytes)
	if err != nil {
		return fmt.Errorf("unmarshal public key: %w", err)
	}
	return cert.Verify(pubKey)
}
