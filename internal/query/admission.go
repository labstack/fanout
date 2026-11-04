package query

import (
	"context"
	"sync"
)

type readClass int

const (
	panelClass readClass = iota
	interactiveClass
)

type readWaiter struct {
	ready   chan struct{}
	granted bool
	weight  int
	owned   bool
}

// readAdmission reserves capacity for interactive reads and gives their queued
// requests first use of shared slots. Pending panels retain progress capacity;
// idle capacity is lent to either class.
type readAdmission struct {
	mu              sync.Mutex
	slots, reserved int
	active          [2]int
	queues          [2][]*readWaiter
	bounds          [2]int
	batches         int
	batchActive     int
}

func newReadAdmission(slots, reserved, panelQueue int) *readAdmission {
	return &readAdmission{slots: slots, reserved: reserved, bounds: [2]int{panelQueue, 2 * slots}}
}
func (a *readAdmission) availableN(class readClass, n int) bool {
	if a.active[0]+a.active[1]+n > a.slots {
		return false
	}
	if class == panelClass {
		return a.active[panelClass]+n <= a.slots-a.reserved
	}
	// Interactive reads are scheduled first and can borrow every idle slot.
	// Once panels need capacity, retain one slot for their progress.
	// Never reserve an unused sibling slot or starve a one-slot pool.
	demand := a.active[panelClass] + len(a.queues[panelClass])
	panels := min(1, demand, a.slots-a.reserved, a.slots-1)
	return a.active[interactiveClass]+n <= a.slots-panels
}
func (a *readAdmission) acquire(ctx context.Context, class readClass) error {
	return a.acquireN(ctx, class, 1)
}
func (a *readAdmission) acquireN(ctx context.Context, class readClass, n int) error {
	return a.acquireWeighted(ctx, class, n, true)
}
func (a *readAdmission) acquireWeighted(ctx context.Context, class readClass, n int, bounded bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	owned := class == panelClass && !bounded
	a.mu.Lock()
	if len(a.queues[class]) == 0 && a.availableN(class, n) && (class == interactiveClass || len(a.queues[interactiveClass]) == 0) {
		a.active[class] += n
		if owned {
			a.batchActive += n
		}
		a.mu.Unlock()
		return nil
	}
	if bounded && len(a.queues[class]) >= a.bounds[class] {
		a.mu.Unlock()
		return errQueueFull
	}
	w := &readWaiter{ready: make(chan struct{}), weight: n, owned: owned}
	a.queues[class] = append(a.queues[class], w)
	a.mu.Unlock()
	select {
	case <-w.ready:
		return nil
	case <-ctx.Done():
		a.mu.Lock()
		if w.granted {
			a.active[class] -= w.weight
			if w.owned {
				a.batchActive -= w.weight
			}
		} else {
			for i, other := range a.queues[class] {
				if other == w {
					a.queues[class] = append(a.queues[class][:i], a.queues[class][i+1:]...)
					break
				}
			}
		}
		a.wake()
		a.mu.Unlock()
		return ctx.Err()
	}
}
func (a *readAdmission) wake() {
	for _, class := range []readClass{interactiveClass, panelClass} {
		for len(a.queues[class]) > 0 && a.availableN(class, a.queues[class][0].weight) {
			w := a.queues[class][0]
			a.queues[class] = a.queues[class][1:]
			a.active[class] += w.weight
			if w.owned {
				a.batchActive += w.weight
			}
			w.granted = true
			close(w.ready)
		}
	}
}
func (a *readAdmission) release(class readClass) { a.releaseN(class, 1) }
func (a *readAdmission) releaseN(class readClass, n int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.active[class] -= n
	a.wake()
}
func (a *readAdmission) releaseOwnedPanel() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.active[panelClass]--
	a.batchActive--
	a.wake()
}

func (a *readAdmission) waiting() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.queues[0]) + len(a.queues[1])
}

// tryShared is an observational probe, not a reservation.
func (a *readAdmission) tryShared() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.availableN(panelClass, 1) && len(a.queues[0])+len(a.queues[1]) == 0
}

// A refresh executes at most two siblings concurrently, each using one global
// slot. An odd remaining slot can run a panel from another refresh.
func (a *readAdmission) batchSlots() int { return min(2, max(1, a.slots-a.reserved)) }

// Bound the lifetime population too: accepted batches that yield their slots
// must not create an unbounded queue when they resume.
func (a *readAdmission) reserveBatch() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	shared := a.slots - a.reserved
	external := a.active[panelClass] - a.batchActive
	running := max(0, (shared-external)/a.batchSlots())
	if a.batches >= a.bounds[panelClass]+running {
		return errQueueFull
	}
	a.batches++
	return nil
}
func (a *readAdmission) releaseBatch() { a.mu.Lock(); a.batches--; a.mu.Unlock() }

// A refresh reserves one lifetime ticket before emitting any result. Siblings
// use individual global slots under a local parallelism cap, so cache hits and
// coalesced work cannot strand an unused half of a two-slot reservation.
// Flights retain the ticket after the response closes; idle batches hold no
// connections. Accepted siblings never re-enter bounded admission.
type panelBatchAdmission struct {
	admission      *readAdmission
	limit          chan struct{}
	mu             sync.Mutex
	closed, ticket bool
	refs, flights  int
}

func newPanelBatchAdmission(a *readAdmission, parallel int) *panelBatchAdmission {
	return &panelBatchAdmission{admission: a, limit: make(chan struct{}, min(parallel, a.batchSlots()))}
}

type panelBatchKey struct{}

func (b *panelBatchAdmission) retainFlight() func() {
	b.mu.Lock()
	b.flights++
	b.mu.Unlock()
	return func() { b.mu.Lock(); b.flights--; b.releaseTicket(); b.mu.Unlock() }
}

func (b *panelBatchAdmission) admit(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.admission.reserveBatch(); err != nil {
		return err
	}
	b.mu.Lock()
	b.ticket = true
	b.mu.Unlock()
	return nil
}

func (b *panelBatchAdmission) acquire(ctx context.Context) (func(), error) {
	select {
	case b.limit <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	b.mu.Lock()
	if b.closed && ctx.Value(panelBatchKey{}) != b {
		b.mu.Unlock()
		if err := b.admission.acquire(ctx, panelClass); err != nil {
			<-b.limit
			return nil, err
		}
		markStarted(ctx)
		return func() { b.admission.release(panelClass); <-b.limit }, nil
	}
	if !b.ticket {
		if err := b.admission.reserveBatch(); err != nil {
			b.mu.Unlock()
			<-b.limit
			return nil, err
		}
		b.ticket = true
	}
	b.refs++
	b.mu.Unlock()
	if err := b.admission.acquireWeighted(ctx, panelClass, 1, false); err != nil {
		b.mu.Lock()
		b.refs--
		b.releaseTicket()
		b.mu.Unlock()
		<-b.limit
		return nil, err
	}
	markStarted(ctx)
	return b.release, nil
}

func (b *panelBatchAdmission) release() {
	b.admission.releaseOwnedPanel()
	b.mu.Lock()
	b.refs--
	b.releaseTicket()
	b.mu.Unlock()
	<-b.limit
}

func (b *panelBatchAdmission) releaseTicket() {
	if b.closed && b.refs == 0 && b.flights == 0 && b.ticket {
		b.ticket = false
		b.admission.releaseBatch()
	}
}

func (b *panelBatchAdmission) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	b.releaseTicket()
}
