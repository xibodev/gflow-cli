package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/xibodev/gflow-cli/pkg/client"
	"github.com/xibodev/gflow-cli/pkg/history"
	"github.com/xibodev/gflow-cli/pkg/util"
)

// Request represents a JSON-RPC 2.0 request.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response represents a JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id"`
	Result  any    `json:"result,omitempty"`
	Error   *Error `json:"error,omitempty"`
}

// Error represents a JSON-RPC error.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Server implements an MCP stdio server.
type Server struct {
	flowClient *client.FlowClient
}

// NewServer creates a new MCP server.
func NewServer(fc *client.FlowClient) *Server {
	return &Server{flowClient: fc}
}

// Run starts the JSON-RPC stdio loop.
func (s *Server) Run() error {
	reader := bufio.NewReader(os.Stdin)

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		if len(line) == 0 || line[0] == '\n' || line[0] == '\r' {
			continue
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}

		resp := s.handleRequest(&req)
		if resp != nil {
			outBytes, _ := json.Marshal(resp)
			fmt.Printf("%s\n", outBytes)
		}
	}
}

func (s *Server) handleRequest(req *Request) *Response {
	switch req.Method {
	case "initialize":
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities": map[string]any{
					"tools": map[string]any{},
				},
				"serverInfo": map[string]any{
					"name":    "gflow",
					"version": "1.0.0",
				},
			},
		}

	case "notifications/initialized":
		return nil

	case "ping":
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]any{},
		}

	case "tools/list":
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"tools": s.getToolsList(),
			},
		}

	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return &Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &Error{Code: -32602, Message: "Invalid arguments"},
			}
		}

		result, err := s.executeTool(params.Name, params.Arguments)
		if err != nil {
			return &Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"isError": true,
					"content": []map[string]any{
						{"type": "text", "text": fmt.Sprintf("Error: %v", err)},
					},
				},
			}
		}

		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": result},
				},
			},
		}

	default:
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &Error{Code: -32601, Message: fmt.Sprintf("Method not found: %s", req.Method)},
		}
	}
}

func (s *Server) getToolsList() []map[string]any {
	return []map[string]any{
		{
			"name":        "generate_flow_image",
			"description": "Generate high-quality AI images using Google Flow (Imagen 4 / Nano Banana 2).",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt": map[string]any{
						"type":        "string",
						"description": "The detailed prompt describing the image.",
					},
					"aspect": map[string]any{
						"type":        "string",
						"description": "Aspect ratio: landscape (16:9), square (1:1), portrait (9:16), 4:3, 3:4.",
						"default":     "landscape",
					},
					"model": map[string]any{
						"type":        "string",
						"description": "Model to use: narwhal (standard Imagen 4), harbor_seal (lite), gem_pix_2 (pro).",
						"default":     "narwhal",
					},
					"count": map[string]any{
						"type":        "integer",
						"description": "Number of image variations (1-4).",
						"default":     1,
					},
				},
				"required": []string{"prompt"},
			},
		},
		{
			"name":        "generate_flow_video",
			"description": "Generate high-definition AI videos using Google Flow (Veo 3.1).",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt": map[string]any{
						"type":        "string",
						"description": "The prompt describing video scene and motion.",
					},
					"duration": map[string]any{
						"type":        "integer",
						"description": "Duration in seconds: 4, 6, 8, or 10.",
						"default":     10,
					},
					"aspect": map[string]any{
						"type":        "string",
						"description": "Aspect ratio: landscape, portrait, square.",
						"default":     "landscape",
					},
					"resolution": map[string]any{
						"type":        "string",
						"description": "Video resolution: 720p (native), 1080p (upsampled), 4k.",
						"default":     "720p",
					},
				},
				"required": []string{"prompt"},
			},
		},
		{
			"name":        "get_flow_status",
			"description": "Check connection status with Chrome extension and Google Flow session.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			"name":        "get_flow_history",
			"description": "List recently generated images and videos.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"limit": map[string]any{
						"type":        "integer",
						"description": "Number of entries to retrieve (default: 10).",
						"default":     10,
					},
				},
			},
		},
	}
}

