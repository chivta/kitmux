package tabs

import "sort"

// Tab is one tab of the kitty OS window, as the tab sorter sees it.
type Tab struct {
	// ID is kitty's tab id.
	ID int
	// Managed is true for a tab kitmux created for a tmux window.
	Managed bool
	// Target is the position of the tab's target on the command line.
	Target int
	// Index is the tmux window index.
	Index int
}

// Move asks for one tab to be moved toward the front of the tab bar.
type Move struct {
	// ID is the tab to move.
	ID int
	// Steps is how many positions it moves backward.
	Steps int
}

// Desired returns the tab ids in the order the tab bar should have. Managed
// tabs are sorted by target (command-line order) and then by tmux window
// index. Tabs kitmux did not create keep their positions; the managed tabs
// fill the remaining slots.
func Desired(current []Tab) []int {
	managed := make([]Tab, 0, len(current))
	for _, tab := range current {
		if tab.Managed {
			managed = append(managed, tab)
		}
	}
	sort.SliceStable(managed, func(i, j int) bool {
		if managed[i].Target != managed[j].Target {
			return managed[i].Target < managed[j].Target
		}
		return managed[i].Index < managed[j].Index
	})
	order := make([]int, len(current))
	next := 0
	for i, tab := range current {
		if !tab.Managed {
			order[i] = tab.ID
			continue
		}
		order[i] = managed[next].ID
		next++
	}
	return order
}

// Moves returns the moves that turn the current tab order into the desired
// one, to be applied in sequence. Each move takes one tab backward past its
// neighbours, which is what kitty's move_tab_backward does; a tab that is
// passed shifts forward by one, and by the last move every tab that is not
// kitmux's is back in its slot.
func Moves(current []Tab) []Move {
	order := make([]int, len(current))
	for i, tab := range current {
		order[i] = tab.ID
	}
	var moves []Move
	for pos, id := range Desired(current) {
		at := pos
		for order[at] != id {
			at++
		}
		if at == pos {
			continue
		}
		moves = append(moves, Move{ID: id, Steps: at - pos})
		copy(order[pos+1:at+1], order[pos:at])
		order[pos] = id
	}
	return moves
}
