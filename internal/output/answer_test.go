package output

import (
	"encoding/json"
	"testing"
)

func TestAnswerSchemaAndString(t *testing.T) {
	t.Parallel()

	schema := AnswerSchema()
	if schema["type"] != "object" {
		t.Fatalf("unexpected schema %#v", schema)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected schema properties %#v", schema["properties"])
	}
	answerField, ok := properties["answer"].(map[string]any)
	if !ok || answerField["type"] != "string" {
		t.Fatalf("unexpected answer field %#v", properties["answer"])
	}

	var schemaFromString map[string]any
	if err := json.Unmarshal([]byte(AnswerSchemaString()), &schemaFromString); err != nil {
		t.Fatalf("schema string should be valid json: %v", err)
	}
}

func TestParseAnswer(t *testing.T) {
	t.Parallel()

	answer, err := ParseAnswer(`{"answer":"steady course"}`)
	if err != nil || answer != "steady course" {
		t.Fatalf("unexpected answer %q err=%v", answer, err)
	}

	answer, err = ParseAnswer(`{"answer":"<final>\nKeep the thread steady.\n</final>"}`)
	if err != nil || answer != "Keep the thread steady." {
		t.Fatalf("unexpected final-wrapped answer %q err=%v", answer, err)
	}

	if _, err := ParseAnswer(`{"response":"wrong field"}`); err == nil {
		t.Fatal("expected wrong field to fail")
	}
	if _, err := ParseAnswer(`plain text`); err == nil {
		t.Fatal("expected plain text to fail")
	}
}
