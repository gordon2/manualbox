package jobs

import (
	"sync"
	"testing"
	"time"
)

func TestBrokerDeliversToAllSubscribers(t *testing.T) {
	b := NewBroker()
	defer b.Close()

	a, cancelA := b.Subscribe()
	defer cancelA()
	c, cancelC := b.Subscribe()
	defer cancelC()

	if got := b.Subscribers(); got != 2 {
		t.Fatalf("Subscribers = %d, want 2", got)
	}

	want := Event{JobID: "job_1", Kind: "convert", State: StateRunning, Progress: 0.5}
	b.Publish(want)

	for i, ch := range []<-chan Event{a, c} {
		select {
		case got := <-ch:
			if got.JobID != want.JobID || got.Progress != want.Progress {
				t.Errorf("subscriber %d got %+v, want %+v", i, got, want)
			}
		case <-time.After(time.Second):
			t.Errorf("subscriber %d received nothing", i)
		}
	}
}

func TestBrokerUnsubscribeStopsDelivery(t *testing.T) {
	b := NewBroker()
	defer b.Close()

	ch, cancel := b.Subscribe()
	cancel()

	if got := b.Subscribers(); got != 0 {
		t.Errorf("Subscribers = %d after cancel, want 0", got)
	}
	// The channel is closed, so a receive returns immediately with the zero value.
	select {
	case _, open := <-ch:
		if open {
			t.Error("channel should be closed after cancel")
		}
	case <-time.After(time.Second):
		t.Error("cancelled subscription channel was not closed")
	}

	// Publishing afterwards must not panic on a closed channel.
	b.Publish(Event{JobID: "job_1"})
}

func TestBrokerCancelIsIdempotent(t *testing.T) {
	b := NewBroker()
	defer b.Close()

	_, cancel := b.Subscribe()
	cancel()
	// A double cancel must not panic by closing an already-closed channel.
	cancel()
}

// TestBrokerPublishNeverBlocks is the important property: a stalled UI must not
// be able to wedge the worker that is reporting progress to it.
func TestBrokerPublishNeverBlocks(t *testing.T) {
	b := NewBroker()
	defer b.Close()

	// Subscribe and then never read.
	_, cancel := b.Subscribe()
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range eventBuffer * 10 {
			b.Publish(Event{JobID: "job_1", Progress: float64(i)})
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Publish blocked on a subscriber that stopped reading")
	}
}

func TestBrokerSlowSubscriberDropsRatherThanBlocking(t *testing.T) {
	b := NewBroker()
	defer b.Close()

	ch, cancel := b.Subscribe()
	defer cancel()

	// Overfill the buffer.
	const sent = eventBuffer * 3
	for i := range sent {
		b.Publish(Event{JobID: "job_1", Progress: float64(i)})
	}

	// The subscriber keeps a bounded backlog rather than an unbounded one.
	received := 0
	for {
		select {
		case <-ch:
			received++
			continue
		default:
		}
		break
	}

	if received == 0 {
		t.Error("subscriber received nothing at all")
	}
	if received > eventBuffer {
		t.Errorf("received %d events, want at most the buffer size %d", received, eventBuffer)
	}
	if received == sent {
		t.Error("expected some events to be dropped for a non-reading subscriber")
	}
}

func TestBrokerCloseClosesSubscribers(t *testing.T) {
	b := NewBroker()
	ch, cancel := b.Subscribe()
	defer cancel()

	b.Close()

	select {
	case _, open := <-ch:
		if open {
			t.Error("channel should be closed after Close")
		}
	case <-time.After(time.Second):
		t.Error("Close did not close the subscriber channel")
	}

	// Close is idempotent, and subscribing afterwards yields a closed channel
	// rather than a leak.
	b.Close()
	after, cancelAfter := b.Subscribe()
	defer cancelAfter()
	if _, open := <-after; open {
		t.Error("subscribing to a closed broker should return a closed channel")
	}
}

func TestBrokerConcurrentUse(t *testing.T) {
	b := NewBroker()
	defer b.Close()

	var wg sync.WaitGroup

	// Churn subscriptions while publishing, to shake out lock ordering bugs.
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				ch, cancel := b.Subscribe()
				select {
				case <-ch:
				default:
				}
				cancel()
			}
		}()
	}
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 200 {
				b.Publish(Event{JobID: "job_1", Progress: float64(i) / 200})
			}
		}()
	}

	wg.Wait()
	if got := b.Subscribers(); got != 0 {
		t.Errorf("Subscribers = %d after all cancels, want 0 — subscriptions leaked", got)
	}
}
