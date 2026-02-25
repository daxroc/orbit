package scenario

import (
	"os"
	"path/filepath"
	"testing"
)

const testScenariosYAML = `scenarios:
  baseline:
    description: "Zero traffic"
    eastWest: []
    northSouth: []
  steady-low:
    description: "Low sustained load"
    eastWest:
      - type: tcp-stream
        bandwidthMbps: 10
        durationSeconds: 0
        payloadBytes: 1400
    northSouth:
      - type: http
        url: "http://external:8080"
        rps: 5
`

func TestEngine_LoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scenarios.yaml")
	if err := os.WriteFile(path, []byte(testScenariosYAML), 0644); err != nil {
		t.Fatal(err)
	}

	e := NewEngine()
	if err := e.LoadFromFile(path); err != nil {
		t.Fatalf("LoadFromFile failed: %v", err)
	}

	scenarios := e.List()
	if len(scenarios) != 2 {
		t.Fatalf("expected 2 scenarios, got %d", len(scenarios))
	}

	baseline, ok := e.Get("baseline")
	if !ok {
		t.Fatal("expected baseline scenario")
	}
	if baseline.Description != "Zero traffic" {
		t.Errorf("expected 'Zero traffic', got %q", baseline.Description)
	}
	if len(baseline.EastWest) != 0 {
		t.Errorf("expected 0 east-west flows, got %d", len(baseline.EastWest))
	}

	steady, ok := e.Get("steady-low")
	if !ok {
		t.Fatal("expected steady-low scenario")
	}
	if len(steady.EastWest) != 1 {
		t.Fatalf("expected 1 east-west flow, got %d", len(steady.EastWest))
	}
	if steady.EastWest[0].Type != "tcp-stream" {
		t.Errorf("expected tcp-stream, got %q", steady.EastWest[0].Type)
	}
	if steady.EastWest[0].BandwidthMbps != 10 {
		t.Errorf("expected bandwidth 10, got %d", steady.EastWest[0].BandwidthMbps)
	}
	if len(steady.NorthSouth) != 1 {
		t.Fatalf("expected 1 north-south flow, got %d", len(steady.NorthSouth))
	}
	if steady.NorthSouth[0].URL != "http://external:8080" {
		t.Errorf("expected URL 'http://external:8080', got %q", steady.NorthSouth[0].URL)
	}
}

func TestEngine_LoadFromFile_NotFound(t *testing.T) {
	e := NewEngine()
	err := e.LoadFromFile("/nonexistent/path/scenarios.yaml")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestEngine_LoadFromFile_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("{{{{invalid yaml"), 0644); err != nil {
		t.Fatal(err)
	}

	e := NewEngine()
	if err := e.LoadFromFile(path); err == nil {
		t.Error("expected error for invalid yaml")
	}
}

func TestEngine_GetMissing(t *testing.T) {
	e := NewEngine()
	_, ok := e.Get("nonexistent")
	if ok {
		t.Error("expected Get to return false for missing scenario")
	}
}

func TestEngine_SetActive_Active_Clear(t *testing.T) {
	e := NewEngine()

	name, runID := e.Active()
	if name != "" || runID != "" {
		t.Errorf("expected empty active, got %q/%q", name, runID)
	}

	e.SetActive("test-scenario", "run-123")
	name, runID = e.Active()
	if name != "test-scenario" {
		t.Errorf("expected 'test-scenario', got %q", name)
	}
	if runID != "run-123" {
		t.Errorf("expected 'run-123', got %q", runID)
	}

	e.Clear()
	name, runID = e.Active()
	if name != "" || runID != "" {
		t.Errorf("expected empty after clear, got %q/%q", name, runID)
	}
}

func TestEngine_List_IsCopy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scenarios.yaml")
	if err := os.WriteFile(path, []byte(testScenariosYAML), 0644); err != nil {
		t.Fatal(err)
	}

	e := NewEngine()
	_ = e.LoadFromFile(path)

	list := e.List()
	delete(list, "baseline")

	_, ok := e.Get("baseline")
	if !ok {
		t.Error("deleting from List result should not affect engine")
	}
}
