package service

import (
	"strings"
	"testing"
)

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
