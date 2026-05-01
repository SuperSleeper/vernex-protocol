package ca

import (
	"encoding/binary"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// resetNTPServers overrides ntpServers for a test and returns a restore func.
func resetNTPServers(addrs []string) func() {
	orig := ntpServers
	ntpServers = addrs
	return func() { ntpServers = orig }
}

// startMockNTP starts a UDP NTP server that responds with the given timestamp.
// Returns the "host:port" address. Server is closed when the test ends.
func startMockNTP(t *testing.T, ntpTime time.Time) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("startMockNTP: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	go func() {
		buf := make([]byte, 48)
		for {
			_, clientAddr, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			resp := make([]byte, 48)
			resp[0] = 0x24 // LI=0, VN=4, Mode=4 (server)
			ntpSecs := uint32(ntpTime.Unix() + ntpEpochOffset)
			frac := uint32(float64(ntpTime.Nanosecond()) / 1e9 * float64(1<<32))
			binary.BigEndian.PutUint32(resp[40:44], ntpSecs)
			binary.BigEndian.PutUint32(resp[44:48], frac)
			conn.WriteTo(resp, clientAddr) //nolint:errcheck
		}
	}()
	return conn.LocalAddr().String()
}

// Test 1: BuildTime unset — no block regardless of drift source.
func TestBuildTimeUnset(t *testing.T) {
	orig := BuildTime
	BuildTime = ""
	defer func() { BuildTime = orig }()
	defer resetNTPServers([]string{})() // skip NTP

	dir := t.TempDir()
	status := CheckSystemClock(dir, nil)
	if status.BlockCAOps {
		t.Errorf("unexpected BlockCAOps with BuildTime=\"\": %s", status.Message)
	}
}

// Test 2: Clock predates build timestamp — BlockCAOps true, Source "build".
func TestBuildTimeFuture(t *testing.T) {
	orig := BuildTime
	BuildTime = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	defer func() { BuildTime = orig }()

	dir := t.TempDir()
	status := CheckSystemClock(dir, nil)
	if !status.BlockCAOps {
		t.Error("expected BlockCAOps when system clock is before build timestamp")
	}
	if status.Source != "build" {
		t.Errorf("expected source=build, got %q", status.Source)
	}
}

// Test 3: last_seen_time.json shows clock went backwards >24h — BlockCAOps true.
func TestClockBackwardsMoreThan24h(t *testing.T) {
	orig := BuildTime
	BuildTime = ""
	defer func() { BuildTime = orig }()
	defer resetNTPServers([]string{})()

	dir := t.TempDir()
	// Store a last-seen time 25 hours in the future; now appears to be 25h in the past.
	futureUTC := time.Now().Add(25 * time.Hour).UTC().Format(time.RFC3339)
	data, _ := json.Marshal(map[string]string{"utc": futureUTC})
	if err := os.WriteFile(filepath.Join(dir, lastSeenTimeFile), data, 0644); err != nil {
		t.Fatal(err)
	}

	status := CheckSystemClock(dir, nil)
	if !status.BlockCAOps {
		t.Error("expected BlockCAOps when clock appears to have gone backwards >24h")
	}
	if status.Source != "persisted" {
		t.Errorf("expected source=persisted, got %q", status.Source)
	}
}

// Test 4: NTP drift < 1 minute — Verified true, BlockCAOps false, Source "ntp".
func TestNTPDriftSmall(t *testing.T) {
	orig := BuildTime
	BuildTime = ""
	defer func() { BuildTime = orig }()

	mockTime := time.Now().Add(30 * time.Second) // 30s drift — well under 1 min
	addr := startMockNTP(t, mockTime)
	defer resetNTPServers([]string{addr})()

	dir := t.TempDir()
	status := CheckSystemClock(dir, nil)
	if status.BlockCAOps {
		t.Errorf("unexpected BlockCAOps for 30s drift: %s", status.Message)
	}
	if !status.Verified {
		t.Error("expected Verified=true for 30s drift")
	}
	if status.Source != "ntp" {
		t.Errorf("expected source=ntp, got %q", status.Source)
	}
}

// Test 5: NTP drift > 5 minutes — BlockCAOps true, Source "ntp".
func TestNTPDriftLarge(t *testing.T) {
	orig := BuildTime
	BuildTime = ""
	defer func() { BuildTime = orig }()

	mockTime := time.Now().Add(10 * time.Minute) // 10 min drift — exceeds 5 min threshold
	addr := startMockNTP(t, mockTime)
	defer resetNTPServers([]string{addr})()

	dir := t.TempDir()
	status := CheckSystemClock(dir, nil)
	if !status.BlockCAOps {
		t.Error("expected BlockCAOps for 10-minute NTP drift")
	}
	if status.Source != "ntp" {
		t.Errorf("expected source=ntp, got %q", status.Source)
	}
}

// Test 6: All NTP fail + no bootstrap — Source "unverified", BlockCAOps false.
func TestAllNTPTimeoutNoBootstrap(t *testing.T) {
	orig := BuildTime
	BuildTime = ""
	defer func() { BuildTime = orig }()
	defer resetNTPServers([]string{})() // empty list: no NTP queries at all

	dir := t.TempDir()
	status := CheckSystemClock(dir, nil) // nil bootstrapURLs
	if status.BlockCAOps {
		t.Errorf("unexpected BlockCAOps for unverified clock: %s", status.Message)
	}
	if status.Source != "unverified" {
		t.Errorf("expected source=unverified, got %q", status.Source)
	}
}

// Test 7: last_seen_time.json is written after a successful NTP check.
func TestLastSeenTimeWritten(t *testing.T) {
	orig := BuildTime
	BuildTime = ""
	defer func() { BuildTime = orig }()

	addr := startMockNTP(t, time.Now()) // ~0 drift
	defer resetNTPServers([]string{addr})()

	dir := t.TempDir()
	lkgPath := filepath.Join(dir, lastSeenTimeFile)

	if _, err := os.Stat(lkgPath); !os.IsNotExist(err) {
		t.Fatal("last_seen_time.json should not exist before first check")
	}

	status := CheckSystemClock(dir, nil)
	if status.BlockCAOps {
		t.Fatalf("unexpected block: %s", status.Message)
	}

	data, err := os.ReadFile(lkgPath)
	if err != nil {
		t.Fatalf("last_seen_time.json not written: %v", err)
	}
	var stored map[string]string
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("last_seen_time.json invalid JSON: %v", err)
	}
	if stored["utc"] == "" {
		t.Error("last_seen_time.json missing utc field")
	}
	if _, err := time.Parse(time.RFC3339, stored["utc"]); err != nil {
		t.Errorf("last_seen_time.json utc not RFC3339: %v", err)
	}
}
