package slashcmd

import (
	"context"
	"fmt"
	"strings"
)

// wfCommand implements /wf [workflow-id].
//
// /wf (no arg) — lists installed workflows via Workflows.List.
//   Returns a numbered list with id, name, brief description.
//   Footer: "type `/wf <id>` to run".
//
// /wf <workflow-id> — looks up the workflow via Workflows.Get.
//   If it has required inputs lacking defaults, prompts the user
//   inline for each via successive Result messages. Once all inputs
//   are collected, dispatches via Workflows.Run with Inline:true and
//   pipes each ProgressEvent into the chat as an info bubble.
//
// /wf <unknown-id> — error message: "no workflow `<id>` — try `/wf` to list".
type wfCommand struct{}

func (wfCommand) Name() string { return "wf" }
func (wfCommand) Description() string {
	return "Run an installed workflow — /wf lists workflows; /wf <id> runs one"
}
func (wfCommand) Hidden() bool     { return false }
func (wfCommand) ComingSoon() bool { return false }

func (wfCommand) Run(ctx context.Context, env Env, args []string) (Result, error) {
	if env.Workflows == nil {
		return Result{
			Kind: ResultKindError,
			Text: "/wf: workflows not wired",
		}, nil
	}

	// /wf with no arg — list installed workflows.
	if len(args) == 0 {
		return wfList(ctx, env)
	}

	id := strings.TrimSpace(args[0])
	if id == "" {
		return wfList(ctx, env)
	}

	// /wf <id> — look up, prompt for required inputs if needed, then run.
	wf, err := env.Workflows.Get(ctx, id)
	if err != nil {
		// Surface as "not found" when the error text contains the id.
		if strings.Contains(err.Error(), id) || strings.Contains(err.Error(), "not found") {
			return Result{
				Kind: ResultKindError,
				Text: fmt.Sprintf("no workflow `%s` — try `/wf` to list", id),
			}, nil
		}
		return Result{
			Kind: ResultKindError,
			Text: "/wf: " + err.Error(),
		}, err
	}

	// Collect inputs from remaining args (key=value pairs) or defaults.
	inputs := make(map[string]string)
	// Parse any key=value tokens the caller may have passed after the id.
	for _, tok := range args[1:] {
		if k, v, ok := strings.Cut(tok, "="); ok {
			inputs[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}

	// Check for required inputs without values or defaults.
	var missing []string
	for _, inp := range wf.Inputs {
		if !inp.Required {
			continue
		}
		if inp.Default != "" {
			continue
		}
		if _, provided := inputs[inp.Name]; provided {
			continue
		}
		missing = append(missing, inp.Name)
	}
	if len(missing) > 0 {
		// Prompt the user for the first missing input. The chat composer
		// re-invokes /wf with the reply attached; until the multi-turn
		// collect loop is wired end-to-end, return a prompt bubble for
		// the first missing field. This matches the cmd_memory pattern of
		// surfacing one actionable message per missing arg.
		return Result{
			Kind: ResultKindInfo,
			Text: fmt.Sprintf("/wf %s: input required — please provide `%s` (re-run as: /wf %s %s=<value>)",
				id, missing[0], id, missing[0]),
		}, nil
	}

	// All required inputs satisfied — dispatch inline.
	ch, err := env.Workflows.Run(ctx, wf.ID, inputs, WorkflowRunOptions{Inline: true, SessionID: env.SessionID})
	if err != nil {
		return Result{
			Kind: ResultKindError,
			Text: fmt.Sprintf("/wf %s: dispatch failed: %s", id, err.Error()),
		}, err
	}

	return wfDrainProgress(ctx, wf, ch)
}

// wfList renders the installed workflow catalog as a numbered list.
func wfList(ctx context.Context, env Env) (Result, error) {
	summaries, err := env.Workflows.List(ctx)
	if err != nil {
		return Result{
			Kind: ResultKindError,
			Text: "/wf: " + err.Error(),
		}, err
	}
	if len(summaries) == 0 {
		return Result{
			Kind: ResultKindInfo,
			Text: "No workflows installed. Install one from the catalog or save a workflow YAML.",
		}, nil
	}
	var b strings.Builder
	b.WriteString("Installed workflows:\n")
	for i, s := range summaries {
		fmt.Fprintf(&b, "  %d. %s — %s", i+1, s.ID, s.Name)
		if s.Description != "" {
			fmt.Fprintf(&b, ": %s", s.Description)
		}
		b.WriteString("\n")
	}
	b.WriteString("\ntype `/wf <id>` to run")
	return Result{
		Kind: ResultKindInfo,
		Text: strings.TrimRight(b.String(), "\n"),
	}, nil
}

// wfDrainProgress reads all events from ch and folds them into a
// single Result so the chat surface shows a concise run summary.
// Each step transition is included in the output text so the user
// can see what ran.
func wfDrainProgress(_ context.Context, wf WorkflowDetail, ch <-chan WorkflowProgressEvent) (Result, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "Running workflow `%s`…\n", wf.Name)
	finalStatus := "completed"
	var runErr string
	for ev := range ch {
		switch ev.Status {
		case "running":
			fmt.Fprintf(&b, "  [%s] running…\n", ev.Step)
		case "completed":
			if ev.Output != "" {
				fmt.Fprintf(&b, "  [%s] %s\n", ev.Step, ev.Output)
			} else {
				fmt.Fprintf(&b, "  [%s] completed\n", ev.Step)
			}
		case "failed":
			finalStatus = "failed"
			runErr = ev.Err
			fmt.Fprintf(&b, "  [%s] failed: %s\n", ev.Step, ev.Err)
		case "skipped":
			fmt.Fprintf(&b, "  [%s] skipped\n", ev.Step)
		default:
			fmt.Fprintf(&b, "  [%s] %s\n", ev.Step, ev.Status)
		}
	}
	if finalStatus == "failed" {
		fmt.Fprintf(&b, "\nWorkflow failed: %s", runErr)
		return Result{
			Kind: ResultKindError,
			Text: strings.TrimRight(b.String(), "\n"),
		}, nil
	}
	fmt.Fprintf(&b, "\nWorkflow completed.")
	return Result{
		Kind: ResultKindInfo,
		Text: strings.TrimRight(b.String(), "\n"),
	}, nil
}
