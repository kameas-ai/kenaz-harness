package mcp

import (
	"context"
	"sync"
	"testing"
	"time"
)

type stubRegistry struct {
	servers []Server
	err     error
}

func (s stubRegistry) List(_ context.Context) ([]Server, error) { return s.servers, s.err }

type recordingSubscriber struct {
	mu      sync.Mutex
	subs    map[string]<-chan any
	stopped []string
}

func newRecordingSubscriber() *recordingSubscriber {
	return &recordingSubscriber{subs: map[string]<-chan any{}}
}

func (r *recordingSubscriber) Subscribe(_ context.Context, view, _ string, source <-chan any) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := view + "-" + time.Now().Format("150405.000000")
	r.subs[id] = source
	return id, nil
}

func (r *recordingSubscriber) Unsubscribe(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.subs, id)
	r.stopped = append(r.stopped, id)
	return nil
}

func TestListServers_NoRegistry_ReturnsEmpty(t *testing.T) {
	api := NewAPI()
	got, err := api.ListServers(context.Background())
	if err != nil {
		t.Fatalf("ListServers: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("nil registry path should yield empty slice, got %d", len(got))
	}
}

func TestListServers_RegistrySorted(t *testing.T) {
	reg := stubRegistry{servers: []Server{
		{ID: "z", Name: "z-server", State: "ready", Version: "1.0.0", Transport: "stdio"},
		{ID: "a", Name: "a-server", State: "connecting", Version: "0.9.0", Transport: "ws"},
	}}
	api := NewAPI(WithRegistry(reg))
	got, err := api.ListServers(context.Background())
	if err != nil {
		t.Fatalf("ListServers: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
	if got[0].Name != "a-server" || got[1].Name != "z-server" {
		t.Errorf("expected sort by name, got [%s, %s]", got[0].Name, got[1].Name)
	}
}

func TestStartStream_NoBroker_NoOp(t *testing.T) {
	api := NewAPI()
	id, err := api.StartStream(context.Background(), "any")
	if err != nil {
		t.Fatalf("StartStream without broker should not error, got %v", err)
	}
	if id != "" {
		t.Errorf("StartStream without broker should return empty id, got %q", id)
	}
	if err := api.StopStream(context.Background(), "any"); err != nil {
		t.Errorf("StopStream without broker should not error, got %v", err)
	}
}

func TestPublish_FansToSubscribers(t *testing.T) {
	rec := newRecordingSubscriber()
	api := NewAPI(WithSubscriber(rec))

	id, err := api.StartStream(context.Background(), "srv-1")
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	rec.mu.Lock()
	src := rec.subs[id]
	rec.mu.Unlock()
	if src == nil {
		t.Fatalf("subscriber missing source channel")
	}

	api.Publish(map[string]any{"name": "tools.invoked"})

	select {
	case ev := <-src:
		m, ok := ev.(map[string]any)
		if !ok || m["name"] != "tools.invoked" {
			t.Errorf("unexpected event payload %v", ev)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("did not receive published event")
	}

	if err := api.StopStream(context.Background(), id); err != nil {
		t.Fatalf("StopStream: %v", err)
	}
	if len(rec.stopped) != 1 {
		t.Errorf("expected 1 unsubscribe, got %d", len(rec.stopped))
	}
}
