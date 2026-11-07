package llm

import "testing"

func TestExtractJSONFence(t *testing.T) {
	got := extractJSON("Вот:\n```json\n{\"a\":1}\n```\nконец")
	if got != `{"a":1}` {
		t.Fatalf("%q", got)
	}
	got = extractJSON("текст {\"a\":\"brace } inside\"} хвост")
	if got != `{"a":"brace } inside"}` {
		t.Fatalf("%q", got)
	}
}
