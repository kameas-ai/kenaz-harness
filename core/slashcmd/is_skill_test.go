package slashcmd

import "testing"

// Skills share the registry with built-ins but rank below user commands;
// IsSkill is how a caller tells them apart.
func TestIsSkill_DistinguishesSkillsFromBuiltins(t *testing.T) {
	t.Parallel()
	reg, err := NewRegistry(Deps{})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if err := reg.RegisterSkill(skillCommand{skill: Skill{ID: "org/pr", Trigger: "pr", Kind: KindText, Body: "x"}}); err != nil {
		t.Fatalf("RegisterSkill: %v", err)
	}
	var sawSkill, sawBuiltin bool
	for _, c := range reg.List() {
		switch {
		case c.Name() == "pr":
			sawSkill = true
			if !IsSkill(c) {
				t.Errorf("registered skill %q not reported as a skill", c.Name())
			}
		case c.Name() == "help":
			sawBuiltin = true
			if IsSkill(c) {
				t.Errorf("built-in %q reported as a skill", c.Name())
			}
		}
	}
	if !sawSkill || !sawBuiltin {
		t.Fatalf("List missing skill (%v) or built-in help (%v)", sawSkill, sawBuiltin)
	}
}
