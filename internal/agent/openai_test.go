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

func TestSessionCarriesConversationAcrossTurnsAndCanReset(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body struct {
			Store bool              `json:"store"`
			Input []json.RawMessage `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Store {
			t.Error("interactive sessions must keep store=false")
		}
		w.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			if len(body.Input) != 1 {
				t.Fatalf("first turn input items: %d", len(body.Input))
			}
			_, _ = w.Write([]byte(`{"id":"resp_1","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"First answer."}]}]}`))
			return
		}
		if len(body.Input) != 3 {
			t.Fatalf("second turn should include user, assistant, user; got %d items", len(body.Input))
		}
		_, _ = w.Write([]byte(`{"id":"resp_2","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Second answer."}]}]}`))
	}))
	defer server.Close()

	session := NewSession(Client{APIKey: "test", BaseURL: server.URL, HTTP: server.Client()}, "Be helpful.", &fakeHandler{})
	first, err := session.Send(context.Background(), "first")
	if err != nil || first != "First answer." {
		t.Fatalf("first turn: answer=%q err=%v", first, err)
	}
	second, err := session.Send(context.Background(), "second")
	if err != nil || second != "Second answer." {
		t.Fatalf("second turn: answer=%q err=%v", second, err)
	}
	if session.HistoryItems() != 4 {
		t.Fatalf("unexpected history length: %d", session.HistoryItems())
	}
	session.Reset()
	if session.HistoryItems() != 0 {
		t.Fatal("session reset did not clear history")
	}
}

func TestSessionStreamsTextAndRetainsCompletedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["stream"] != true || body["store"] != false {
			t.Fatalf("unexpected streaming flags: %#v", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"Hello \"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"world\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_stream\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"Hello world\"}]}]}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	var streamed string
	session := NewSession(Client{APIKey: "test", BaseURL: server.URL, HTTP: server.Client()}, "Be helpful.", &fakeHandler{})
	answer, err := session.SendStream(context.Background(), "hello", func(delta string) { streamed += delta })
	if err != nil {
		t.Fatal(err)
	}
	if answer != "Hello world" || streamed != "Hello world" {
		t.Fatalf("answer=%q streamed=%q", answer, streamed)
	}
	if session.HistoryItems() != 2 {
		t.Fatalf("unexpected history length: %d", session.HistoryItems())
	}
}

func TestSessionRollsBackFailedTurn(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body struct {
			Input []json.RawMessage `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if requests == 1 {
			http.Error(w, `{"error":{"message":"temporary"}}`, http.StatusServiceUnavailable)
			return
		}
		if len(body.Input) != 1 {
			t.Fatalf("retry should not contain the failed turn; got %d input items", len(body.Input))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_ok","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Recovered."}]}]}`))
	}))
	defer server.Close()

	session := NewSession(Client{APIKey: "test", BaseURL: server.URL, HTTP: server.Client()}, "Be helpful.", &fakeHandler{})
	if _, err := session.Send(context.Background(), "failed"); err == nil {
		t.Fatal("expected failed turn")
	}
	if session.HistoryItems() != 0 {
		t.Fatalf("failed turn was retained: %d items", session.HistoryItems())
	}
	answer, err := session.Send(context.Background(), "retry")
	if err != nil || answer != "Recovered." {
		t.Fatalf("retry: answer=%q err=%v", answer, err)
	}
}
