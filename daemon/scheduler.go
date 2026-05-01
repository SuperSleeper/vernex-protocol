package main

import (
	"container/heap"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// TokenRequest is a work item submitted to the scheduler.
// Class 1 = community benefit (higher priority, higher contribution delta).
// Class 2 = personal benefit (lower priority, lower contribution delta).
type TokenRequest struct {
	Class          int           `json:"class"`
	Prompt         string        `json:"prompt"`
	Model          string        `json:"model,omitempty"`
	Justification  string        `json:"justification,omitempty"`   // required for Class 2 in Phase 4b
	EstimatedCost  float64       `json:"estimated_cost,omitempty"`  // reserved for Phase 4b
	RuntimeCeiling time.Duration `json:"runtime_ceiling,omitempty"` // reserved for graceful degradation
	seq            int64         // internal FIFO ordering within same class
	responseCh     chan tokenResult // result channel; nil for fire-and-forget
}

type tokenResult struct {
	response          string
	routedTo          string
	model             string
	responseTimeMs    int64
	contributionDelta float64
	err               error
}

// requestQueue implements heap.Interface. Lower class = higher priority.
// Within the same class, lower seq = earlier (FIFO).
type requestQueue []*TokenRequest

func (q requestQueue) Len() int { return len(q) }
func (q requestQueue) Less(i, j int) bool {
	if q[i].Class != q[j].Class {
		return q[i].Class < q[j].Class
	}
	return q[i].seq < q[j].seq
}
func (q requestQueue) Swap(i, j int)  { q[i], q[j] = q[j], q[i] }
func (q *requestQueue) Push(x any)    { *q = append(*q, x.(*TokenRequest)) }
func (q *requestQueue) Pop() any {
	old := *q
	n := len(old)
	item := old[n-1]
	*q = old[:n-1]
	return item
}

// Scheduler serialises requests through a priority queue.
// Class 1 runs before Class 2; within each class requests are FIFO.
type Scheduler struct {
	queue   requestQueue
	mu      sync.Mutex
	cond    *sync.Cond
	seqNext int64 // atomic counter for FIFO ordering
}

func NewScheduler() *Scheduler {
	s := &Scheduler{}
	s.cond = sync.NewCond(&s.mu)
	heap.Init(&s.queue)
	return s
}

func (s *Scheduler) Enqueue(req *TokenRequest) {
	s.mu.Lock()
	req.seq = atomic.AddInt64(&s.seqNext, 1)
	heap.Push(&s.queue, req)
	s.cond.Signal()
	s.mu.Unlock()
}

func (s *Scheduler) depth() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.queue.Len()
}

// classCounts returns [class1count, class2count] without holding the lock long.
func (s *Scheduler) classCounts() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var c1, c2 int
	for _, r := range s.queue {
		if r.Class == 1 {
			c1++
		} else {
			c2++
		}
	}
	return c1, c2
}

// run is the single worker goroutine. It processes one request at a time,
// Class 1 before Class 2, FIFO within each class.
func (s *Scheduler) run(node *Node) {
	for {
		s.mu.Lock()
		for s.queue.Len() == 0 {
			s.cond.Wait()
		}
		req := heap.Pop(&s.queue).(*TokenRequest)
		s.mu.Unlock()

		delta := 2.0
		if req.Class == 2 {
			delta = 1.0
		}

		model := req.Model
		if model == "" {
			model = defaultModel
		}

		start := time.Now()
		llmResp, routedTo, err := routedCallOllama(req.Prompt, model, node.ollamaNodes)
		elapsed := time.Since(start).Milliseconds()

		if err == nil {
			node.mu.Lock()
			node.stats.ContributionScore += delta
			node.stats.TotalConnections++
			node.mu.Unlock()
			fmt.Printf("  [✓] scheduler class=%d  %dms  score=%.1f  routed=%s\n",
				req.Class, elapsed, node.stats.ContributionScore, routedTo)
		} else {
			fmt.Printf("  [!] scheduler class=%d error: %v\n", req.Class, err)
		}

		if req.responseCh != nil {
			req.responseCh <- tokenResult{
				response:          llmResp,
				routedTo:          routedTo,
				model:             model,
				responseTimeMs:    elapsed,
				contributionDelta: delta,
				err:               err,
			}
		}
	}
}

