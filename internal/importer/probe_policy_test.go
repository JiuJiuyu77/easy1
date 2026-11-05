package importer

import (
	"context"
	"errors"
	"testing"
)

func TestEvaluateProbePolicy(t *testing.T) {
	ctx := context.Background()
	targets := []string{"a", "b", "c"}
	failing := map[string]bool{"a": true, "b": true}

	probe := func(_ context.Context, target string) error {
		if failing[target] {
			return errors.New("boom")
		}
		return nil
	}

	// any: the single healthy target is enough.
	if err := evaluateProbePolicy(targets, "any", probe, ctx); err != nil {
		t.Fatalf("any policy should pass with one healthy target: %v", err)
	}
	// all: one failing target fails the node.
	if err := evaluateProbePolicy(targets, "all", probe, ctx); err == nil {
		t.Fatal("all policy should fail when a target fails")
	}
	// all: every target answering passes.
	allHealthy := func(context.Context, string) error { return nil }
	if err := evaluateProbePolicy(targets, "all", allHealthy, ctx); err != nil {
		t.Fatalf("all policy should pass when every target answers: %v", err)
	}
	// any: no target answering reports all failures.
	allBad := func(context.Context, string) error { return errors.New("boom") }
	if err := evaluateProbePolicy(targets, "any", allBad, ctx); err == nil {
		t.Fatal("any policy should fail when every target fails")
	}
	// A single legacy target keeps working.
	if err := evaluateProbePolicy([]string{"only"}, "any", allHealthy, ctx); err != nil {
		t.Fatalf("single target: %v", err)
	}
	if err := evaluateProbePolicy(nil, "any", allHealthy, ctx); err == nil {
		t.Fatal("empty target list must fail")
	}
}
