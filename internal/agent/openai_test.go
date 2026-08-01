package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeHandler struct{ calls int }

func (f *fakeHandler) Tools() []FunctionTool {
	return []FunctionTool{{Type: "function", Name: "inspect", Description: "inspect", Parameters: map[string]any{"type": "object", "properties": map[string]any{}, "required": []string{}, "additionalProperties": false}, Strict: true}}
}
func (f *fakeHandler) Call(_ context.Context, name string, args json.RawMessage) (any, error) {
	f.calls++
	return map[string]string{"runtime": "go"}, nil
}

func TestResponsesToolLoopReplaysItems(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["store"] != false {
			t.Errorf("expected store=false")
		}
		w.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			_, _ = w.Write([]byte(`{"id":"resp_1","status":"completed","output":[{"type":"reasoning","id":"rs_1","encrypted_content":"opaque"},{"type":"function_call","id":"fc_1","call_id":"call_1","name":"inspect","arguments":"{}"}]}`))
			return
		}
		input, _ := body["input"].([]any)
		if len(input) < 4 {
			t.Errorf("expected user, replayed outputs, and function output; got %d", len(input))
		}
		_, _ = w.Write([]byte(`{"id":"resp_2","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Detected Go."}]}]}`))
	}))
	defer server.Close()
	handler := &fakeHandler{}
	client := Client{APIKey: "test", BaseURL: server.URL, Model: DefaultModel, HTTP: server.Client()}
	answer, err := client.Run(context.Background(), "Use tools.", "inspect", handler)
	if err != nil {
		t.Fatal(err)
	}
	if answer != "Detected Go." || handler.calls != 1 || requests != 2 {
		t.Fatalf("unexpected result: %q calls=%d requests=%d", answer, handler.calls, requests)
	}
}

func TestEmptyToolSchemaUsesRequiredArray(t *testing.T) {
	tools := (Tools{}).Tools()
	required, ok := tools[0].Parameters["required"].([]string)
	if !ok || required == nil || len(required) != 0 {
		t.Fatalf("strict schema required must be an empty array: %#v", tools[0].Parameters["required"])
	}
}
