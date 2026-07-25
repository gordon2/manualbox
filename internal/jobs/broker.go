package jobs

import (
	"sync"
	"time"
)

// eventBuffer is how many events a slow subscriber may fall behind before its
// events start being dropped.
const eventBuffer = 32

// Event is a job state or progress change, delivered to subscribers.
type Event struct {
	JobID    string    `json:"jobId"`
	Kind     string    `json:"kind"`
	State    State     `json:"state"`
	Progress float64   `json:"progress"`
	Note     string    `json:"note,omitempty"`
	Error    string    `json:"error,omitempty"`
	At       time.Time `json:"at"`
}

func eventFor(j *Job) Event {
	return Event{
		JobID:    j.ID,
		Kind:     j.Kind,
		State:    j.State,
		Progress: j.Progress,
		Note:     j.ProgressNote,
		Error:    j.LastError,
		At:       j.UpdatedAt,
	}
}

// Broker fans job events out to subscribers, which is how the UI shows live
// progress without polling.
//
// Publishing never blocks. A subscriber that stops reading — a browser tab that
// froze, an SSE connection that died without closing — loses events rather than
// stalling the worker that produced them. Progress reporting must never be able
// to wedge the work it is reporting on.
type Broker struct {
	mu     sync.RWMutex
	next   int
	subs   map[int]chan Event
	closed bool
}

// NewBroker returns an empty broker.
func NewBroker() *Broker {
	return &Broker{subs: make(map[int]chan Event)}
}

// Subscribe returns a channel of events and a function to stop listening. The
// cancel function must be called or the subscription leaks.
func (b *Broker) Subscribe() (events <-chan Event, cancel func()) {
	b.mu.Lock()
	defer b.mu.Unlock()

	ch := make(chan Event, eventBuffer)
	if b.closed {
		close(ch)
		return ch, func() {}
	}

	fid := b.next
	b.next++
	b.subs[fid] = ch

	return ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if sub, ok := b.subs[fid]; ok {
			delete(b.subs, fid)
			close(sub)
		}
	}
}

// Publish delivers an event to all current subscribers, dropping it for any
// subscriber whose buffer is full.
func (b *Broker) Publish(ev Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, ch := range b.subs {
		select {
		case ch <- ev:
		default:
			// Subscriber is behind; drop rather than block. The UI recovers on the
			// next event, and a full refetch gives it the authoritative state.
		}
	}
}

// Subscribers reports the current subscriber count, for tests and diagnostics.
func (b *Broker) Subscribers() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}

// Close shuts the broker down and closes every subscriber channel.
func (b *Broker) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return
	}
	b.closed = true
	for fid, ch := range b.subs {
		delete(b.subs, fid)
		close(ch)
	}
}
