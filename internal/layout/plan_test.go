package layout

import (
	"reflect"
	"testing"
)

func TestPlan(t *testing.T) {
	cases := []struct {
		name    string
		layout  string
		present []int
		want    []Launch
	}{
		{
			name:   "new tab with one pane",
			layout: layoutSingle,
			want:   []Launch{{Pane: 2, NextTo: NoPane}},
		},
		{
			name:   "new tab with a pair",
			layout: layoutPair,
			want: []Launch{
				{Pane: 0, NextTo: NoPane},
				{Pane: 1, NextTo: 0, Bias: 49},
			},
		},
		{
			// Every row gets its first window before any row is split, so
			// each split carves the right area.
			name:   "new tab with nested splits",
			layout: layoutNested,
			want: []Launch{
				{Pane: 5, NextTo: NoPane},
				{Pane: 6, NextTo: 5, Stacked: true, Bias: 46},
				{Pane: 7, NextTo: 5, Bias: 49},
				{Pane: 8, NextTo: 6, Bias: 49},
				{Pane: 9, NextTo: 8, Stacked: true, Bias: 45},
			},
		},
		{
			// The second row's window first takes the space of rows two and
			// three, then the third row is split off it.
			name:   "new tab with three rows",
			layout: layoutTiled,
			want: []Launch{
				{Pane: 5, NextTo: NoPane},
				{Pane: 6, NextTo: 5, Stacked: true, Bias: 67},
				{Pane: 9, NextTo: 6, Stacked: true, Bias: 50},
				{Pane: 7, NextTo: 5, Bias: 50},
				{Pane: 8, NextTo: 6, Bias: 50},
			},
		},
		{
			name:    "pane split off an existing one",
			layout:  layoutPair,
			present: []int{0},
			want:    []Launch{{Pane: 1, NextTo: 0, Bias: 49}},
		},
		{
			name:    "pane added before the existing one",
			layout:  layoutPair,
			present: []int{1},
			want:    []Launch{{Pane: 0, NextTo: 1, Bias: 50}},
		},
		{
			name:    "full-height pane beside a stack anchors on the stack's last window",
			layout:  layoutFull,
			present: []int{5, 7, 6, 8, 9},
			want:    []Launch{{Pane: 10, NextTo: 9, Bias: 49}},
		},
		{
			name:    "several panes missing in different rows",
			layout:  layoutNested,
			present: []int{5, 6},
			want: []Launch{
				{Pane: 7, NextTo: 5, Bias: 49},
				{Pane: 8, NextTo: 6, Bias: 49},
				{Pane: 9, NextTo: 8, Stacked: true, Bias: 45},
			},
		},
		{
			name:    "nothing missing",
			layout:  layoutNested,
			present: []int{5, 7, 6, 8, 9},
			want:    nil,
		},
		{
			name:    "windows of panes that left the layout are ignored",
			layout:  layoutSingle,
			present: []int{40, 41},
			want:    []Launch{{Pane: 2, NextTo: NoPane}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, err := Parse(tc.layout)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			present := map[int]bool{}
			for _, pane := range tc.present {
				present[pane] = true
			}
			got := Plan(root, present)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

// TestPlanAnchorsExist checks that every launch splits a window that exists
// by the time it runs.
func TestPlanAnchorsExist(t *testing.T) {
	for _, s := range []string{layoutSingle, layoutPair, layoutNested, layoutTiled, layoutFull} {
		root, err := Parse(s)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		have := map[int]bool{}
		for i, launch := range Plan(root, nil) {
			if i == 0 && launch.NextTo != NoPane {
				t.Fatalf("%s: first launch must open the tab, got %+v", s, launch)
			}
			if i > 0 && !have[launch.NextTo] {
				t.Fatalf("%s: launch %+v splits a window that does not exist yet", s, launch)
			}
			if have[launch.Pane] {
				t.Fatalf("%s: pane %d launched twice", s, launch.Pane)
			}
			have[launch.Pane] = true
		}
		if len(have) != len(root.Panes()) {
			t.Fatalf("%s: launched %d panes, layout has %d", s, len(have), len(root.Panes()))
		}
	}
}
