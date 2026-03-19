package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"crdt-agent-memory/internal/config"
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Result  any    `json:"result,omitempty"`
	Error   any    `json:"error,omitempty"`
}

type apiEnvelope struct {
	OK        bool            `json:"ok"`
	Data      json.RawMessage `json:"data,omitempty"`
	Error     *apiError       `json:"error,omitempty"`
	Warnings  []string        `json:"warnings"`
	RequestID string          `json:"request_id"`
}

type apiError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	Details   any    `json:"details,omitempty"`
}

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "", "path to config yaml")
	flag.Parse()
	if configPath == "" {
		log.Fatal("--config is required")
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatal(err)
	}
	reader := bufio.NewReader(os.Stdin)
	for {
		req, err := readMessage(reader)
		if err != nil {
			if err == io.EOF {
				return
			}
			log.Fatal(err)
		}
		var rpcReq rpcRequest
		if err := json.Unmarshal(req, &rpcReq); err != nil {
			writeMessage(rpcResponse{JSONRPC: "2.0", Error: map[string]any{"code": -32700, "message": err.Error()}})
			continue
		}
		resp := handle(cfg, rpcReq)
		if rpcReq.ID != nil {
			writeMessage(resp)
		}
	}
}

func handle(cfg config.Config, req rpcRequest) rpcResponse {
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": "2024-11-05",
			"serverInfo": map[string]any{
				"name":    "memory-mcp",
				"version": "0.1.0",
			},
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
		}
	case "notifications/initialized":
		return rpcResponse{}
	case "tools/list":
		resp.Result = map[string]any{
			"tools": []map[string]any{
				{
					"name":        "memory.sync_status",
					"description": "return local sync health without mutating state",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"namespace": map[string]any{"type": "string"},
						},
						"required": []string{"namespace"},
					},
				},
			},
		}
	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			resp.Error = map[string]any{"code": -32602, "message": err.Error()}
			return resp
		}
		if params.Name != "memory.sync_status" {
			resp.Error = map[string]any{"code": -32601, "message": "unknown tool"}
			return resp
		}
		namespace, _ := params.Arguments["namespace"].(string)
		if strings.TrimSpace(namespace) == "" {
			resp.Error = map[string]any{"code": -32602, "message": "namespace is required"}
			return resp
		}
		httpResp, err := http.Get(strings.TrimRight(cfg.API.BaseURL, "/") + "/v1/sync/status?namespace=" + namespace)
		if err != nil {
			resp.Error = map[string]any{"code": -32000, "message": err.Error()}
			return resp
		}
		defer httpResp.Body.Close()
		var payload apiEnvelope
		if err := json.NewDecoder(httpResp.Body).Decode(&payload); err != nil {
			resp.Error = map[string]any{"code": -32000, "message": err.Error()}
			return resp
		}
		if httpResp.StatusCode >= http.StatusBadRequest || !payload.OK {
			message := "request failed"
			if payload.Error != nil && strings.TrimSpace(payload.Error.Message) != "" {
				message = payload.Error.Message
			}
			resp.Error = map[string]any{"code": -32000, "message": message}
			return resp
		}
		var data map[string]any
		if len(payload.Data) > 0 {
			if err := json.Unmarshal(payload.Data, &data); err != nil {
				resp.Error = map[string]any{"code": -32000, "message": err.Error()}
				return resp
			}
		} else {
			data = map[string]any{}
		}
		text, _ := json.Marshal(data)
		resp.Result = map[string]any{
			"structuredContent": map[string]any{
				"ok":         true,
				"data":       data,
				"warnings":   payload.Warnings,
				"request_id": payload.RequestID,
			},
			"content": []map[string]any{
				{"type": "text", "text": string(text)},
			},
		}
	default:
		resp.Error = map[string]any{"code": -32601, "message": "method not found"}
	}
	return resp
}

func readMessage(r *bufio.Reader) ([]byte, error) {
	length := 0
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			value := strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:"))
			n, err := strconv.Atoi(value)
			if err != nil {
				return nil, err
			}
			length = n
		}
	}
	if length <= 0 {
		return nil, io.EOF
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}

func writeMessage(resp rpcResponse) {
	if resp.JSONRPC == "" && resp.Result == nil && resp.Error == nil {
		return
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		log.Fatal(err)
	}
	var buf bytes.Buffer
	_, _ = fmt.Fprintf(&buf, "Content-Length: %d\r\n\r\n", len(raw))
	_, _ = buf.Write(raw)
	_, _ = os.Stdout.Write(buf.Bytes())
}
