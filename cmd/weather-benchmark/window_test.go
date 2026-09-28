package main

import "testing"

func TestOwnedWindowGeometryCommandsArePIDScoped(t *testing.T) {
	float, err := geometryCommand("float", 123)
	if err != nil || float != `hl.dsp.window.float({window="pid:123", action='set'})` {
		t.Fatal(float, err)
	}
	resize, err := geometryCommand("resize", 123)
	if err != nil || resize != `hl.dsp.window.resize({window="pid:123", x=749, y=910})` {
		t.Fatal(resize, err)
	}
	move, err := geometryCommand("move", 123)
	if err != nil || move != `hl.dsp.window.move({window="pid:123", x=15, y=40, relative=false})` {
		t.Fatal(move, err)
	}
	for _, pid := range []int{-1, 0, 1, 2147483648} {
		for _, operation := range []string{"float", "resize", "move"} {
			if _, err := geometryCommand(operation, pid); err == nil {
				t.Fatalf("accepted unsafe PID %d for %s", pid, operation)
			}
		}
	}
	if _, err := geometryCommand("close", 123); err == nil {
		t.Fatal("accepted unrelated dispatcher")
	}
}

func TestWindowVisibilityAndGeometryMustMatch(t *testing.T) {
	visible := client{PID: 123, Mapped: true, Visible: true, Floating: true, Size: []int{749, 910}, At: []int{15, 40}}
	if !windowVisibilityMatches(visible, 1, 123, false) || windowVisibilityMatches(visible, 1, 123, true) {
		t.Fatal("visible owned window state was classified incorrectly")
	}
	for _, wrong := range []client{
		{PID: 124, Mapped: true, Visible: true, Floating: true, Size: []int{749, 910}, At: []int{15, 40}},
		{PID: 123, Mapped: false, Visible: false, Floating: true, Size: []int{749, 910}, At: []int{15, 40}},
		{PID: 123, Mapped: true, Visible: true, Hidden: true, Floating: true, Size: []int{749, 910}, At: []int{15, 40}},
		{PID: 123, Mapped: true, Visible: true, Floating: false, Size: []int{749, 910}, At: []int{15, 40}},
		{PID: 123, Mapped: true, Visible: true, Floating: true, Size: []int{749, 448}, At: []int{15, 40}},
		{PID: 123, Mapped: true, Visible: true, Floating: true, Size: []int{749}, At: []int{15, 40}},
		{PID: 123, Mapped: true, Visible: true, Floating: true, Size: []int{749, 910, 0}, At: []int{15, 40}},
		{PID: 123, Mapped: true, Visible: true, Floating: true, Size: []int{749, 910}},
		{PID: 123, Mapped: true, Visible: true, Floating: true, Size: []int{749, 910}, At: []int{15, 178}},
		{PID: 123, Mapped: true, Visible: true, Floating: true, Size: []int{749, 910}, At: []int{16, 40}},
		{PID: 123, Mapped: true, Visible: true, Floating: true, Size: []int{749, 910}, At: []int{15}},
		{PID: 123, Mapped: true, Visible: true, Floating: true, Size: []int{749, 910}, At: []int{15, 40, 0}},
	} {
		if windowVisibilityMatches(wrong, 1, 123, false) {
			t.Fatalf("accepted mismatched visible window: %+v", wrong)
		}
	}
	if !windowVisibilityMatches(client{}, 0, 123, true) {
		t.Fatal("unmapped window was not classified as hidden")
	}
	for _, wrong := range []client{
		{PID: 123, Mapped: true, Visible: true, Floating: true, Size: []int{749, 910}},
	} {
		if windowVisibilityMatches(wrong, 1, 123, true) {
			t.Fatalf("accepted visible window during hidden run: %+v", wrong)
		}
	}
	if windowVisibilityMatches(visible, 2, 123, true) {
		t.Fatal("accepted ambiguous duplicate PID windows during hidden run")
	}
	if windowVisibilityMatches(visible, 2, 123, false) {
		t.Fatal("accepted ambiguous duplicate PID windows during visible run")
	}
}
