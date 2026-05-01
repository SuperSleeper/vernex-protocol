package ca

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// BuildTime is set via -ldflags "-X vernex/daemon/ca.BuildTime=<RFC3339>" at build time.
// Empty string disables the build-time consistency check.
var BuildTime = ""

// ClockStatus is the result of CheckSystemClock.
type ClockStatus struct {
	Verified   bool
	Drift      time.Duration
	Source     string // "ntp", "bootstrap", "build", "persisted", "unverified"
	BlockCAOps bool
	Message    string
}

const lastSeenTimeFile = "last_seen_time.json"

// ntpEpochOffset is the number of seconds between the NTP epoch (Jan 1 1900) and the
// Unix epoch (Jan 1 1970): 70 years = 70*365 + 17 leap-year days = 25567 days.
const ntpEpochOffset int64 = 2208988800

// ntpServers queried in parallel. Package-level var so tests can override without
// recompilation.
var ntpServers = []string{
	"time.cloudflare.com:123",
	"0.pool.ntp.org:123",
	"1.pool.ntp.org:123",
}

// CheckSystemClock verifies the local clock via four ordered steps:
//
//	A — build-timestamp consistency (needs -ldflags BuildTime)
//	B — last-known-good regression guard
//	C — NTP median consensus (pure UDP, RFC 5905)
//	D — bootstrap /time endpoint fallback
//
// BlockCAOps is set true only when clock fraud is unambiguous (steps A/B/C/D
// all report drift > 5 min). Unverified (all sources unreachable) never blocks.
func CheckSystemClock(configDir string, bootstrapURLs []string) ClockStatus {
	now := time.Now()

	// ── Step A: build-timestamp consistency ───────────────────────────────────
	if BuildTime != "" {
		buildTime, err := time.Parse(time.RFC3339, BuildTime)
		if err == nil && now.Before(buildTime) {
			return ClockStatus{
				Source:     "build",
				BlockCAOps: true,
				Message: fmt.Sprintf(
					"system clock (%s) predates build timestamp (%s)",
					now.UTC().Format(time.RFC3339), BuildTime),
			}
		}
	}

	// ── Step B: last-known-good regression ────────────────────────────────────
	lkgPath := filepath.Join(configDir, lastSeenTimeFile)
	if data, err := os.ReadFile(lkgPath); err == nil {
		var stored struct {
			UTC string `json:"utc"`
		}
		if json.Unmarshal(data, &stored) == nil {
			if prev, err := time.Parse(time.RFC3339, stored.UTC); err == nil {
				if prev.Sub(now) > 24*time.Hour {
					return ClockStatus{
						Source:     "persisted",
						BlockCAOps: true,
						Message: fmt.Sprintf(
							"clock went backwards more than 24h since last run (last: %s, now: %s)",
							prev.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339)),
					}
				}
			}
		}
	}

	// ── Step C: NTP median ────────────────────────────────────────────────────
	if ntpTime, ok := queryNTPMedian(); ok {
		drift := absDuration(now.Sub(ntpTime))
		st := ClockStatus{Verified: true, Drift: drift, Source: "ntp"}
		if drift > 5*time.Minute {
			st.BlockCAOps = true
			st.Verified = false
			st.Message = fmt.Sprintf(
				"NTP drift %s exceeds 5-minute threshold", drift.Round(time.Second))
		}
		if !st.BlockCAOps {
			saveLastSeenTime(configDir)
		}
		return st
	}

	// ── Step D: bootstrap /time fallback ──────────────────────────────────────
	for _, burl := range bootstrapURLs {
		if t, ok := fetchBootstrapTime(burl, configDir); ok {
			drift := absDuration(now.Sub(t))
			st := ClockStatus{Verified: true, Drift: drift, Source: "bootstrap"}
			if drift > 5*time.Minute {
				st.BlockCAOps = true
				st.Verified = false
				st.Message = fmt.Sprintf(
					"bootstrap time drift %s exceeds 5-minute threshold", drift.Round(time.Second))
			}
			if !st.BlockCAOps {
				saveLastSeenTime(configDir)
			}
			return st
		}
	}

	// ── Unverified ────────────────────────────────────────────────────────────
	return ClockStatus{
		Source:     "unverified",
		BlockCAOps: false,
		Message:    "clock unverified — NTP and bootstrap peers unreachable",
	}
}

// BlockIfClockInvalid returns an error when status.BlockCAOps is true.
func BlockIfClockInvalid(status ClockStatus) error {
	if status.BlockCAOps {
		return fmt.Errorf("%s", status.Message)
	}
	return nil
}

// ── NTP (pure UDP, RFC 5905) ──────────────────────────────────────────────────

