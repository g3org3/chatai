package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type apiRequest struct {
	Model    string       `json:"model"`
	Messages []apiMessage `json:"messages"`
	Tools    []toolDef    `json:"tools,omitempty"`
}

type apiMessage struct {
	Role       string     `json:"role"`
	Content    *string    `json:"content"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type toolDef struct {
	Type     string       `json:"type"`
	Function toolFunction `json:"function"`
}

type toolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type toolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type apiResponse struct {
	Choices []struct {
		Message struct {
			Role      string     `json:"role"`
			Content   string     `json:"content"`
			ToolCalls []toolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

var httpClient = &http.Client{
	Timeout: 120 * time.Second,
}

func strPtr(s string) *string { return &s }

func chatCompletion(host, apiKey, modelName string, messages []apiMessage, tools []toolDef) (*apiResponse, error) {
	reqBody := apiRequest{
		Model:    modelName,
		Messages: messages,
	}
	if len(tools) > 0 {
		reqBody.Tools = tools
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	url := host + "/chat/completions"
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, string(respBody))
	}

	var apiResp apiResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if apiResp.Error != nil {
		return nil, fmt.Errorf("API: %s", apiResp.Error.Message)
	}

	if len(apiResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices returned")
	}

	return &apiResp, nil
}
