package client

import (
	"context"
	"testing"

	"github.com/xibodev/gflow-cli/pkg/bridge"
	"github.com/xibodev/gflow-cli/pkg/config"
)

func testClient() *FlowClient {
	cfg := &config.Config{Host: "127.0.0.1", Port: 1, ProjectID: ""}
	return NewFlowClient(cfg, bridge.NewExtensionBridgeWithToken("t"))
}

func TestGenerateRequiresProject(t *testing.T) {
	c := testClient()
	if _, err := c.GenerateImages(context.Background(), "p", "square", 1, "", nil, nil); err == nil {
		t.Fatalf("missing project must fail before dispatch")
	}
	if _, err := c.GenerateVideo(context.Background(), "p", "landscape", 4, "", "", "", nil); err == nil {
		t.Fatalf("missing project must fail before dispatch")
	}
}

func TestGenerateValidation(t *testing.T) {
	cfg := &config.Config{Host: "127.0.0.1", Port: 1, ProjectID: "proj"}
	c := NewFlowClient(cfg, bridge.NewExtensionBridgeWithToken("t"))
	if _, err := c.GenerateVideo(context.Background(), "p", "landscape", 5, "", "", "", nil); err == nil {
		t.Fatalf("bad duration must fail")
	}
	if _, err := c.GenerateVideo(context.Background(), "p", "landscape", 4, "", "", "end", nil); err == nil {
		t.Fatalf("end without start must fail")
	}
	neg := int64(-1)
	if _, err := c.GenerateImages(context.Background(), "p", "square", 1, "", nil, &neg); err == nil {
		t.Fatalf("negative seed must fail")
	}
	if _, err := c.UpsampleVideo(context.Background(), "", "landscape", "1080p", nil); err == nil {
		t.Fatalf("empty media ID must fail")
	}
	if _, err := c.UpsampleVideo(context.Background(), "m", "landscape", "8k", nil); err == nil {
		t.Fatalf("unknown resolution must fail")
	}
	if _, err := c.CheckVideoStatus(context.Background(), nil); err == nil {
		t.Fatalf("empty IDs must fail")
	}
}
