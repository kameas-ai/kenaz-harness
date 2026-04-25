package pack

import (
	"strings"
	"testing"
)

func TestValidate_RequiresName(t *testing.T) {
	p := &ContextPack{Ref: PackRef{Version: "1.0.0", Layer: LayerOrg}}
	if err := Validate(p, ValidatorOptions{}); !HasCode(err, CodeRequiredFieldMissing) {
		t.Fatalf("expected CodeRequiredFieldMissing, got %v", err)
	}
}

func TestValidate_RequiresVersion(t *testing.T) {
	p := &ContextPack{Ref: PackRef{Name: "x", Layer: LayerOrg},
		Signature: SignatureRef{Path: "p", AnchorID: "a"}}
	if err := Validate(p, ValidatorOptions{}); !HasCode(err, CodeRequiredFieldMissing) {
		t.Fatalf("expected CodeRequiredFieldMissing, got %v", err)
	}
}

func TestValidate_RejectsBadLayer(t *testing.T) {
	p := &ContextPack{Ref: PackRef{Name: "x", Version: "1.0.0", Layer: "department"},
		Signature: SignatureRef{Path: "p", AnchorID: "a"}}
	if err := Validate(p, ValidatorOptions{}); !HasCode(err, CodeInvalidLayer) {
		t.Fatalf("expected CodeInvalidLayer, got %v", err)
	}
}

func TestValidate_DuplicateEntryName(t *testing.T) {
	p := &ContextPack{
		Ref:       PackRef{Name: "x", Version: "1.0.0", Layer: LayerOrg},
		Signature: SignatureRef{Path: "p", AnchorID: "a"},
		Entries: []ContextEntry{
			{Name: "term", Kind: KindGlossary, SizeBytes: 10},
			{Name: "term", Kind: KindGlossary, SizeBytes: 10},
		},
	}
	if err := Validate(p, ValidatorOptions{}); !HasCode(err, CodeDuplicateEntryName) {
		t.Fatalf("expected CodeDuplicateEntryName, got %v", err)
	}
}

func TestValidate_RejectsInvalidEntryKind(t *testing.T) {
	p := &ContextPack{
		Ref:       PackRef{Name: "x", Version: "1.0.0", Layer: LayerOrg},
		Signature: SignatureRef{Path: "p", AnchorID: "a"},
		Entries: []ContextEntry{
			{Name: "term", Kind: "haiku", SizeBytes: 1},
		},
	}
	if err := Validate(p, ValidatorOptions{}); !HasCode(err, CodeInvalidEntryKind) {
		t.Fatalf("expected CodeInvalidEntryKind, got %v", err)
	}
}

func TestValidate_RequiresSignatureRef(t *testing.T) {
	p := &ContextPack{Ref: PackRef{Name: "x", Version: "1.0.0", Layer: LayerOrg}}
	if err := Validate(p, ValidatorOptions{}); !HasCode(err, CodeSignatureRefMissing) {
		t.Fatalf("expected CodeSignatureRefMissing, got %v", err)
	}
}

func TestValidate_SignatureOptionalToggle(t *testing.T) {
	off := false
	p := &ContextPack{Ref: PackRef{Name: "x", Version: "1.0.0", Layer: LayerOrg}}
	if err := Validate(p, ValidatorOptions{SignatureRequired: &off}); err != nil {
		t.Fatalf("with SignatureRequired=false, validate must pass; got %v", err)
	}
}

func TestValidate_SoftSizeBudgetWarns(t *testing.T) {
	p := &ContextPack{
		Ref:       PackRef{Name: "x", Version: "1.0.0", Layer: LayerOrg},
		Signature: SignatureRef{Path: "p", AnchorID: "a"},
		Entries: []ContextEntry{
			{Name: "big", Kind: KindExplanation,
				Body:      []byte(strings.Repeat("y", 1024)),
				SizeBytes: 1024},
		},
	}
	err := Validate(p, ValidatorOptions{LayerSizeBudget: 256})
	if err != nil {
		t.Fatalf("soft mode must not return error; got %v", err)
	}
	if len(p.Warnings) == 0 || p.Warnings[0].Code != string(CodeOversizeLayer) {
		t.Fatalf("expected oversize_layer warning, got %v", p.Warnings)
	}
}

func TestValidate_HardSizeBudgetFails(t *testing.T) {
	p := &ContextPack{
		Ref:       PackRef{Name: "x", Version: "1.0.0", Layer: LayerOrg},
		Signature: SignatureRef{Path: "p", AnchorID: "a"},
		Entries: []ContextEntry{
			{Name: "big", Kind: KindExplanation, SizeBytes: 1024},
		},
	}
	err := Validate(p, ValidatorOptions{LayerSizeBudget: 256, HardSizeFail: true})
	if !HasCode(err, CodeOversizeLayer) {
		t.Fatalf("hard-fail mode: expected CodeOversizeLayer, got %v", err)
	}
}

func TestValidate_PerEntryCeiling(t *testing.T) {
	p := &ContextPack{
		Ref:       PackRef{Name: "x", Version: "1.0.0", Layer: LayerOrg},
		Signature: SignatureRef{Path: "p", AnchorID: "a"},
		Entries: []ContextEntry{
			{Name: "huge", Kind: KindExplanation, SizeBytes: 100},
		},
	}
	if err := Validate(p, ValidatorOptions{MaxEntrySize: 10}); !HasCode(err, CodeOversizeEntry) {
		t.Fatalf("expected CodeOversizeEntry, got %v", err)
	}
}

func TestValidate_BadName(t *testing.T) {
	p := &ContextPack{Ref: PackRef{Name: "Bad NAME", Version: "1", Layer: LayerOrg},
		Signature: SignatureRef{Path: "p", AnchorID: "a"}}
	if err := Validate(p, ValidatorOptions{}); !HasCode(err, CodeInvalidName) {
		t.Fatalf("expected CodeInvalidName, got %v", err)
	}
}
