package skill

// dogfood 2026-10-08 round 2: kenaz__skill on a PROMPT-kind command
// returned the rendered prompt as a bare `output`, and the model reported
// "the skill returned a triage prompt rather than a triage result". The
// result now says what it is: an instruction to carry out.

import (
	"context"
	"strings"
	"testing"

	coreslashcmd "github.com/kameas-ai/kenaz-harness/core/slashcmd"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

func TestTool_Call_PromptSkillSaysItIsAnInstruction(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, dir := openSkillTestDB(t)
	store := coreslashcmd.NewStore(db, dir)
	for _, c := range []coreslashcmd.UserCommand{
		{Name: "bughunt", Scope: coreslashcmd.ScopeGlobal, Kind: coreslashcmd.KindPrompt,
			Description: "triage", Body: "Triage the reported bug and list likely causes.", ModelInvokable: true},
		{Name: "motd", Scope: coreslashcmd.ScopeGlobal, Kind: coreslashcmd.KindText,
			Description: "text", Body: "hello", ModelInvokable: true},
	} {
		if err := store.SaveUser(ctx, c); err != nil {
			t.Fatalf("SaveUser(%s): %v", c.Name, err)
		}
	}
	tool := New(Options{Dispatch: coreslashcmd.NewDispatch(store, nil)})
	sctx := toolloop.WithSessionID(ctx, "sess-1")

	var prompt struct {
		Output      string `json:"output"`
		Instruction string `json:"instruction"`
		IsError     bool   `json:"isError"`
	}
	out, _ := tool.Call(sctx, mustMarshal(t, map[string]any{"name": "bughunt"}))
	mustUnmarshal(t, out, &prompt)
	if prompt.IsError || !strings.Contains(prompt.Output, "Triage the reported bug") {
		t.Fatalf("prompt skill result = %s", out)
	}
	if !strings.Contains(prompt.Instruction, "instruction") || !strings.Contains(prompt.Instruction, "not a result") {
		t.Errorf("instruction = %q, want it to say the output is an instruction to carry out", prompt.Instruction)
	}

	var text struct {
		Instruction string `json:"instruction"`
	}
	out, _ = tool.Call(sctx, mustMarshal(t, map[string]any{"name": "motd"}))
	mustUnmarshal(t, out, &text)
	if text.Instruction != "" {
		t.Errorf("text skill carries an instruction %q; only prompt skills should", text.Instruction)
	}

	if !strings.Contains(ToolDescription, "PROMPT skill returns an instruction") {
		t.Errorf("ToolDescription does not document prompt-skill semantics: %q", ToolDescription)
	}
}
