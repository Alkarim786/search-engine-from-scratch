package frontier

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestDedupAndFIFO(t *testing.T) {
	f := New()
	if !f.Add("http://a.com/1", 0) || f.Add("http://a.com/1", 0) {
		t.Fatal("second Add of the same URL must be rejected")
	}
	f.Add("http://a.com/2", 1)
	ctx := context.Background()
	it, ok := f.Next(ctx)
	if !ok || it.URL != "http://a.com/1" || it.Depth != 0 {
		t.Fatalf("got %+v", it)
	}
	f.Done(it.Host, 0)
	it, _ = f.Next(ctx)
	if it.URL != "http://a.com/2" {
		t.Fatalf("got %+v", it)
	}
	f.Done(it.Host, 0)
	if _, ok := f.Next(ctx); ok {
		t.Fatal("frontier should be exhausted")
	}
}

func TestPerHostDelayEnforced(t *testing.T) {
	f := New()
	for _, p := range []string{"1", "2", "3", "4"} {
		f.Add("http://slow.com/"+p, 0)
	}
	const delay = 60 * time.Millisecond
	var starts []time.Time
	for {
		it, ok := f.Next(context.Background())
		if !ok {
			break
		}
		starts = append(starts, time.Now())
		f.Done(it.Host, delay)
	}
	if len(starts) != 4 {
		t.Fatalf("got %d fetches", len(starts))
	}
	for i := 1; i < len(starts); i++ {
		if gap := starts[i].Sub(starts[i-1]); gap < delay-5*time.Millisecond {
			t.Errorf("gap %d = %v < %v", i, gap, delay)
		}
	}
}

func TestHostsAreIndependentAndNeverConcurrentPerHost(t *testing.T) {
	f := New()
	hosts := []string{"a.com", "b.com", "c.com"}
	for _, h := range hosts {
		for i := 0; i < 5; i++ {
			f.Add("http://"+h+"/"+string(rune('a'+i)), 0)
		}
	}
	var mu sync.Mutex
	active := map[string]int{}
	var maxActive int
	var wg sync.WaitGroup
	start := time.Now()
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				it, ok := f.Next(context.Background())
				if !ok {
					return
				}
				mu.Lock()
				active[it.Host]++
				if active[it.Host] > maxActive {
					maxActive = active[it.Host]
				}
				mu.Unlock()
				time.Sleep(5 * time.Millisecond)
				mu.Lock()
				active[it.Host]--
				mu.Unlock()
				f.Done(it.Host, 20*time.Millisecond)
			}
		}()
	}
	wg.Wait()
	if maxActive != 1 {
		t.Fatalf("max concurrent requests to one host = %d, want 1", maxActive)
	}
	// 5 sequential fetches per host with 20ms delay ≈ 5*5+4*20 = 105ms; hosts run in parallel.
	if el := time.Since(start); el > 400*time.Millisecond {
		t.Fatalf("hosts did not run in parallel: %v", el)
	}
}

func TestNextHonoursContext(t *testing.T) {
	f := New()
	f.Add("http://a.com/1", 0)
	it, _ := f.Next(context.Background())
	f.Done(it.Host, time.Hour)
	f.Add("http://a.com/2", 0)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, ok := f.Next(ctx); ok {
		t.Fatal("expected cancellation while waiting for the host delay")
	}
}
