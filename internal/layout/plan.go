package layout

const (
	// Bias is a percentage; these bounds keep a new window from taking
	// nothing or everything.
	minBiasPercent = 1
	maxBiasPercent = 99
	percent        = 100
)

// Launch describes one kitty window to open for a pane.
type Launch struct {
	// Pane is the pane the new window shows.
	Pane int
	// NextTo is the pane whose window is split to make room. NoPane means
	// the window opens in a new tab.
	NextTo int
	// Stacked puts the new window below NextTo (kitty's hsplit) instead of
	// to its right (vsplit).
	Stacked bool
	// Bias is the percentage of NextTo's space the new window takes.
	Bias int
}

// Plan lists the kitty windows to open, in order, so that every pane of the
// tmux layout root has one. present holds the panes that already have a
// window. Each launch splits a window that exists by then, so running the
// launches in order rebuilds the tmux arrangement: a new tab ends up with the
// same tree and proportions, and a pane added to an existing tab opens next to
// its neighbour in the layout.
func Plan(root *Node, present map[int]bool) []Launch {
	p := &planner{have: map[int]bool{}}
	for _, pane := range root.Panes() {
		if present[pane] {
			p.have[pane] = true
		}
	}
	if len(p.have) == 0 {
		first := firstLeaf(root)
		p.launches = append(p.launches, Launch{Pane: first.Pane, NextTo: NoPane})
		p.have[first.Pane] = true
	}
	p.node(root)
	return p.launches
}

// planner collects launches and tracks which panes have a window so far.
type planner struct {
	have     map[int]bool
	launches []Launch
}

// node makes sure every child of n has a window, then descends. n itself
// always holds at least one window when node is called. Opening the windows
// of all children before descending matters: a child's first window still
// spans the child's whole area at that point, so splitting it yields the
// sibling's area rather than a corner of it.
func (p *planner) node(n *Node) {
	if n.Kind == Leaf {
		return
	}
	for i, child := range n.Children {
		if p.any(child) {
			continue
		}
		var anchor *Node
		var anchorExtent, newExtent int
		if i > 0 {
			// The new window takes over the area of this child and of
			// the windowless children after it.
			last := i
			for last+1 < len(n.Children) && !p.any(n.Children[last+1]) {
				last++
			}
			anchor = p.lastWindow(n.Children[i-1])
			anchorExtent = extent(n, i-1, i-1)
			newExtent = extent(n, i, last)
		} else {
			// Nothing before the first child to split; use the first
			// later child that has a window.
			next := 1
			for !p.any(n.Children[next]) {
				next++
			}
			anchor = p.firstWindow(n.Children[next])
			anchorExtent = extent(n, next, next)
			newExtent = extent(n, 0, next-1)
		}
		first := firstLeaf(child)
		p.launches = append(p.launches, Launch{
			Pane:    first.Pane,
			NextTo:  anchor.Pane,
			Stacked: n.Kind == TopBottom,
			Bias:    bias(newExtent, anchorExtent),
		})
		p.have[first.Pane] = true
	}
	for _, child := range n.Children {
		p.node(child)
	}
}

// any reports whether some pane under n has a window.
func (p *planner) any(n *Node) bool {
	return p.firstWindow(n) != nil
}

// firstWindow returns the first leaf under n, in layout order, that has a
// window.
func (p *planner) firstWindow(n *Node) *Node {
	var found *Node
	n.walkLeaves(func(leaf *Node) {
		if found == nil && p.have[leaf.Pane] {
			found = leaf
		}
	})
	return found
}

// lastWindow returns the last leaf under n, in layout order, that has a
// window.
func (p *planner) lastWindow(n *Node) *Node {
	var found *Node
	n.walkLeaves(func(leaf *Node) {
		if p.have[leaf.Pane] {
			found = leaf
		}
	})
	return found
}

// firstLeaf returns the first leaf under n in layout order.
func firstLeaf(n *Node) *Node {
	for n.Kind != Leaf {
		n = n.Children[0]
	}
	return n
}

// extent measures children from..to of n along n's split direction, including
// the borders between them.
func extent(n *Node, from, to int) int {
	total := 0
	for i := from; i <= to; i++ {
		if n.Kind == LeftRight {
			total += n.Children[i].W
		} else {
			total += n.Children[i].H
		}
		if i > from {
			total += borderCells
		}
	}
	return total
}

// bias returns the share, in percent, that a new window of newExtent cells
// takes when it splits a window that keeps anchorExtent cells.
func bias(newExtent, anchorExtent int) int {
	whole := newExtent + borderCells + anchorExtent
	value := (newExtent*percent + whole/2) / whole
	return min(max(value, minBiasPercent), maxBiasPercent)
}
