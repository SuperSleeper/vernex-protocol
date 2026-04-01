#!/bin/bash
# Priority ordering validation — Class 1 should complete before Class 2
# under concurrent load. Fires 3x Class 2 + 2x Class 1 simultaneously,
# records completion order and response times.

API="http://localhost:7701/submit"
TMPDIR=$(mktemp -d)
START=$(date +%s%3N)

echo "=== Vernex Priority Queue Test ==="
echo "Firing 3x Class 2 + 2x Class 1 simultaneously..."
echo ""

submit() {
    local class=$1
    local label=$2
    local prompt=$3
    local t0=$(date +%s%3N)
    local result
    result=$(curl -s -X POST "$API" \
        -H "Content-Type: application/json" \
        -d "{\"prompt\": \"$prompt\", \"class\": $class}")
    local t1=$(date +%s%3N)
    local elapsed=$((t1 - t0))
    local offset=$((t0 - START))
    echo "$label  class=$class  queued_at=+${offset}ms  completed_at=+$((t1 - START))ms  duration=${elapsed}ms"
}

# Fire all requests simultaneously in background
submit 2 "C2-A" "Write a poem about autumn leaves." &
submit 2 "C2-B" "Explain what a CPU does." &
submit 2 "C2-C" "Describe how rainbows form." &
submit 1 "C1-A" "What is a distributed hash table?" &
submit 1 "C1-B" "Define consensus in distributed systems." &

# Wait for all to finish
wait

echo ""
echo "=== Queue state after test ==="
curl -s http://localhost:7701/queue | jq
echo ""
echo "=== Expected: C1-A and C1-B complete before C2-A, C2-B, C2-C ==="
echo "(C2 requests that were already processing when C1 arrived may finish first)"
