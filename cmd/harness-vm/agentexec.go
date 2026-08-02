// agentexec.go — the agent step's model execution seam (Spec 058).
//
// The graph's run node (graphrun.go) used to be an echo stub. This file
// replaces it with a real LLM-backed executor built on the harness's own
// core/llm provider registry, resolved entirely from the in-VM environment.
// Provider and model identity NEVER cross the :7881 wire contract — selection
// happens here, inside the harness process (Spec 058 FR-002 / 056 G4).
//
// Env contract (read once at process start):
//
//	KENAZ_AGENT_EXEC      "stub"  → offline echo graph (CI / smoke only).
//	                      ""/"real" → real model execution (the default).
//	                      anything else → every task fails with a named
//	                      config error (never a silent echo).
//	KENAZ_AGENT_PROVIDER  optional adapter kind override (anthropic,
//	                      openrouter, openai, gemini, ... — any kind the
//	                      registry serves). Unset → auto-detect below.
//	KENAZ_AGENT_CRED_ENV  optional name of the env var holding the API key
//	                      for KENAZ_AGENT_PROVIDER. Defaults to the
//	                      provider's conventional variable when known.
//	KENAZ_AGENT_MODEL     optional model id override. Unset → the
//	                      provider's default from defaultAgentModels.
//
// Auto-detection (no KENAZ_AGENT_PROVIDER): the first non-empty credential
// env var in core/llm/envprovider.Detections order picks the provider. When
// NOTHING resolves in real mode, tasks fail with a named no_model_credential
// error — the honest degraded mode Spec 058 US3 requires. Echo is reachable
// ONLY via the explicit stub flag (FR-004).
//
// The detection table, the per-provider default models, and the resolution
// rules live in core/llm/envprovider so this executor and the SERVED harness
// (--serve, which seeds a host provider profile from the same env) can never
// disagree about which provider a given VM environment implies.
//
// PRIVACY: credential VALUES never leave the resolver (core/llm/credref /
// core/secrets); this file logs only the provider kind, model id, and the
// credential env var NAME.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/credref"
	"github.com/kameas-ai/kenaz-harness/core/llm/envprovider"
	"github.com/kameas-ai/kenaz-harness/core/llm/registry"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

const (
	agentExecEnv     = "KENAZ_AGENT_EXEC"
	agentProviderEnv = "KENAZ_AGENT_PROVIDER"
	agentModelEnv    = "KENAZ_AGENT_MODEL"
	agentCredEnvEnv  = "KENAZ_AGENT_CRED_ENV"

	// agentProfileID is the single in-VM provider profile the executor loads
	// into its private registry. Structural — never user content.
	agentProfileID = "agent-exec"
)

// agentNoCredentialHint is this surface's caller-specific advice appended to
// envprovider.ErrNoCredential (Spec 058 FR-003 / SC-002). The sentinel's
// leading "no_model_credential:" token stays first so the wire's
// message_truncated (64 runes) always carries the stable name.
const agentNoCredentialHint = "grant one (e.g. ANTHROPIC_API_KEY) or set KENAZ_AGENT_EXEC=stub"

// agentEnvOptions binds this surface's override env vars to the shared
// resolver in core/llm/envprovider.
var agentEnvOptions = envprovider.Options{
	ProviderVar:      agentProviderEnv,
	CredVar:          agentCredEnvEnv,
	ModelVar:         agentModelEnv,
	NoCredentialHint: agentNoCredentialHint,
}

// agentExecutor is the seam between the graph's run node and the model call.
// Implementations MUST honour ctx cancellation so a task.cancel aborts an
// in-flight provider request promptly (Spec 058 FR-006).
type agentExecutor interface {
	// Generate produces the agent step's output for input. step is the
	// structural preset-step label ("" for the default plan→run graph);
	// it is a fixed catalog id, never user content.
	Generate(ctx context.Context, step, input string) (string, error)
}

// stubExecutor is the explicit offline echo (KENAZ_AGENT_EXEC=stub). It
// preserves the pre-058 behaviour byte-for-byte: a cooperative delay (so a
// task.cancel lands mid-run — cancel_test.go) followed by echoing the input.
type stubExecutor struct{}

func (stubExecutor) Generate(ctx context.Context, _ /* step */, input string) (string, error) {
	if err := cooperativeDelay(ctx); err != nil {
		return "", err
	}
	return input, nil
}

// failingExecutor fails every task with a fixed, named error. It is what a
// real-mode process runs with when no provider resolved: tasks land in the
// errored state instead of silently echoing (Spec 058 US3).
type failingExecutor struct{ err error }

func (f failingExecutor) Generate(context.Context, string, string) (string, error) {
	return "", f.err
}

// agentConfigError marks a process-level agent-exec configuration failure
// (no credential, bad mode). The kernel wraps transform errors with node
// context via %w; runTask unwraps this type so the 64-rune wire message leads
// with the NAMED cause ("no_model_credential: ...") rather than the wrapping.
type agentConfigError struct{ inner error }

func (e *agentConfigError) Error() string { return e.inner.Error() }
func (e *agentConfigError) Unwrap() error { return e.inner }

