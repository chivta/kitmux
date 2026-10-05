package layout

import (
	"errors"
	"reflect"
	"testing"
)

// Layout strings below were printed by tmux 3.7c (#{window_layout}).
const (
	layoutSingle = "b25f,80x24,0,0,2"
	layoutPair   = "f91d,120x40,0,0{60x40,0,0,0,59x40,61,0,1}"
	layoutNested = "5857,80x24,0,0[80x12,0,0{40x12,0,0,5,39x12,41,0,7},80x11,0,13{40x11,0,13,6,39x11,41,13[39x5,41,13,8,39x5,41,19,9]}]"
	layoutTiled  = "0d90,80x24,0,0[80x7,0,0{39x7,0,0,5,40x7,40,0,7},80x7,0,8{39x7,0,8,6,40x7,40,8,8},80x8,0,16,9]"
	layoutFull   = "8bac,80x24,0,0{40x24,0,0[40x7,0,0{19x7,0,0,5,20x7,20,0,7},40x7,0,8{19x7,0,8,6,20x7,20,8,8},40x8,0,16,9],39x24,41,0,10}"
)

func TestParseFormatRoundTrip(t *testing.T) {
	for _, s := range []string{layoutSingle, layoutPair, layoutNested, layoutTiled, layoutFull} {
		node, err := Parse(s)
		if err != nil {
			t.Fatalf("Parse(%q): %v", s, err)
		}
		got := Format(node)
		if got != s {
			t.Errorf("Format(Parse(%q)) = %q", s, got)
		}
	}
}

func TestParseWithoutChecksum(t *testing.T) {
	node, err := Parse("120x40,0,0{60x40,0,0,0,59x40,61,0,1}")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if Format(node) != layoutPair {
		t.Fatalf("Format = %q, want %q", Format(node), layoutPair)
	}
}

func TestParseStructure(t *testing.T) {
	node, err := Parse(layoutNested)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if node.Kind != TopBottom || node.W != 80 || node.H != 24 || len(node.Children) != 2 {
		t.Fatalf("root = %+v", node)
	}
	bottom := node.Children[1]
	if bottom.Kind != LeftRight || bottom.Y != 13 || bottom.H != 11 {
		t.Fatalf("bottom row = %+v", bottom)
	}
	stack := bottom.Children[1]
	if stack.Kind != TopBottom || stack.X != 41 || len(stack.Children) != 2 {
		t.Fatalf("stack = %+v", stack)
	}
	last := stack.Children[1]
	if last.Kind != Leaf || last.Pane != 9 || last.W != 39 || last.H != 5 || last.X != 41 || last.Y != 19 {
		t.Fatalf("last leaf = %+v", last)
	}
	wantPanes := []int{5, 7, 6, 8, 9}
	if !reflect.DeepEqual(node.Panes(), wantPanes) {
		t.Fatalf("Panes() = %v, want %v", node.Panes(), wantPanes)
	}
	if node.Leaf(6) == nil || node.Leaf(6).W != 40 {
		t.Fatalf("Leaf(6) = %+v", node.Leaf(6))
	}
	if node.Leaf(42) != nil {
		t.Fatal("Leaf(42) should be nil")
	}
}

func TestParseLeafWithoutPane(t *testing.T) {
	node, err := Parse("10x5,0,0{4x5,0,0,5x5,5,0}")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if node.Children[0].Pane != NoPane || node.Children[1].Pane != NoPane {
		t.Fatalf("children = %+v %+v", node.Children[0], node.Children[1])
	}
	if node.String() != "10x5,0,0{4x5,0,0,5x5,5,0}" {
		t.Fatalf("String() = %q", node.String())
	}
}

func TestParseErrors(t *testing.T) {
	for _, s := range []string{
		"",
		"garbage",
		"80x24",
		"80x24,0",
		"80x24,0,0{40x24,0,0,1",
		"80x24,0,0{40x24,0,0,1]",
		"80x24,0,0,1,extra",
		"x24,0,0,1",
	} {
		_, err := Parse(s)
		if !errors.Is(err, ErrInvalidLayout) {
			t.Errorf("Parse(%q): err = %v, want ErrInvalidLayout", s, err)
		}
	}
}

func TestChecksum(t *testing.T) {
	cases := []struct {
		body string
		want uint16
	}{
		{"80x24,0,0,2", 0xb25f},
		{"120x40,0,0,0", 0xaafd},
		{"131x35,0,0{100x35,0,0,0,30x35,101,0,1}", 0x387f},
		{"80x24,0,0[80x7,0,0{39x7,0,0,5,40x7,40,0,7},80x7,0,8{39x7,0,8,6,40x7,40,8,8},80x8,0,16,9]", 0x0d90},
	}
	for _, tc := range cases {
		got := Checksum(tc.body)
		if got != tc.want {
			t.Errorf("Checksum(%q) = %04x, want %04x", tc.body, got, tc.want)
		}
	}
}
