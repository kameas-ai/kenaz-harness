package redact

import (
	"errors"
	"testing"
)

func TestLintPermissionPayload(t *testing.T) {
	tests := []struct {
		name    string
		payload map[string]any
		wantErr error
	}{
		{
			name: "argv present at top level — reject",
			payload: map[string]any{
				"argv":       "git commit -m 'secret message'",
				"session_id": "sess-123",
			},
			wantErr: ErrArgvPresent,
		},
		{
			name: "argv present nested — reject",
			payload: map[string]any{
				"session_id": "sess-123",
				"context": map[string]any{
					"argv": []string{"git", "commit"},
				},
			},
			wantErr: ErrArgvPresent,
		},
		{
			name: "argv deeply nested — reject",
			payload: map[string]any{
				"outer": map[string]any{
					"inner": map[string]any{
						"argv": "rm -rf /",
					},
				},
			},
			wantErr: ErrArgvPresent,
		},
		{
			name: "allowed fields only — pass",
			payload: map[string]any{
				"session_id":     "sess-abc",
				"pattern":        "git status",
				"decision":       "allow",
				"policy_id":      "pol-001",
				"prompt_id":      "prm-001",
				"dangerous_tier": 0,
				"scope":          "project",
			},
			wantErr: nil,
		},
		{
			name:    "empty payload — pass",
			payload: map[string]any{},
			wantErr: nil,
		},
		{
			name: "unknown fields present — pass (forward-compat)",
			payload: map[string]any{
				"session_id":   "sess-xyz",
				"future_field": "some-value",
				"pattern":      "ls -la",
			},
			wantErr: nil,
		},
		{
			name: "path and tool_name present — pass",
			payload: map[string]any{
				"session_id":  "sess-xyz",
				"path":        "/home/user/project/main.go",
				"tool_name":   "filesystem__read_file",
				"server_name": "filesystem",
				"purpose":     "provider_call",
			},
			wantErr: nil,
		},
		{
			name: "nested map without argv — pass",
			payload: map[string]any{
				"session_id": "sess-abc",
				"metadata": map[string]any{
					"pattern":   "git log",
					"policy_id": "pol-002",
				},
			},
			wantErr: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := lintPermissionPayload(tc.payload)
			if tc.wantErr != nil {
				if err == nil {
					t.Fatalf("expected error %v, got nil", tc.wantErr)
				}
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("expected %v, got %v", tc.wantErr, err)
				}
			} else {
				if err != nil {
					t.Fatalf("expected nil error, got %v", err)
				}
			}
		})
	}
}
