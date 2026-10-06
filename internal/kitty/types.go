// Package kitty drives the kitty instance kitmux starts: it launches the
// process, writes the overlay configuration with kitmux's key bindings, and
// talks to kitty over its remote-control socket.
package kitty

import (
	"encoding/json"
	"fmt"
)

const (
	// VarTarget, VarWindow and VarPane are the user variables every kitmux
	// window carries: the target name, the tmux window id ("@1") and the
	// tmux pane id ("%1"). kitmux recognises its windows by them.
	VarTarget = "kitmux_target"
	VarWindow = "kitmux_window"
	VarPane   = "kitmux_pane"

	// LayoutSplits is the kitty layout kitmux tabs use.
	LayoutSplits = "splits"
)

// OSWindow is one kitty OS window from `kitty @ ls`.
type OSWindow struct {
	ID        int   `json:"id"`
	IsFocused bool  `json:"is_focused"`
	Tabs      []Tab `json:"tabs"`
}

// Tab is one kitty tab from `kitty @ ls`.
type Tab struct {
	ID          int         `json:"id"`
	Title       string      `json:"title"`
	IsActive    bool        `json:"is_active"`
	Layout      string      `json:"layout"`
	LayoutState LayoutState `json:"layout_state"`
	Windows     []Window    `json:"windows"`
	Groups      []Group     `json:"groups"`
}

// LayoutState is the part of a tab's layout state kitmux reads: the split
// tree of the splits layout.
type LayoutState struct {
	Pairs *Pair `json:"pairs"`
}

// Group is a kitty window group: a window and the overlays stacked on it. The
// split tree refers to groups, not windows.
type Group struct {
	ID      int   `json:"id"`
	Windows []int `json:"windows"`
}

// Window is one kitty window from `kitty @ ls`.
type Window struct {
	ID        int               `json:"id"`
	Columns   int               `json:"columns"`
	Lines     int               `json:"lines"`
	IsFocused bool              `json:"is_focused"`
	IsActive  bool              `json:"is_active"`
	UserVars  map[string]string `json:"user_vars"`
}

// Pair is a node of the splits layout tree. One and Two are each either a
// window group or a nested pair, or nil when the slot is empty.
type Pair struct {
	// Horizontal is true when One is left of Two, false when it is above.
	Horizontal bool
	One        *PairChild
	Two        *PairChild
}

// PairChild is one side of a Pair: a nested Pair when Pair is set, otherwise
// the window group Group.
type PairChild struct {
	Group int
	Pair  *Pair
}

// pairJSON is the wire form of a Pair. kitty leaves out "horizontal" when it
// is true and "one"/"two" when the slot is empty.
type pairJSON struct {
	Horizontal *bool           `json:"horizontal"`
	One        json.RawMessage `json:"one"`
	Two        json.RawMessage `json:"two"`
}

// UnmarshalJSON decodes kitty's serialized pair.
func (p *Pair) UnmarshalJSON(data []byte) error {
	var wire pairJSON
	err := json.Unmarshal(data, &wire)
	if err != nil {
		return fmt.Errorf("decode split pair: %w", err)
	}
	p.Horizontal = wire.Horizontal == nil || *wire.Horizontal
	p.One, err = decodeChild(wire.One)
	if err != nil {
		return err
	}
	p.Two, err = decodeChild(wire.Two)
	return err
}

// decodeChild decodes one side of a pair: a group id or a nested pair.
func decodeChild(data json.RawMessage) (*PairChild, error) {
	if len(data) == 0 || string(data) == "null" {
		return nil, nil
	}
	if data[0] == '{' {
		nested := &Pair{}
		err := json.Unmarshal(data, nested)
		if err != nil {
			return nil, err
		}
		return &PairChild{Pair: nested}, nil
	}
	var group int
	err := json.Unmarshal(data, &group)
	if err != nil {
		return nil, fmt.Errorf("decode split child: %w", err)
	}
	return &PairChild{Group: group}, nil
}