// queryNTPMedian queries ntpServers in parallel and returns the median transmit
// timestamp from all servers that respond within the 3-second per-server deadline.
func queryNTPMedian() (time.Time, bool) {
	type ntpResult struct {
		t  time.Time
		ok bool
	}
	results := make([]ntpResult, len(ntpServers))
	var wg sync.WaitGroup
	for i, srv := range ntpServers {
		wg.Add(1)
		go func(idx int, addr string) {
			defer wg.Done()
			t, err := queryNTPServer(addr)
			results[idx] = ntpResult{t: t, ok: err == nil}
		}(i, srv)
	}
	wg.Wait()

	var times []time.Time
	for _, r := range results {
		if r.ok {
			times = append(times, r.t)
		}
	}
	if len(times) == 0 {
		return time.Time{}, false
	}
	// Insertion sort (at most 3 elements in practice).
	for i := 1; i < len(times); i++ {
		for j := i; j > 0 && times[j].Before(times[j-1]); j-- {
			times[j], times[j-1] = times[j-1], times[j]
		}
	}
	return times[len(times)/2], true
}

// queryNTPServer performs a single NTP exchange against addr and returns the
// transmit timestamp. Implements the minimal RFC 5905 client: send a 48-byte
// request (LI=0, VN=4, Mode=3) and read the transmit timestamp from bytes 40–47
// of the response.
func queryNTPServer(addr string) (time.Time, error) {
	conn, err := net.DialTimeout("udp", addr, 3*time.Second)
	if err != nil {
		return time.Time{}, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck

	// Byte 0: LI=0 (2 bits) | VN=4 (3 bits) | Mode=3/client (3 bits) = 0b00_100_011 = 0x23
	req := make([]byte, 48)
	req[0] = 0x23
	if _, err := conn.Write(req); err != nil {
		return time.Time{}, err
	}

	resp := make([]byte, 48)
	if _, err := conn.Read(resp); err != nil {
		return time.Time{}, err
	}

	// Transmit timestamp: bytes 40–43 (seconds since NTP epoch) + 44–47 (32-bit fraction).
	secs := binary.BigEndian.Uint32(resp[40:44])
	frac := binary.BigEndian.Uint32(resp[44:48])
	if secs == 0 {
		return time.Time{}, fmt.Errorf("NTP response has zero transmit timestamp")
	}

	unixSecs := int64(secs) - ntpEpochOffset
	nsec := int64(math.Round(float64(frac) / float64(1<<32) * 1e9))
	return time.Unix(unixSecs, nsec), nil
}

// ── Bootstrap time check ──────────────────────────────────────────────────────

// fetchBootstrapTime fetches /time from a bootstrap peer, optionally verifying
// the ML-DSA signature using the peer's VernexCert when the local TrustStore has
// a root cert. Falls through to TOFU accept when TrustStore is not yet populated
// (consistent with TLS behaviour until the CA is fully deployed).
func fetchBootstrapTime(bootstrapURL string, configDir string) (time.Time, bool) {
	ts, _ := LoadTrustStore(configDir)
	client := ts.NewTLSClient(5 * time.Second)

	resp, err := client.Get(bootstrapURL + "/time")
	if err != nil {
		return time.Time{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return time.Time{}, false
	}

	var payload struct {
		UTC       string `json:"utc"`
		NodeID    string `json:"node_id"`
		Signature string `json:"signature"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil || payload.UTC == "" {
		return time.Time{}, false
	}

	t, err := time.Parse(time.RFC3339, payload.UTC)
	if err != nil {
		return time.Time{}, false
	}

	// Verify ML-DSA signature when TrustStore has a root cert (enrolled mode).
	if ts.RootCert != nil && payload.Signature != "" && payload.NodeID != "" {
		cert, cerr := FetchPeerCert(bootstrapURL, client)
		if cerr == nil {
			if verr := ts.VerifyCert(*cert); verr == nil {
				pubBytes, err := base64.StdEncoding.DecodeString(cert.PublicKey)
				if err == nil {
					pub, err := scheme.UnmarshalBinaryPublicKey(pubBytes)
					if err == nil {
						sig, err := base64.StdEncoding.DecodeString(payload.Signature)
						if err != nil || !scheme.Verify(pub, []byte(payload.UTC+"|"+payload.NodeID), sig, nil) {
							return time.Time{}, false
						}
					}
				}
			}
		}
	}

	return t, true
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// saveLastSeenTime persists the current UTC time so the next run can detect
// backwards clock jumps (Step B).
func saveLastSeenTime(configDir string) {
	data, _ := json.Marshal(map[string]string{"utc": time.Now().UTC().Format(time.RFC3339)})
	os.WriteFile(filepath.Join(configDir, lastSeenTimeFile), data, 0644) //nolint:errcheck
}