// --- Commons Review ---
// Patent-critical mechanism: the system may SUGGEST upgrading a Class 2 request
// to Class 1, but CANNOT reclassify without explicit user consent.
// The consent requirement is legally significant — do not remove or bypass it.

const commonsReviewTTL = 60 * time.Second

type pendingReview struct {
	req         TokenRequest
	reason      string // why the system suggested upgrade
	expiresAt   time.Time
	webSearched bool
	searchQuery string
}

type commonsAssessment struct {
	CommunityBenefit bool   `json:"community_benefit"`
	Reason           string `json:"reason"`
}

func generateReviewID() string {
	b := make([]byte, 6)
	rand.Read(b) //nolint:errcheck
	return "RVW-" + hex.EncodeToString(b)
}

// assessCommunityBenefit asks Mistral whether a prompt has broad community value.
// Returns (shouldReview, reason, error).
func assessCommunityBenefit(prompt string, nodes []ollamaNode) (bool, string, error) {
	assessment := `You are evaluating whether a user request has broad community or educational value that would benefit many people, not just the individual requester. Consider: Is this information publicly useful? Would multiple users benefit from knowing this?

Request: "` + prompt + `"

Respond only with valid JSON and no other text: {"community_benefit": true or false, "reason": "one sentence explanation"}`

	raw, _, err := routedCallOllama(assessment, defaultModel, nodes)
	if err != nil {
		return false, "", err
	}

	// Extract JSON from response (Mistral may include prose before/after)
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start == -1 || end == -1 || end <= start {
		return false, "", fmt.Errorf("no JSON in assessment response")
	}

	var a commonsAssessment
	if err := json.Unmarshal([]byte(raw[start:end+1]), &a); err != nil {
		return false, "", fmt.Errorf("parse assessment: %w", err)
	}
	return a.CommunityBenefit, a.Reason, nil
}

// RateLimiter enforces a per-key sliding-window rate limit.
// For signed inter-node requests the key is the peer's node ID;
// for unsigned local requests the key is the source IP address.
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string][]time.Time
	limit   int
	window  time.Duration
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		buckets: make(map[string][]time.Time),
		limit:   limit,
		window:  window,
	}
}

// Allow returns true if the key is under the rate limit, false if it should be rejected.
func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)

	bucket := rl.buckets[key]
	valid := bucket[:0]
	for _, t := range bucket {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	if len(valid) >= rl.limit {
		rl.buckets[key] = valid
		return false
	}
	rl.buckets[key] = append(valid, now)
	return true
}

// PruneEmpty removes buckets with no activity within the window.
// Called periodically to prevent unbounded memory growth from stale keys.
func (rl *RateLimiter) PruneEmpty() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	cutoff := time.Now().Add(-rl.window)
	for key, bucket := range rl.buckets {
		active := false
		for _, t := range bucket {
			if t.After(cutoff) {
				active = true
				break
			}
		}
		if !active {
			delete(rl.buckets, key)
		}
	}
}

// rateLimitKey returns the rate-limit key for an incoming request:
// the peer node ID for signed inter-node requests, or the source IP for unsigned local ones.
func rateLimitKey(r *http.Request) string {
	if id := r.Header.Get("X-Vernex-Node-ID"); id != "" {
		return id
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// startCommonsReviewExpiry runs the expiry loop that auto-executes stale reviews as Class 2.
func startCommonsReviewExpiry(node *Node) {
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		for range ticker.C {
			now := time.Now()
			node.reviewsMu.Lock()
			for id, rev := range node.reviews {
				if now.After(rev.expiresAt) {
					fmt.Printf("  [~] commons review expired  id=%s — auto-running as Class 2\n", id)
					respCh := make(chan tokenResult, 1)
					node.scheduler.Enqueue(&TokenRequest{
						Class:      rev.req.Class,
						Prompt:     rev.req.Prompt,
						Model:      rev.req.Model,
						responseCh: respCh,
					})
					delete(node.reviews, id)
					go func() { <-respCh }() // drain result; no client waiting
				}
			}
			node.reviewsMu.Unlock()
		}
	}()
}

// startRateLimiterPrune runs the periodic pruning goroutine for the node's rate limiter.
func startRateLimiterPrune(node *Node) {
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		for range ticker.C {
			node.rateLimiter.PruneEmpty()
		}
	}()
}
