package agent

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/pkg/errors"

	"github.com/raoptimus/kodrun/internal/llm"
)

// ClassifyKind enumerates the categories the response classifier may assign.
type ClassifyKind string

const (
	ClassifyKindPlan                 ClassifyKind = "plan"
	ClassifyKindQuestionAnswer       ClassifyKind = "question_answer"
	ClassifyKindClarificationRequest ClassifyKind = "clarification_request"
	ClassifyKindStatus               ClassifyKind = "status"
	ClassifyKindOther                ClassifyKind = "other"
)

// ClassifySuggestedAction enumerates the follow-up actions the classifier may suggest.
type ClassifySuggestedAction string

const (
	ClassifyActionApprovePlan    ClassifySuggestedAction = "approve_plan"
	ClassifyActionAnswerQuestion ClassifySuggestedAction = "answer_question"
	ClassifyActionNone           ClassifySuggestedAction = "none"
)

// ClassifyResult is the structured verdict of the response classifier.
type ClassifyResult struct {
	Kind            ClassifyKind            `json:"kind"`
	NeedsUserAction bool                    `json:"needs_user_action"`
	SuggestedAction ClassifySuggestedAction `json:"suggested_action"`
	CTAText         string                  `json:"cta_text"`
}

// safeClassifyDefault is the value returned when the classifier fails or times out.
// It must be inert: no dialog, no extra UI events.
func safeClassifyDefault() ClassifyResult {
	return ClassifyResult{
		Kind:            ClassifyKindOther,
		NeedsUserAction: false,
		SuggestedAction: ClassifyActionNone,
	}
}

// ClassifyResponse asks an LLM to classify a previously produced agent response.
// It is intended to be called from a background goroutine after the main agent
// finished its turn. On any error or timeout it returns safeClassifyDefault().
//
// timeout caps the whole call (including LLM time). Pass 0 to disable.
func ClassifyResponse(
	ctx context.Context,
	client llm.Client,
	model, lang, userInput, agentResponse string,
	timeout time.Duration,
) (ClassifyResult, error) {
	if client == nil || model == "" {
		return safeClassifyDefault(), errors.New("classifier: missing client or model")
	}
	if strings.TrimSpace(agentResponse) == "" {
		return safeClassifyDefault(), nil
	}

	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	systemPrompt := systemPromptForRole(RoleResponseClassifier, lang, "", "", nil)
	userPayload := "USER_INPUT:\n" + userInput + "\n\nAGENT_RESPONSE:\n" + agentResponse

	req := llm.ChatRequest{
		Model: model,
		Messages: []llm.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPayload},
		},
		Options: map[string]any{
			"temperature": 0,
		},
	}

	chunk, err := client.ChatSync(ctx, &req)
	if err != nil {
		return safeClassifyDefault(), errors.WithMessage(err, "classifier chat")
	}

	res, err := parseClassifyJSON(chunk.Content)
	if err != nil {
		return safeClassifyDefault(), errors.WithMessage(err, "classifier parse")
	}
	return res, nil
}

// parseClassifyJSON extracts a JSON object from raw model output and decodes it
// into a ClassifyResult. It tolerates surrounding markdown fences and trailing prose.
func parseClassifyJSON(raw string) (ClassifyResult, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ClassifyResult{}, errors.New("empty response")
	}

	// Strip markdown code fences if present.
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```JSON")
		s = strings.TrimPrefix(s, "```")
		if i := strings.LastIndex(s, "```"); i >= 0 {
			s = s[:i]
		}
		s = strings.TrimSpace(s)
	}

	// Locate the first '{' and the matching last '}' to tolerate trailing prose.
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return ClassifyResult{}, errors.New("no JSON object found")
	}
	jsonBlob := s[start : end+1]

	var res ClassifyResult
	if err := json.Unmarshal([]byte(jsonBlob), &res); err != nil {
		return ClassifyResult{}, errors.WithMessage(err, "unmarshal")
	}

	// Defensive normalization: enforce schema invariants.
	switch res.Kind {
	case ClassifyKindPlan, ClassifyKindQuestionAnswer, ClassifyKindClarificationRequest, ClassifyKindStatus, ClassifyKindOther:
	default:
		res.Kind = ClassifyKindOther
	}
	switch res.SuggestedAction {
	case ClassifyActionApprovePlan, ClassifyActionAnswerQuestion, ClassifyActionNone:
	default:
		res.SuggestedAction = ClassifyActionNone
	}
	if res.SuggestedAction != ClassifyActionApprovePlan {
		res.CTAText = ""
	}
	return res, nil
}
