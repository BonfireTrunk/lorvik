package domain

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"strconv"
	"sync"
	"time"
)

// ID is a 64-bit, time-ordered identifier used as the primary key of every
// table. It travels as a decimal string on the wire because JavaScript cannot
// hold it as a number.
//
// Layout, most significant bit first:
//
//	 1 bit   always zero, so the value is positive
//	41 bits  milliseconds since IDEpoch (lasts until 2095-09-07)
//	 6 bits  node id, 0..63
//	16 bits  per-node counter, seeded at random, wraps
type ID int64

// IDEpoch is the zero point of the timestamp bits.
var IDEpoch = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

const (
	idNodeBits    = 6
	idCounterBits = 16
	idNodeMax     = 1<<idNodeBits - 1
	idCounterMask = 1<<idCounterBits - 1
	idTimeShift   = idNodeBits + idCounterBits
	idTimeMax     = 1<<41 - 1
)

// String formats the id as a decimal number.
func (id ID) String() string { return strconv.FormatInt(int64(id), 10) }

// Time returns the millisecond the id was generated in.
func (id ID) Time() time.Time {
	ms := int64(id) >> idTimeShift
	return IDEpoch.Add(time.Duration(ms) * time.Millisecond)
}

// Node returns the node id embedded in the id.
func (id ID) Node() int { return int(int64(id)>>idCounterBits) & idNodeMax }

// IsZero reports whether the id is unset.
func (id ID) IsZero() bool { return id == 0 }

// ParseID reads a decimal id. It rejects zero, negatives and non-numeric text.
func ParseID(s string) (ID, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid id %q", s)
	}
	return ID(n), nil
}

// IDGen produces ids for one process. Node ids distinguish processes that run
// at the same time; the counter distinguishes ids within one millisecond. It
// is safe for concurrent use.
type IDGen struct {
	now  func() time.Time
	node int64

	mu      sync.Mutex
	lastMS  int64 // millisecond of the previous id
	counter int64 // value used for the previous id
}

// NewIDGen returns a generator for the given node id. A node id below zero
// picks one at random. The counter starts at a random value so two nodes
// that happen to share a node id still differ. The clock is injectable for
// tests; nil means time.Now.
func NewIDGen(node int, now func() time.Time) (*IDGen, error) {
	if node > idNodeMax {
		return nil, fmt.Errorf("node id %d exceeds %d", node, idNodeMax)
	}
	if node < 0 {
		node = int(randomBelow(idNodeMax + 1))
	}
	if now == nil {
		now = time.Now
	}
	seed := int64(randomBelow(idCounterMask + 1))
	return &IDGen{now: now, node: int64(node), counter: seed}, nil
}

// Node returns the node id this generator stamps into every id.
func (g *IDGen) Node() int { return int(g.node) }

// New returns the next id. Ids from one generator are strictly increasing:
// the counter only ever goes up within a millisecond, and when it would wrap
// past 65 535 the generator waits for the next millisecond and continues
// from zero there.
func (g *IDGen) New() ID {
	g.mu.Lock()
	defer g.mu.Unlock()

	ms := max(g.millis(),
		// The clock went backwards. Keep issuing in the last millisecond we
		// used so ids stay increasing.
		g.lastMS)
	next := g.counter + 1
	if next > idCounterMask {
		for ms <= g.lastMS {
			time.Sleep(50 * time.Microsecond)
			ms = g.millis()
		}
		next = 0
	}
	g.lastMS = ms
	g.counter = next
	return ID(ms<<idTimeShift | g.node<<idCounterBits | next)
}

func (g *IDGen) millis() int64 {
	return min(max(g.now().Sub(IDEpoch).Milliseconds(), 0), idTimeMax)
}

// randomBelow returns a uniform value in [0, n) from crypto/rand.
func randomBelow(n uint64) uint64 {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand.Read never fails on supported platforms (Go 1.24+).
		panic("crypto/rand: " + err.Error())
	}
	return binary.LittleEndian.Uint64(b[:]) % n
}
