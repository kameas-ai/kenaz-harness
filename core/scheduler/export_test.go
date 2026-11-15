package scheduler

// Test-only hooks for the external scheduler_test package: drive the
// cron and one-shot callbacks directly, the way a tick or timer that
// raced Stop would.

// FireForTest invokes the cron callback for id.
func (e *ChatCronEngine) FireForTest(id string) { e.fire(id) }

// FireOnceForTest invokes the one-shot timer callback for id.
func (e *ChatCronEngine) FireOnceForTest(id string) { e.fireOnce(id) }
