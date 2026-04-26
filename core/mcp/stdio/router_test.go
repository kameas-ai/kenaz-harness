package stdio

import (
	"sync"
	"testing"
)

func TestRouter_RegisterDeliver(t *testing.T) {
	t.Parallel()
	r := NewResponseRouter(nil)
	ch := r.Register(1)
	if ok := r.Deliver(1, RawMessage{}); !ok {
		t.Fatalf("Deliver returned false")
	}
	select {
	case <-ch:
	default:
		t.Fatalf("channel not delivered")
	}
}

func TestRouter_DeliverAfterCancelDropped(t *testing.T) {
	t.Parallel()
	r := NewResponseRouter(nil)
	ch := r.Register(1)
	r.Cancel(1)
	// Channel was closed by Cancel — receive returns immediately.
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatalf("channel had a value after Cancel")
		}
	default:
		t.Fatalf("channel still open after Cancel")
	}
	if ok := r.Deliver(1, RawMessage{}); ok {
		t.Fatalf("Deliver after Cancel returned true; want dropped")
	}
}

func TestRouter_DoubleDeliverSecondDropped(t *testing.T) {
	t.Parallel()
	r := NewResponseRouter(nil)
	ch := r.Register(1)
	if ok := r.Deliver(1, RawMessage{Method: "first"}); !ok {
		t.Fatalf("first Deliver false")
	}
	// Second deliver finds the channel buffer already full → dropped.
	if ok := r.Deliver(1, RawMessage{Method: "second"}); ok {
		t.Fatalf("second Deliver true; want dropped")
	}
	got := <-ch
	if got.Method != "first" {
		t.Fatalf("got %q want first", got.Method)
	}
}

func TestRouter_ConcurrentRegisterDeliverCancel(t *testing.T) {
	t.Parallel()
	r := NewResponseRouter(nil)
	const n = 200
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(id int64) {
			defer wg.Done()
			ch := r.Register(id)
			if id%2 == 0 {
				r.Deliver(id, RawMessage{})
				<-ch
				r.Cancel(id)
			} else {
				r.Cancel(id)
				// Late delivery after cancel must not block.
				r.Deliver(id, RawMessage{})
			}
		}(int64(i))
	}
	wg.Wait()
	if got := r.Pending(); got != 0 {
		t.Fatalf("Pending = %d after teardown; want 0", got)
	}
}

func TestRouter_CancelAllUnblocksWaiters(t *testing.T) {
	t.Parallel()
	r := NewResponseRouter(nil)
	chs := []<-chan RawMessage{r.Register(1), r.Register(2), r.Register(3)}
	r.CancelAll()
	for i, ch := range chs {
		select {
		case _, ok := <-ch:
			if ok {
				t.Fatalf("ch %d had a value after CancelAll", i)
			}
		default:
			t.Fatalf("ch %d still open after CancelAll", i)
		}
	}
}
