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
}

// readAdmission reserves capacity for interactive reads and gives their queued
// requests first use of any released slot. Capacity is lent to either class
// except for the interactive reservation.
type readAdmission struct {
	mu              sync.Mutex
	slots, reserved int
	active          [2]int
	queues          [2][]*readWaiter
	bounds          [2]int
	batches         int
}

func newReadAdmission(slots, reserved, panelQueue int) *readAdmission {
	return &readAdmission{slots: slots, reserved: reserved, bounds: [2]int{panelQueue, 2 * slots}}
}
func (a *readAdmission) availableN(class readClass, n int) bool {
	return a.active[0]+a.active[1]+n <= a.slots && (class == interactiveClass || a.active[panelClass]+n <= a.slots-a.reserved)
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
	a.mu.Lock()
	if len(a.queues[class]) == 0 && a.availableN(class, n) && (class == interactiveClass || len(a.queues[interactiveClass]) == 0) {
		a.active[class] += n
		a.mu.Unlock()
		return nil
	}
	if bounded && len(a.queues[class]) >= a.bounds[class] {
		a.mu.Unlock()
		return errQueueFull
	}
	w := &readWaiter{ready: make(chan struct{}), weight: n}
	a.queues[class] = append(a.queues[class], w)
	a.mu.Unlock()
	select {
	case <-w.ready:
		return nil
	case <-ctx.Done():
		a.mu.Lock()
		if w.granted {
			a.active[class] -= w.weight
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
func (a *readAdmission) waiting() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.queues[0]) + len(a.queues[1])
}

// tryShared is an observational probe, not a reservation.
func (a *readAdmission) tryShared() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.availableN(panelClass, a.batchSlots()) && len(a.queues[0])+len(a.queues[1]) == 0
}

// A free fragment smaller than a refresh budget cannot start another batch.
// Background pressure must use the same unit as foreground admission.
func (a *readAdmission) batchSlots() int { return min(2, max(1, (a.slots-a.reserved)/2)) }

// Bound the lifetime population too: accepted batches that yield their slots
// must not create an unbounded queue when they resume.
func (a *readAdmission) reserveBatch() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.batches >= a.bounds[panelClass]+max(1, (a.slots-a.reserved)/2) {
		return errQueueFull
	}
	a.batches++
	return nil
}
func (a *readAdmission) releaseBatch() { a.mu.Lock(); a.batches--; a.mu.Unlock() }

// A refresh takes one FIFO ticket for a small, atomic slot budget. Its siblings
// cannot fill the global queue or refuse one another halfway through a refresh.
// The lease is lazy: cache hits and coalesced flights never reserve connections.
// Idle leases yield their slots (including while a stream client is stalled);
// an accepted batch can requeue without being refused by its siblings.
// A detached flight retains the lease until it exits; late cache revalidations
// after close use ordinary single-slot admission instead.
type panelBatchAdmission struct {
	admission                               *readAdmission
	slots                                   int
	limit                                   chan struct{}
	mu                                      sync.Mutex
	ready                                   chan struct{}
	started, closed, held, accepted, ticket bool
	refs                                    int
	err                                     error
}

func newPanelBatchAdmission(a *readAdmission, parallel int) *panelBatchAdmission {
	slots := min(parallel, a.batchSlots())
	return &panelBatchAdmission{admission: a, slots: slots, limit: make(chan struct{}, slots), ready: make(chan struct{})}
}
func (b *panelBatchAdmission) acquire(ctx context.Context) (func(), error) {
	select {
	case b.limit <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		if err := b.admission.acquire(ctx, panelClass); err != nil {
			<-b.limit
			return nil, err
		}
		markStarted(ctx)
		return func() { b.admission.release(panelClass); <-b.limit }, nil
	}
	b.refs++
	first := !b.started
	accepted := b.accepted
	b.started = true
	b.mu.Unlock()
	if first {
		var err error
		if !accepted {
			err = b.admission.reserveBatch()
			if err == nil {
				b.mu.Lock()
				b.ticket = true
				b.mu.Unlock()
			}
		}
		if err == nil {
			err = b.admission.acquireWeighted(ctx, panelClass, b.slots, !accepted)
		}
		b.mu.Lock()
		b.err = err
		b.held = err == nil
		b.accepted = b.accepted || err == nil
		close(b.ready)
		b.mu.Unlock()
	}
	select {
	case <-b.ready:
	case <-ctx.Done():
		b.release()
		return nil, ctx.Err()
	}
	b.mu.Lock()
	err := b.err
	b.mu.Unlock()
	if err != nil {
		b.release()
		return nil, err
	}
	markStarted(ctx)
	return b.release, nil
}
func (b *panelBatchAdmission) release() {
	b.mu.Lock()
	b.refs--
	b.releaseIdle()
	b.mu.Unlock()
	<-b.limit
}
func (b *panelBatchAdmission) releaseIdle() {
	if b.refs == 0 && b.held {
		b.held = false
		b.admission.releaseN(panelClass, b.slots)
		b.started = false
		b.ready = make(chan struct{})
	}
	if b.closed && b.refs == 0 && b.ticket {
		b.ticket = false
		b.admission.releaseBatch()
	}
}
func (b *panelBatchAdmission) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	b.releaseIdle()
}