// defaultAgentModels is the per-provider model used when KENAZ_AGENT_MODEL is
// unset. It is an alias of the shared table — this executor and the served
// harness resolve identical defaults by construction, not by convention.
var defaultAgentModels = envprovider.DefaultModels

// resolveAgentExecutor picks the executor for this process from the
// environment. It never fails the process: an unresolvable real mode returns
// a failingExecutor so every dispatched task errors with the named cause.
func resolveAgentExecutor(log *slog.Logger) agentExecutor {
	switch mode := os.Getenv(agentExecEnv); mode {
	case "stub":
		log.Warn("kenaz-harness-vm: agent execution STUB (KENAZ_AGENT_EXEC=stub) — echo graph, offline CI only")
		return stubExecutor{}
	case "", "real":
		// fall through to real resolution below
	default:
		// An unrecognised mode must never silently pick a behaviour — least of
		// all echo. Fail every task with a named config error.
		err := fmt.Errorf("bad_agent_exec_mode: KENAZ_AGENT_EXEC=%q (want \"stub\", \"real\", or unset)", mode)
		log.Error("kenaz-harness-vm: agent execution misconfigured", "err", err)
		return failingExecutor{err: &agentConfigError{inner: err}}
	}

	exec, err := newLLMExecutor()
	if err != nil {
		log.Error("kenaz-harness-vm: agent execution UNAVAILABLE — tasks will error", "err", err)
		return failingExecutor{err: &agentConfigError{inner: err}}
	}
	log.Info("kenaz-harness-vm: agent execution REAL",
		"provider", exec.kind, "model", exec.model, "cred_env", exec.credEnv)
	return exec
}

// llmExecutor drives the agent step through the core/llm registry pipeline
// (capability gate → policy → env credential resolution → retry → adapter).
type llmExecutor struct {
	reg       *registry.Registry
	profileID string

	// Resolution facts, for logging/tests only (structural, never secrets).
	kind    string
	model   string
	credEnv string
}

// newLLMExecutor resolves a provider/model/credential from the environment
// and builds a registry around it. Errors are named and actionable — they are
// what real-mode tasks fail with when the VM has no usable grant.
func newLLMExecutor() (*llmExecutor, error) {
	res, err := envprovider.Resolve(os.Getenv, agentEnvOptions)
	if err != nil {
		return nil, err
	}

	// A private registry with the env-backed secrets resolver: credential
	// BYTES are read per-request from the named env var and zeroized after
	// each call; they never persist on this struct.
	reg, err := registry.New(registry.Options{Resolver: credref.New(secrets.NewMemoryBackend())})
	if err != nil {
		return nil, fmt.Errorf("agent_exec_registry: %w", err)
	}
	if err := reg.LoadProfiles([]llm.ProviderProfile{res.Profile(agentProfileID)}); err != nil {
		return nil, fmt.Errorf("agent_exec_profile: %w", err)
	}
	return &llmExecutor{
		reg:       reg,
		profileID: agentProfileID,
		kind:      res.Kind,
		model:     res.Model,
		credEnv:   res.CredEnv,
	}, nil
}

// Generate runs one model call for the agent step. The full response text is
// returned so the graph emits it at node completion through the existing
// task.running chunk path (the kernel surfaces node outputs after the run;
// per-token streaming is a kernel capability we deliberately do not add here).
func (e *llmExecutor) Generate(ctx context.Context, step, input string) (string, error) {
	req := llm.GenerationRequest{
		ProfileID: e.profileID,
		Messages:  []llm.Message{llm.NewTextMessage(llm.RoleUser, input)},
	}
	if step != "" {
		// step is a fixed workflow-catalog label (structural, not user content).
		req.System = fmt.Sprintf("You are executing the %q step of a multi-step workflow. Produce this step's output for the input below.", step)
	}

	stream, err := e.reg.Stream(ctx, req)
	if err != nil {
		return "", err
	}

	// Drain the stream, accumulating text deltas as a fallback in case the
	// terminal Response carries no content. A cancelled ctx aborts the
	// upstream connection immediately (FR-006): Cancel() tears down the
	// provider call and we return without waiting on the events channel —
	// adapters honour ctx themselves, so the producer goroutine unwinds.
	var streamed strings.Builder
	events := stream.Events()
drain:
	for {
		select {
		case <-ctx.Done():
			_ = stream.Cancel()
			return "", ctx.Err()
		case ev, ok := <-events:
			if !ok {
				break drain
			}
			if ev.Kind == llm.StreamText {
				streamed.WriteString(ev.Text)
			}
		}
	}

	resp, err := stream.Final()
	if err != nil {
		return "", err
	}
	var out strings.Builder
	for _, c := range resp.Content {
		if c.Type != "text" && c.Type != "" {
			continue
		}
		if c.Text == "" {
			continue
		}
		if out.Len() > 0 {
			out.WriteString("\n\n")
		}
		out.WriteString(c.Text)
	}
	if out.Len() == 0 {
		return streamed.String(), nil
	}
	return out.String(), nil
}
