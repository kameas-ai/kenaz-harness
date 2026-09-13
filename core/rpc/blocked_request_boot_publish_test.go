package rpc

// Unit-level proof (isolated from the full core.New/rpc.New boot path)
// that publishPendingBlockedRequestsAtBoot stays silent when nothing is
// pending, and DOES publish when something is — the exact distinction
// AC-013 (FR-008: "no new broker emissions" for a zero-schedule build)
// requires, and the reason publishPendingBlockedRequestsAtBoot exists as
// a separate function from publishPendingBlockedRequests (the live-
// refresh path, which always publishes, correctly, since it is only ever
// called right after a row was written).

import (
	"context"
	"testing"

	blockedrequestsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/blockedrequests"
)

func TestPublishPendingBlockedRequestsAtBoot_SilentWhenEmpty(t *testing.T) {
	rec := &recordingEmitter{}
	broker := NewStreamBroker(rec)
	a := &API{
		broker:             broker,
		blockedRequestsAPI: blockedrequestsview.New(blockedrequestsview.Config{}), // nil store -> ListPending returns nil, nil
	}
	a.publishPendingBlockedRequestsAtBoot(context.Background())
	if topics := rec.topics(); len(topics) != 0 {
		t.Fatalf("emitted topics = %v, want none (AC-013: a zero-pending build must start no new broker emissions)", topics)
	}
}

// fakeBlockedRequestsAPI lets this test report a non-empty pending list
// without standing up a real store.
type fakeBlockedRequestsAPI struct {
	pending []blockedrequestsview.PendingRequest
}

func (f *fakeBlockedRequestsAPI) ListPending(context.Context) ([]blockedrequestsview.PendingRequest, error) {
	return f.pending, nil
}
func (f *fakeBlockedRequestsAPI) Grant(context.Context, string) error   { return nil }
func (f *fakeBlockedRequestsAPI) Dismiss(context.Context, string) error { return nil }

func TestPublishPendingBlockedRequestsAtBoot_PublishesWhenNonEmpty(t *testing.T) {
	rec := &recordingEmitter{}
	broker := NewStreamBroker(rec)
	a := &API{
		broker: broker,
		blockedRequestsAPI: &fakeBlockedRequestsAPI{
			pending: []blockedrequestsview.PendingRequest{{ID: "req-1", Status: "pending"}},
		},
	}
	a.publishPendingBlockedRequestsAtBoot(context.Background())
	topics := rec.topics()
	if len(topics) != 1 || topics[0] != TopicBlockedPermissionRequestPending {
		t.Fatalf("emitted topics = %v, want exactly [%s]", topics, TopicBlockedPermissionRequestPending)
	}
}
