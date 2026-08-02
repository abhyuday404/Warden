package agent

import (
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

type Handler interface {
	Tools() []FunctionTool
	Call(context.Context, string, json.RawMessage) (any, error)
}

func (c Client) Run(ctx context.Context, instructions, goal string, handler Handler) (string, error) {
	if strings.TrimSpace(c.APIKey) == "" {
		return "", fmt.Errorf("OPENAI_API_KEY is required for agent mode")
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

	userItem, _ := json.Marshal(map[string]any{"role": "user", "content": goal})
	input := []json.RawMessage{userItem}
	for round := 0; round < c.MaxToolRounds; round++ {
		resp, err := c.createResponse(ctx, instructions, input, handler.Tools())
		if err != nil {
			return "", err
		}
		input = append(input, resp.Output...)
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
			result, callErr := handler.Call(ctx, call.Name, json.RawMessage(call.Arguments))
			payload := map[string]any{"ok": callErr == nil, "result": result}
			if callErr != nil {
				payload["error"] = callErr.Error()
			}
			encoded, _ := json.Marshal(payload)
			item, _ := json.Marshal(map[string]any{"type": "function_call_output", "call_id": call.CallID, "output": string(encoded)})
			input = append(input, item)
		}
	}
	return "", fmt.Errorf("agent exceeded %d tool rounds", c.MaxToolRounds)
}

func (c Client) createResponse(ctx context.Context, instructions string, input []json.RawMessage, tools []FunctionTool) (response, error) {
	body := map[string]any{
		"model": c.Model, "instructions": instructions, "input": input, "tools": tools,
		"tool_choice": "auto", "parallel_tool_calls": false, "store": false,
		"include":   []string{"reasoning.encrypted_content"},
		"reasoning": map[string]any{"effort": c.ReasoningEffort},
		"text":      map[string]any{"verbosity": "medium"},
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return response{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/responses", bytes.NewReader(encoded))
	if err != nil {
		return response{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "warden/0.2")
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
