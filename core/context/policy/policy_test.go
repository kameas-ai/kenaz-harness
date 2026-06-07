package policy

import (
	"errors"
	"strings"
	"testing"

	pack "github.com/kameas-ai/kenaz-harness/core/context/pack"
)

func TestDefault_HasSpecDefaults(t *testing.T) {
	d := Default()
	if d.Conflict != ConflictOverrideByPrecedence {
		t.Errorf("Conflict = %q, want override_by_precedence", d.Conflict)
	}
	if d.Verification != PostureFailClosed {
		t.Errorf("Verification = %q, want fail_closed", d.Verification)
	}
	if d.BudgetMode != BudgetSoftWarn {
		t.Errorf("BudgetMode = %q, want keep_by_name_order_then_warn", d.BudgetMode)
	}
	if d.LayerSizeBudget != pack.DefaultLayerSizeBudget {
		t.Errorf("budget = %d, want %d", d.LayerSizeBudget, pack.DefaultLayerSizeBudget)
	}
}

func TestStrict_FailsOnConflictAndOversize(t *testing.T) {
	s := Strict()
	if s.Conflict != ConflictFail {
		t.Errorf("Conflict = %q, want fail_on_conflict", s.Conflict)
	}
	if s.BudgetMode != BudgetHardFail {
		t.Errorf("BudgetMode = %q, want hard_fail", s.BudgetMode)
	}
}

func TestNormalised_FillsBlanks(t *testing.T) {
	r := Resolution{}
	n := r.Normalised()
	if n.Conflict != ConflictOverrideByPrecedence ||
		n.Verification != PostureFailClosed ||
		n.BudgetMode != BudgetSoftWarn ||
		n.LayerSizeBudget != pack.DefaultLayerSizeBudget {
		t.Errorf("Normalised() = %+v", n)
	}
}

func TestEffectiveBudget_DisableWithNegative(t *testing.T) {
	r := Resolution{LayerSizeBudget: -1}
	if r.EffectiveBudget() != 0 {
		t.Errorf("negative budget should disable; got %d", r.EffectiveBudget())
	}
}

func TestErrConflict_MessageShape(t *testing.T) {
	one := &ErrConflict{Conflicts: []ConflictReport{
		{EntryName: "term", LeftLayer: pack.LayerOrg, RightLayer: pack.LayerTeam},
	}}
	if !strings.Contains(one.Error(), "term") {
		t.Errorf("single-conflict message must mention entry name; got %q", one.Error())
	}
	multi := &ErrConflict{Conflicts: []ConflictReport{
		{EntryName: "a"}, {EntryName: "b"}, {EntryName: "c"},
	}}
	if !strings.Contains(multi.Error(), "3") {
		t.Errorf("multi-conflict message must report count; got %q", multi.Error())
	}
}

func TestErrConflict_IsTypedError(t *testing.T) {
	var ec *ErrConflict
	err := error(&ErrConflict{Conflicts: []ConflictReport{{EntryName: "x"}}})
	if !errors.As(err, &ec) {
		t.Errorf("ErrConflict must be typed for errors.As")
	}
}

func TestErrOversizeLayer_Message(t *testing.T) {
	e := &ErrOversizeLayer{
		Layer:  pack.LayerPersonal,
		Pack:   pack.PackRef{Name: "p", Version: "1"},
		Bytes:  4096,
		Budget: 1024,
	}
	if !strings.Contains(e.Error(), "personal") || !strings.Contains(e.Error(), "4096") {
		t.Errorf("message missing context; got %q", e.Error())
	}
}
