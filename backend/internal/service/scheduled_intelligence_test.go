package service

import (
	"strings"
	"testing"
)

func TestGradeIntelligence(t *testing.T) {
	cases := []struct {
		name, kind, answer, response, wantStatus string
		choices                                  []string
		wantScore                                *int
	}{
		{name: "choice correct", kind: "choice", answer: "B", response: " b ", choices: []string{"first", "second"}, wantStatus: "correct", wantScore: testScorePtr(100)},
		{name: "choice wrong", kind: "choice", answer: "B", response: "A", choices: []string{"first", "second"}, wantStatus: "incorrect", wantScore: testScorePtr(0)},
		{name: "choice ambiguous", kind: "choice", answer: "B", response: "I think B", choices: []string{"first", "second"}, wantStatus: "pending"},
		{name: "short answer", kind: "short_answer", answer: "New York", response: " NEW   YORK ", wantStatus: "correct", wantScore: testScorePtr(100)},
		{name: "open", kind: "open", response: "<html></html>", wantStatus: "pending"},
		{name: "blank", kind: "short_answer", answer: "42", wantStatus: "pending"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, score := gradeIntelligence(&IntelligenceQuestion{Kind: tc.kind, Answer: tc.answer, Choices: tc.choices}, tc.response)
			if status != tc.wantStatus || (score == nil) != (tc.wantScore == nil) || (score != nil && *score != *tc.wantScore) {
				t.Fatalf("grade = (%q, %v), want (%q, %v)", status, score, tc.wantStatus, tc.wantScore)
			}
		})
	}
}

func testScorePtr(n int) *int { return &n }

func TestQuestionPromptIsIncludedInNativePayloads(t *testing.T) {
	payload, err := createTestPayload("claude", "Compute 17 + 25")
	if err != nil {
		t.Fatal(err)
	}
	messages := payload["messages"].([]map[string]any)
	parts := messages[0]["content"].([]map[string]any)
	if parts[0]["text"] != "Compute 17 + 25" || payload["max_tokens"] != 4096 {
		t.Fatalf("Claude prompt not forwarded: %v", payload)
	}
	responses := createOpenAITestPayload("gpt", false, "Compute 17 + 25")
	input := responses["input"].([]map[string]any)
	content := input[0]["content"].([]map[string]any)
	if content[0]["text"] != "Compute 17 + 25" {
		t.Fatalf("Responses prompt not forwarded: %v", responses)
	}
	if strings.Contains(parts[0]["text"].(string), "hi") {
		t.Fatal("default probe leaked into intelligence question")
	}
}
