// Package frontier is the crawl scheduler: one FIFO queue per host (so the
// crawl is breadth-first within a host), a minimum delay between two requests
// to the same host, at most one in-flight request per host, and a global
// seen-set so no URL is queued twice.
package frontier

import (
	"context"
	"net/url"
	"sync"
	"time"
)

// Item is one URL to fetch.
type Item struct {
	URL   string
	Host  string
	Depth int
}

type hostQueue struct {
	items []Item
	next  time.Time // earliest time the next request to this host may start
	busy  bool
}

// Frontier is safe for concurrent use.
type Frontier struct {
	mu     sync.Mutex
	hosts  map[string]*hostQueue
	seen   map[string]struct{}
	queued int
	busy   int
	// Poll bounds how long Next sleeps when every ready host is busy.
	poll time.Duration
}

// New returns an empty frontier.
func New() *Frontier {
	return &Frontier{hosts: map[string]*hostQueue{}, seen: map[string]struct{}{}, poll: 2 * time.Millisecond}
}

// MarkSeen records u as seen and reports whether it was new.
func (f *Frontier) MarkSeen(u string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.seen[u]; ok {
		return false
	}
	f.seen[u] = struct{}{}
	return true
}

// Seen returns the number of distinct URLs ever added or marked.
func (f *Frontier) Seen() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.seen)
}

// Add queues a canonical URL unless it was seen before; it reports whether
// the URL was queued.
func (f *Frontier) Add(rawURL string, depth int) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.seen[rawURL]; ok {
		return false
	}
	f.seen[rawURL] = struct{}{}
	hq := f.hosts[u.Host]
	if hq == nil {
		hq = &hostQueue{}
		f.hosts[u.Host] = hq
	}
	hq.items = append(hq.items, Item{URL: rawURL, Host: u.Host, Depth: depth})
	f.queued++
	return true
}

// Next blocks until some host is ready and returns its next URL, marking the
// host busy. It returns false when the frontier is exhausted (nothing queued
// and nothing in flight) or ctx is cancelled. Every successful Next must be
// paired with a Done call.
func (f *Frontier) Next(ctx context.Context) (Item, bool) {
	for {
		f.mu.Lock()
		now := time.Now()
		var pick *hostQueue
		var wake time.Time
		for _, hq := range f.hosts {
			if len(hq.items) == 0 || hq.busy {
				continue
			}
			if !hq.next.After(now) {
				if pick == nil || hq.next.Before(pick.next) {
					pick = hq
				}
			} else if wake.IsZero() || hq.next.Before(wake) {
				wake = hq.next
			}
		}
		if pick != nil {
			it := pick.items[0]
			pick.items = pick.items[1:]
			pick.busy = true
			f.queued--
			f.busy++
			f.mu.Unlock()
			return it, true
		}
		if f.queued == 0 && f.busy == 0 {
			f.mu.Unlock()
			return Item{}, false
		}
		sleep := f.poll
		if !wake.IsZero() {
			if d := wake.Sub(now); d < sleep || f.busy == 0 {
				sleep = d
			}
		}
		f.mu.Unlock()
		t := time.NewTimer(sleep)
		select {
		case <-ctx.Done():
			t.Stop()
			return Item{}, false
		case <-t.C:
		}
	}
}

// Done releases the host and forbids another request to it for delay.
func (f *Frontier) Done(host string, delay time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if hq := f.hosts[host]; hq != nil {
		hq.busy = false
		hq.next = time.Now().Add(delay)
		f.busy--
	}
}

// Pending returns the number of queued (not yet started) URLs.
func (f *Frontier) Pending() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.queued
}
