package domain

import (
	"sync"
	"testing"
	"time"
)

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

func TestIDGenLayout(t *testing.T) {
	t.Parallel()
	at := IDEpoch.Add(1234567 * time.Millisecond)
	g, err := NewIDGen(37, fixedClock(at))
	if err != nil {
		t.Fatal(err)
	}
	id := g.New()
	if id <= 0 {
		t.Fatalf("id must be positive, got %d", id)
	}
	if got := id.Time(); !got.Equal(at) {
		t.Errorf("Time() = %v, want %v", got, at)
	}
	if got := id.Node(); got != 37 {
		t.Errorf("Node() = %d, want 37", got)
	}
	if id.String() != "" && id.String()[0] == '-' {
		t.Errorf("String() = %q, want decimal without sign", id.String())
	}
}

func TestIDGenRejectsNodeOutOfRange(t *testing.T) {
	t.Parallel()
	if _, err := NewIDGen(64, nil); err == nil {
		t.Fatal("node 64 accepted, want error")
	}
	g, err := NewIDGen(-1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := g.Node(); n < 0 || n > 63 {
		t.Errorf("random node %d outside 0..63", n)
	}
}

func TestIDGenStrictlyIncreasingWithinMillisecond(t *testing.T) {
	t.Parallel()
	g, err := NewIDGen(1, fixedClock(IDEpoch.Add(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	g.counter = 0 // the seed is random; start low so 60 000 ids fit in one millisecond
	prev := g.New()
	for i := range 60000 {
		id := g.New()
		if id <= prev {
			t.Fatalf("id %d after %d is not larger (iteration %d)", id, prev, i)
		}
		prev = id
	}
}

func TestIDGenWrapWaitsForNextMillisecond(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	now := IDEpoch.Add(time.Minute)
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	g, err := NewIDGen(2, clock)
	if err != nil {
		t.Fatal(err)
	}
	g.counter = idCounterMask - 1 // next id takes 65535, the one after must wrap

	first := g.New()
	done := make(chan ID, 1)
	go func() { done <- g.New() }()

	select {
	case id := <-done:
		t.Fatalf("second id %d issued without the clock moving", id)
	case <-time.After(20 * time.Millisecond):
	}
	mu.Lock()
	now = now.Add(time.Millisecond)
	mu.Unlock()

	second := <-done
	if second <= first {
		t.Fatalf("wrapped id %d not larger than %d", second, first)
	}
	if second.Time().Sub(first.Time()) != time.Millisecond {
		t.Errorf("wrapped id is %v after the first, want 1ms", second.Time().Sub(first.Time()))
	}
	if second&idCounterMask != 0 {
		t.Errorf("counter after wrap = %d, want 0", second&idCounterMask)
	}
}

func TestIDGenClockGoingBackwardsKeepsOrder(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	now := IDEpoch.Add(time.Hour)
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	g, err := NewIDGen(3, clock)
	if err != nil {
		t.Fatal(err)
	}
	a := g.New()
	mu.Lock()
	now = now.Add(-time.Second)
	mu.Unlock()
	b := g.New()
	if b <= a {
		t.Fatalf("id %d issued after clock step back is not larger than %d", b, a)
	}
	if !b.Time().Equal(a.Time()) {
		t.Errorf("after a backwards step the id should stay in the last millisecond; got %v vs %v", b.Time(), a.Time())
	}
}

func TestIDGenConcurrentUnique(t *testing.T) {
	t.Parallel()
	g, err := NewIDGen(0, nil)
	if err != nil {
		t.Fatal(err)
	}
	const workers, per = 8, 20000
	var wg sync.WaitGroup
	results := make([][]ID, workers)
	for w := range workers {
		wg.Go(func() {
			ids := make([]ID, per)
			for i := range ids {
				ids[i] = g.New()
			}
			results[w] = ids
		})
	}
	wg.Wait()
	seen := make(map[ID]struct{}, workers*per)
	for _, ids := range results {
		for _, id := range ids {
			if _, dup := seen[id]; dup {
				t.Fatalf("duplicate id %d", id)
			}
			seen[id] = struct{}{}
		}
	}
}

func TestParseID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		want    ID
		wantErr bool
	}{
		{"7245123456789012345", 7245123456789012345, false},
		{"1", 1, false},
		{"0", 0, true},
		{"-5", 0, true},
		{"abc", 0, true},
		{"", 0, true},
		{"9223372036854775808", 0, true}, // overflows int64
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got, err := ParseID(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseID(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("ParseID(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}
