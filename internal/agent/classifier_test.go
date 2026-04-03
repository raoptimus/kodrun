package agent

import (
	"testing"
)

func TestParseClassifyJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		wantKind    ClassifyKind
		wantAction  ClassifySuggestedAction
		wantNeeds   bool
		wantCTA     string
		wantErr     bool
	}{
		{
			name: "valid plain JSON",
			input: `{
				"kind": "plan",
				"needs_user_action": true,
				"suggested_action": "approve_plan",
				"cta_text": "Подтвердите, чтобы начать."
			}`,
			wantKind:   ClassifyKindPlan,
			wantAction: ClassifyActionApprovePlan,
			wantNeeds:  true,
			wantCTA:    "Подтвердите, чтобы начать.",
		},
		{
			name: "JSON in markdown fences",
			input: "```json\n" +
				`{"kind":"question_answer","needs_user_action":false,"suggested_action":"none","cta_text":""}` +
				"\n```",
			wantKind:   ClassifyKindQuestionAnswer,
			wantAction: ClassifyActionNone,
			wantNeeds:  false,
			wantCTA:    "",
		},
		{
			name: "JSON with surrounding prose",
			input: `Sure, here is the classification:
{"kind":"plan","needs_user_action":true,"suggested_action":"approve_plan","cta_text":"Go?"}
Hope this helps!`,
			wantKind:   ClassifyKindPlan,
			wantAction: ClassifyActionApprovePlan,
			wantNeeds:  true,
			wantCTA:    "Go?",
		},
		{
			name: "unknown kind normalized to other",
			input: `{"kind":"weird","needs_user_action":false,"suggested_action":"none","cta_text":""}`,
			wantKind:   ClassifyKindOther,
			wantAction: ClassifyActionNone,
			wantNeeds:  false,
		},
		{
			name: "cta_text dropped when action is not approve_plan",
			input: `{"kind":"question_answer","needs_user_action":false,"suggested_action":"none","cta_text":"leftover"}`,
			wantKind:   ClassifyKindQuestionAnswer,
			wantAction: ClassifyActionNone,
			wantCTA:    "",
		},
		{
			name:    "empty input",
			input:   "",
			wantErr: true,
		},
		{
			name:    "no JSON object",
			input:   "I am not JSON at all",
			wantErr: true,
		},
		{
			name:    "invalid JSON",
			input:   "{not valid",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseClassifyJSON(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (result=%+v)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Kind != tt.wantKind {
				t.Errorf("Kind: got %q, want %q", got.Kind, tt.wantKind)
			}
			if got.SuggestedAction != tt.wantAction {
				t.Errorf("SuggestedAction: got %q, want %q", got.SuggestedAction, tt.wantAction)
			}
			if got.NeedsUserAction != tt.wantNeeds {
				t.Errorf("NeedsUserAction: got %v, want %v", got.NeedsUserAction, tt.wantNeeds)
			}
			if got.CTAText != tt.wantCTA {
				t.Errorf("CTAText: got %q, want %q", got.CTAText, tt.wantCTA)
			}
		})
	}
}

func TestSafeClassifyDefault(t *testing.T) {
	t.Parallel()
	d := safeClassifyDefault()
	if d.NeedsUserAction {
		t.Errorf("default must not require user action")
	}
	if d.SuggestedAction != ClassifyActionNone {
		t.Errorf("default action must be none, got %q", d.SuggestedAction)
	}
	if d.Kind != ClassifyKindOther {
		t.Errorf("default kind must be other, got %q", d.Kind)
	}
}
