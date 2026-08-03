package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const DefaultModel = "gpt-5.6-sol"

type FunctionTool struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	Strict      bool           `json:"strict"`
}

type ToolCall struct {
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type response struct {
	ID     string            `json:"id"`
	Status string            `json:"status"`
	Output []json.RawMessage `json:"output"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type Client struct {
	APIKey          string
	BaseURL         string
	Model           string
	ReasoningEffort string
	HTTP            *http.Client
	MaxToolRounds   int
}

// Session keeps the complete local Responses API input history for a
// store=false, multi-turn interaction. History includes assistant reasoning,
// messages, function calls, and function outputs so subsequent turns retain
// the same context as the model's previous turn.
type Session struct {
	Client       Client
	Instructions string
	Handler      Handler
	history      []json.RawMessage
}

type Handler interface {
	Tools() []FunctionTool
	Call(context.Context, string, json.RawMessage) (any, error)
}

func (c Client) Run(ctx context.Context, instructions, goal string, handler Handler) (string, error) {
	session := NewSession(c, instructions, handler)
	return session.Send(ctx, goal)
}

func NewSession(client Client, instructions string, handler Handler) *Session {
	return &Session{Client: client, Instructions: instructions, Handler: handler}
}

func (s *Session) Send(ctx context.Context, message string) (string, error) {
	return s.send(ctx, message, nil)
}

// SendStream behaves like Send and emits final answer text deltas as they
// arrive. Tool calls still complete deterministically before the next model
// round begins.
func (s *Session) SendStream(ctx context.Context, message string, onTextDelta func(string)) (string, error) {
	return s.send(ctx, message, onTextDelta)
}

func (s *Session) send(ctx context.Context, message string, onTextDelta func(string)) (string, error) {
	if s == nil {
		return "", fmt.Errorf("agent session is not configured")
	}
	if s.Handler == nil {
		return "", fmt.Errorf("agent session handler is not configured")
	}
	if strings.TrimSpace(message) == "" {
		return "", fmt.Errorf("agent message is required")
	}
	client, err := s.Client.normalized()
	if err != nil {
		return "", err
	}
	s.Client = client
	turnStart := len(s.history)
	responseReceived := false
	defer func() {
		if !responseReceived {
			s.history = s.history[:turnStart]
		}
	}()

	userItem, _ := json.Marshal(map[string]any{"role": "user", "content": message})
	s.history = append(s.history, userItem)
	for round := 0; round < client.MaxToolRounds; round++ {
		var resp response
		if onTextDelta != nil {
			resp, err = client.createResponseStream(ctx, s.Instructions, s.history, s.Handler.Tools(), onTextDelta)
		} else {
			resp, err = client.createResponse(ctx, s.Instructions, s.history, s.Handler.Tools())
		}
		if err != nil {
			return "", err
		}
		responseReceived = true
		s.history = append(s.history, resp.Output...)
		calls := make([]ToolCall, 0)
		for _, raw := range resp.Output {
			var item ToolCall
			if json.Unmarshal(raw, &item) == nil && item.Type == "function_call" {
				calls = append(calls, item)
			}
		}
		if len(calls) == 0 {
			text := outputText(resp.Output)
			if text == "" {
				return "", fmt.Errorf("agent response completed without text or tool calls")
			}
			return text, nil
		}
		for _, call := range calls {
			result, callErr := s.Handler.Call(ctx, call.Name, json.RawMessage(call.Arguments))
			payload := map[string]any{"ok": callErr == nil, "result": result}
			if callErr != nil {
				payload["error"] = callErr.Error()
			}
			encoded, _ := json.Marshal(payload)
			item, _ := json.Marshal(map[string]any{"type": "function_call_output", "call_id": call.CallID, "output": string(encoded)})
			s.history = append(s.history, item)
		}
	}
	return "", fmt.Errorf("agent exceeded %d tool rounds", client.MaxToolRounds)
}

func (s *Session) Reset() {
	if s != nil {
		s.history = nil
	}
}

func (s *Session) HistoryItems() int {
	if s == nil {
		return 0
	}
	return len(s.history)
}

func (c Client) normalized() (Client, error) {
	if strings.TrimSpace(c.APIKey) == "" {
		return c, fmt.Errorf("OPENAI_API_KEY is required for agent mode")
	}
	if c.BaseURL == "" {
		c.BaseURL = "https://api.openai.com/v1"
	}
	if c.Model == "" {
		c.Model = DefaultModel
	}
	if c.ReasoningEffort == "" {
		c.ReasoningEffort = "medium"
	}
	if c.HTTP == nil {
		c.HTTP = &http.Client{Timeout: 3 * time.Minute}
	}
	if c.MaxToolRounds <= 0 {
		c.MaxToolRounds = 12
	}
	return c, nil
}

func (c Client) createResponse(ctx context.Context, instructions string, input []json.RawMessage, tools []FunctionTool) (response, error) {
	body := c.responseBody(instructions, input, tools)
	encoded, err := json.Marshal(body)
	if err != nil {
		return response{}, err
	}
	req, err := c.responseRequest(ctx, encoded)
	if err != nil {
		return response{}, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return response{}, fmt.Errorf("OpenAI Responses API: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return response{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return response{}, fmt.Errorf("OpenAI Responses API returned %s: %s", resp.Status, redactAPIError(raw))
	}
	var decoded response
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return response{}, fmt.Errorf("decode OpenAI response: %w", err)
	}
	if decoded.Error != nil {
		return decoded, fmt.Errorf("OpenAI response error %s: %s", decoded.Error.Code, decoded.Error.Message)
	}
	return decoded, nil
}

func (c Client) createResponseStream(ctx context.Context, instructions string, input []json.RawMessage, tools []FunctionTool, onTextDelta func(string)) (response, error) {
	body := c.responseBody(instructions, input, tools)
	body["stream"] = true
	encoded, err := json.Marshal(body)
	if err != nil {
		return response{}, err
	}
	req, err := c.responseRequest(ctx, encoded)
	if err != nil {
		return response{}, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return response{}, fmt.Errorf("OpenAI Responses API: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		if readErr != nil {
			return response{}, readErr
		}
		return response{}, fmt.Errorf("OpenAI Responses API returned %s: %s", resp.Status, redactAPIError(raw))
	}

	var completed response
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64<<10), 8<<20)
	var data strings.Builder
	flush := func() error {
		payload := strings.TrimSpace(data.String())
		data.Reset()
		if payload == "" || payload == "[DONE]" {
			return nil
		}
		var event struct {
			Type     string          `json:"type"`
			Delta    string          `json:"delta"`
			Response json.RawMessage `json:"response"`
			Message  string          `json:"message"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return fmt.Errorf("decode OpenAI stream event: %w", err)
		}
		switch event.Type {
		case "response.output_text.delta":
			if event.Delta != "" && onTextDelta != nil {
				onTextDelta(event.Delta)
			}
		case "response.completed", "response.failed", "response.incomplete":
			if len(event.Response) == 0 {
				return fmt.Errorf("OpenAI stream %s without response", event.Type)
			}
			if err := json.Unmarshal(event.Response, &completed); err != nil {
				return fmt.Errorf("decode completed OpenAI response: %w", err)
			}
			if event.Type != "response.completed" {
				if completed.Error != nil {
					return fmt.Errorf("OpenAI response error %s: %s", completed.Error.Code, completed.Error.Message)
				}
				return fmt.Errorf("OpenAI response ended with %s", event.Type)
			}
		case "error":
			if event.Message == "" {
				event.Message = "stream error"
			}
			return fmt.Errorf("OpenAI response error: %s", event.Message)
		}
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return response{}, err
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return response{}, fmt.Errorf("read OpenAI stream: %w", err)
	}
	if data.Len() > 0 {
		if err := flush(); err != nil {
			return response{}, err
		}
	}
	if completed.ID == "" {
		return response{}, fmt.Errorf("OpenAI stream ended without a completed response")
	}
	return completed, nil
}

func (c Client) responseBody(instructions string, input []json.RawMessage, tools []FunctionTool) map[string]any {
	return map[string]any{
		"model": c.Model, "instructions": instructions, "input": input, "tools": tools,
		"tool_choice": "auto", "parallel_tool_calls": false, "store": false,
		"include":   []string{"reasoning.encrypted_content"},
		"reasoning": map[string]any{"effort": c.ReasoningEffort},
		"text":      map[string]any{"verbosity": "medium"},
	}
}

func (c Client) responseRequest(ctx context.Context, encoded []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/responses", bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "warden/0.3")
	return req, nil
}

func outputText(items []json.RawMessage) string {
	var parts []string
	for _, raw := range items {
		var item struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.Unmarshal(raw, &item) != nil || item.Type != "message" {
			continue
		}
		for _, content := range item.Content {
			if content.Type == "output_text" && content.Text != "" {
				parts = append(parts, content.Text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func redactAPIError(raw []byte) string {
	text := strings.TrimSpace(string(raw))
	if len(text) > 2000 {
		text = text[:2000] + "…"
	}
	return text
}
