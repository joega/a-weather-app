package main

import (
	"errors"
	"io"
	"testing"
)

func TestBoundedTaskNamesRefusesPartialListingError(t *testing.T) {
	partial := errors.New("injected directory read failure")
	if _, err := boundedTaskNames([]string{"1234"}, partial); !errors.Is(err, partial) {
		t.Fatalf("partial child listing accepted: %v", err)
	}
	rows, err := boundedTaskNames([]string{"1234"}, io.EOF)
	if err != nil || len(rows) != 1 {
		t.Fatalf("EOF partial page should remain usable: %v %v", rows, err)
	}
	if _, err = boundedTaskNames(make([]string, 257), nil); err == nil {
		t.Fatal("oversized task listing accepted")
	}
}
