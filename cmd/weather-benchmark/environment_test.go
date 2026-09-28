package main

import (
	"reflect"
	"testing"
)

func TestEnvironmentWithStateDirReplacesInheritedValue(t *testing.T) {
	input := []string{"PATH=/bin", "A_WEATHER_APP_STATE_DIR=/real/user/data", "HOME=/home/test"}
	got := environmentWithStateDir(input, "/tmp/isolated-state")
	want := []string{"PATH=/bin", "HOME=/home/test", "A_WEATHER_APP_STATE_DIR=/tmp/isolated-state"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected child environment: %#v", got)
	}
	if len(input) != 3 || input[1] != "A_WEATHER_APP_STATE_DIR=/real/user/data" {
		t.Fatal("input environment was mutated")
	}
}