func (s *Server) executeTool(name string, args map[string]any) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	switch name {
	case "get_flow_status":
		br := s.flowClient.Bridge()
		return fmt.Sprintf("Extension Connected: %v\nHas Flow Token: %v\nActive Sessions: %d",
			br.IsConnected(), br.HasFlowKey(), br.ActiveSessionCount()), nil

	case "get_flow_history":
		limit := 10
		if l, ok := args["limit"].(float64); ok && l > 0 {
			limit = int(l)
		}
		entries, err := history.List(limit)
		if err != nil {
			return "", err
		}
		data, _ := json.MarshalIndent(entries, "", "  ")
		return string(data), nil

	case "generate_flow_image":
		prompt, _ := args["prompt"].(string)
		aspect := "landscape"
		if a, ok := args["aspect"].(string); ok && a != "" {
			aspect = a
		}
		model := "narwhal"
		if m, ok := args["model"].(string); ok && m != "" {
			model = m
		}
		count := 1
		if c, ok := args["count"].(float64); ok && c > 0 {
			count = int(c)
		}

		assets, err := s.flowClient.GenerateImages(ctx, prompt, aspect, count, model, nil, 0)
		if err != nil {
			return "", err
		}

		outDir := s.flowClient.Config().OutputDir
		var savedList []string
		for i := range assets {
			a := &assets[i]
			saved, err := util.SaveAsset(ctx, a, outDir)
			if err == nil {
				a.LocalPath = saved
				savedList = append(savedList, saved)
				_ = history.Add(history.Entry{
					ID:        a.ID,
					Type:      "image",
					Prompt:    prompt,
					LocalPath: saved,
					URL:       a.URL,
					Aspect:    aspect,
					Model:     model,
				})
			}
		}

		return fmt.Sprintf("Successfully generated %d image(s):\n%s", len(savedList), formatBulletList(savedList)), nil

	case "generate_flow_video":
		prompt, _ := args["prompt"].(string)
		duration := 10
		if d, ok := args["duration"].(float64); ok && d > 0 {
			duration = int(d)
		}
		aspect := "landscape"
		if a, ok := args["aspect"].(string); ok && a != "" {
			aspect = a
		}
		res := "720p"
		if r, ok := args["resolution"].(string); ok && r != "" {
			res = r
		}

		mediaIDs, err := s.flowClient.GenerateVideo(ctx, prompt, aspect, duration, "", "", "", 0)
		if err != nil {
			return "", err
		}

		assets, err := s.flowClient.WaitForVideo(ctx, mediaIDs, 12*time.Minute)
		if err != nil {
			return "", err
		}

		if res == "1080p" || res == "4k" {
			upIDs, err := s.flowClient.UpsampleVideo(ctx, assets[0].ID, aspect, res, 0)
			if err == nil && len(upIDs) > 0 {
				upAssets, err := s.flowClient.WaitForVideo(ctx, upIDs, 10*time.Minute)
				if err == nil && len(upAssets) > 0 {
					assets = upAssets
				}
			}
		}

		outDir := s.flowClient.Config().OutputDir
		var savedList []string
		for i := range assets {
			a := &assets[i]
			saved, err := util.SaveAsset(ctx, a, outDir)
			if err == nil {
				a.LocalPath = saved
				savedList = append(savedList, saved)
				_ = history.Add(history.Entry{
					ID:        a.ID,
					Type:      "video",
					Prompt:    prompt,
					LocalPath: saved,
					URL:       a.URL,
					Aspect:    aspect,
				})
			}
		}

		return fmt.Sprintf("Successfully generated video:\n%s", formatBulletList(savedList)), nil

	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

func formatBulletList(items []string) string {
	res := ""
	for _, it := range items {
		res += fmt.Sprintf("- %s\n", it)
	}
	return res
}
