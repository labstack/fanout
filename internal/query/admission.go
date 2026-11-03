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
}

func newReadAdmission(slots, reserved, panelQueue int) *readAdmission {
	return &readAdmission{slots: slots, reserved: reserved, bounds: [2]int{panelQueue, 2 * slots}}
}
func (a *readAdmission) available(class readClass) bool {
	return a.active[0]+a.active[1] < a.slots && (class == interactiveClass || a.active[panelClass] < a.slots-a.reserved)
}
func (a *readAdmission) acquire(ctx context.Context, class readClass) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.Lock()
	if len(a.queues[class]) == 0 && a.available(class) && (class == interactiveClass || len(a.queues[interactiveClass]) == 0) {
		a.active[class]++
		a.mu.Unlock()
		return nil
	}
	if len(a.queues[class]) >= a.bounds[class] {
		a.mu.Unlock()
		return errQueueFull
	}
	w := &readWaiter{ready: make(chan struct{})}
	a.queues[class] = append(a.queues[class], w)
	a.mu.Unlock()
	select {
	case <-w.ready:
		return nil
	case <-ctx.Done():
		a.mu.Lock()
		if w.granted {
			a.active[class]--
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
		for len(a.queues[class]) > 0 && a.available(class) {
			w := a.queues[class][0]
			a.queues[class] = a.queues[class][1:]
			a.active[class]++
			w.granted = true
			close(w.ready)
		}
	}
}
func (a *readAdmission) release(class readClass) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.active[class]--
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
	return a.available(panelClass) && len(a.queues[0])+len(a.queues[1]) == 0
}
