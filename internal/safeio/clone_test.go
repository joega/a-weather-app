package safeio

import (
	"math"
	"reflect"
	"testing"
)

func TestCloneCheckedRejectsInvalidTrees(t *testing.T) {
	cycle := map[string]any{}
	cycle["self"] = cycle
	deep := map[string]any{}
	for i := 0; i < 18; i++ {
		deep = map[string]any{"child": deep}
	}
	for name, value := range map[string]map[string]any{
		"channel": {"value": make(chan int)},
		"nan":     {"value": math.NaN()},
		"cycle":   cycle,
		"depth":   deep,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := CloneChecked(value); err == nil {
				t.Fatal("CloneChecked accepted an invalid JSON tree")
			}
		})
	}
}

func TestCloneCheckedPreservesRepresentationAndIsolation(t *testing.T) {
	if value, err := CloneChecked(nil); value != nil || err != nil {
		t.Fatalf("CloneChecked(nil) = %v, %v", value, err)
	}
	original := map[string]any{"number": 1, "empty": []any{}, "null": nil,
		"items": []any{map[string]any{"name": "original"}}}
	copy, err := CloneChecked(original)
	if err != nil {
		t.Fatal(err)
	}
	if copy["number"] != float64(1) || !reflect.DeepEqual(copy["empty"], []any{}) || copy["null"] != nil {
		t.Fatalf("JSON representation changed: %#v", copy)
	}
	copy["items"].([]any)[0].(map[string]any)["name"] = "changed"
	if original["items"].([]any)[0].(map[string]any)["name"] != "original" {
		t.Fatal("clone shares nested mutable state")
	}
}
