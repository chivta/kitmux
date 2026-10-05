package tabs

import (
	"reflect"
	"testing"
)

// managed builds a kitmux tab.
func managed(id, target, index int) Tab {
	return Tab{ID: id, Managed: true, Target: target, Index: index}
}

// foreign builds a tab kitmux did not create.
func foreign(id int) Tab {
	return Tab{ID: id}
}

func TestDesired(t *testing.T) {
	cases := []struct {
		name    string
		current []Tab
		want    []int
	}{
		{"empty", nil, []int{}},
		{"already sorted", []Tab{managed(1, 0, 0), managed(2, 0, 1), managed(3, 1, 0)}, []int{1, 2, 3}},
		{"by index within a target", []Tab{managed(1, 0, 5), managed(2, 0, 1), managed(3, 0, 3)}, []int{2, 3, 1}},
		{"targets in command-line order", []Tab{managed(1, 1, 0), managed(2, 0, 7), managed(3, 1, 2), managed(4, 0, 2)}, []int{4, 2, 1, 3}},
		{"foreign tabs keep their slots", []Tab{managed(1, 0, 2), foreign(9), managed(2, 0, 1), foreign(8), managed(3, 0, 0)}, []int{3, 9, 2, 8, 1}},
		{"only foreign", []Tab{foreign(4), foreign(2)}, []int{4, 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Desired(tc.current)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Desired = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMoves(t *testing.T) {
	cases := []struct {
		name    string
		current []Tab
		want    []Move
	}{
		{"sorted needs no moves", []Tab{managed(1, 0, 0), managed(2, 0, 1)}, nil},
		{"new window of the first target appears last", []Tab{managed(1, 0, 0), managed(2, 1, 0), managed(3, 1, 1), managed(4, 0, 1)}, []Move{{ID: 4, Steps: 2}}},
		{"swapped neighbours", []Tab{managed(1, 0, 1), managed(2, 0, 0)}, []Move{{ID: 2, Steps: 1}}},
		{"reversed", []Tab{managed(1, 0, 2), managed(2, 0, 1), managed(3, 0, 0)}, []Move{{ID: 3, Steps: 2}, {ID: 2, Steps: 1}}},
		{"across a foreign tab", []Tab{managed(1, 0, 1), foreign(9), managed(2, 0, 0)}, []Move{{ID: 2, Steps: 2}, {ID: 9, Steps: 1}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Moves(tc.current)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Moves = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestMovesReachDesired replays the moves the way kitty applies them and
// checks the result.
func TestMovesReachDesired(t *testing.T) {
	layouts := [][]Tab{
		{managed(1, 1, 3), foreign(7), managed(2, 0, 9), managed(3, 1, 0), foreign(8), managed(4, 0, 1), managed(5, 2, 0)},
		{foreign(1), managed(2, 0, 2), managed(3, 0, 1), managed(4, 0, 0), foreign(5)},
		{managed(3, 2, 0), managed(2, 1, 0), managed(1, 0, 0)},
	}
	for i, current := range layouts {
		order := make([]int, len(current))
		for j, tab := range current {
			order[j] = tab.ID
		}
		for _, move := range Moves(current) {
			at := 0
			for order[at] != move.ID {
				at++
			}
			for step := 0; step < move.Steps; step++ {
				order[at], order[at-1] = order[at-1], order[at]
				at--
			}
		}
		want := Desired(current)
		if !reflect.DeepEqual(order, want) {
			t.Fatalf("layout %d: moves give %v, want %v", i, order, want)
		}
	}
}
