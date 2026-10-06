package daemon

import (
	"fmt"
	"reflect"
	"testing"
)

func TestParseWindows(t *testing.T) {
	lines := [][]byte{
		[]byte("@3 4 e3fa,150x44,0,0{76x44,0,0,5,73x44,77,0,6} my editor"),
		[]byte("@0 0 bbc0,150x44,0,0,3 zsh"),
		[]byte("@7 2 bbc1,150x44,0,0,4"),           // empty name
		[]byte("@8 x bbc1,150x44,0,0,4 bad index"), // skipped
		[]byte("@9 5 not-a-layout name"),           // skipped
		[]byte("garbage"),                          // skipped
	}
	windows := parseWindows(lines)
	var got []string
	for _, w := range windows {
		got = append(got, fmt.Sprintf("%s|%s|%d", w.id, w.name, len(w.tree.Panes())))
	}
	want := []string{"@0|zsh|1", "@7||1", "@3|my editor|2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	if windows[2].index != 4 || windows[2].layout != "e3fa,150x44,0,0{76x44,0,0,5,73x44,77,0,6}" {
		t.Fatalf("window = %+v", windows[2])
	}
}

func TestSwapCommands(t *testing.T) {
	cases := []struct {
		current, want []int
		cmds          []string
	}{
		{[]int{1, 2, 3}, []int{1, 2, 3}, nil},
		{[]int{1, 2}, []int{2, 1}, []string{"swap-pane -d -s %2 -t %1"}},
		{[]int{5, 9, 8, 7}, []int{9, 5, 8, 7}, []string{"swap-pane -d -s %9 -t %5"}},
		{[]int{1, 2, 3}, []int{3, 1, 2}, []string{"swap-pane -d -s %3 -t %1", "swap-pane -d -s %1 -t %2"}},
	}
	for _, tc := range cases {
		got := swapCommands(tc.current, tc.want)
		if !reflect.DeepEqual(got, tc.cmds) {
			t.Errorf("swapCommands(%v, %v) = %q, want %q", tc.current, tc.want, got, tc.cmds)
		}
	}
}

func TestSamePanes(t *testing.T) {
	if !samePanes([]int{1, 2, 3}, []int{3, 1, 2}) {
		t.Fatal("same panes in another order")
	}
	if samePanes([]int{1, 2}, []int{1, 2, 3}) || samePanes([]int{1, 2}, []int{1, 4}) {
		t.Fatal("different panes")
	}
}

func TestParsePaneID(t *testing.T) {
	for id, want := range map[string]int{"%0": 0, "%12": 12} {
		got, ok := parsePaneID(id)
		if !ok || got != want {
			t.Errorf("parsePaneID(%q) = %d, %v", id, got, ok)
		}
	}
	for _, id := range []string{"", "%", "12", "%-1", "%x", "@1"} {
		_, ok := parsePaneID(id)
		if ok {
			t.Errorf("parsePaneID(%q) should fail", id)
		}
	}
}
