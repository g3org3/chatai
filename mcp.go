package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
)

type mcpServer struct {
	URL       string
	SessionID string
	Name      string
	Tools     []mcpTool
	nextID    atomic.Int64
}

type mcpTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type jsonRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
	ID      *int64      `json:"id,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
	ID      *int64          `json:"id,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *mcpServer) send(method string, params interface{}, expectResponse bool) (*jsonRPCResponse, error) {
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	if expectResponse {
		id := s.nextID.Add(1)
		req.ID = &id
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	httpReq, err := http.NewRequest("POST", s.URL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	if s.SessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", s.SessionID)
	}

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}
	defer resp.Body.Close()

	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		s.SessionID = sid
	}

	if !expectResponse {
		io.ReadAll(resp.Body)
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}

	contentType := resp.Header.Get("Content-Type")
	var rpcResp jsonRPCResponse

	if strings.Contains(contentType, "text/event-stream") {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data:") {
				data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if json.Unmarshal([]byte(data), &rpcResp) == nil && rpcResp.ID != nil {
					break
				}
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("SSE read: %w", err)
		}
	} else {
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("read: %w", err)
		}
		if err := json.Unmarshal(b, &rpcResp); err != nil {
			return nil, fmt.Errorf("parse: %w", err)
		}
	}

	if rpcResp.Error != nil {
		return nil, fmt.Errorf("RPC error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	return &rpcResp, nil
}

func mcpConnect(url string) (*mcpServer, error) {
	server := &mcpServer{URL: url}

	initParams := map[string]interface{}{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]interface{}{},
		"clientInfo": map[string]string{
			"name":    "chatai",
			"version": "0.1.0",
		},
	}

	resp, err := server.send("initialize", initParams, true)
	if err != nil {
		return nil, fmt.Errorf("initialize: %w", err)
	}

	var initResult struct {
		ServerInfo struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}
	if json.Unmarshal(resp.Result, &initResult) == nil && initResult.ServerInfo.Name != "" {
		server.Name = initResult.ServerInfo.Name
	}
	if server.Name == "" {
		server.Name = url
	}

	server.send("notifications/initialized", map[string]interface{}{}, false)

	toolResp, err := server.send("tools/list", map[string]interface{}{}, true)
	if err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}

	var toolsResult struct {
		Tools []mcpTool `json:"tools"`
	}
	if err := json.Unmarshal(toolResp.Result, &toolsResult); err != nil {
		return nil, fmt.Errorf("parse tools: %w", err)
	}

	server.Tools = toolsResult.Tools
	return server, nil
}

func mcpCallTool(server *mcpServer, name string, arguments json.RawMessage) (string, error) {
	params := map[string]interface{}{
		"name":      name,
		"arguments": arguments,
	}

	resp, err := server.send("tools/call", params, true)
	if err != nil {
		return "", err
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return "", fmt.Errorf("parse result: %w", err)
	}

	var texts []string
	for _, c := range result.Content {
		if c.Type == "text" {
			texts = append(texts, c.Text)
		}
	}

	content := strings.Join(texts, "\n")
	if result.IsError {
		return "", fmt.Errorf("tool error: %s", content)
	}

	return content, nil
}
