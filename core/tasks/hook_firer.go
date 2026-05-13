package tasks

// HookRunnerIface is the narrow interface the tasks package needs from
// core/hooks.Runner to fire background_task_complete events without a
// circular import.
//
// The rpc wiring layer injects a real implementation that calls
// hooks.Runner.Fire(ctx, hooks.EventBackgroundTaskComplete, payload).
type HookRunnerIface interface {
	FireBackgroundTaskComplete(payload BackgroundTaskCompletePayload)
}
