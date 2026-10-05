package layout

// Split is kitty's split tree for one tab, reduced to the windows that show
// tmux panes. A node is either a window or a pair. A nil child of a pair
// stands for a window that does not belong to kitmux; it is left out of the
// tmux layout.
type Split struct {
	// Window marks a kitty window; Pane, Cols and Rows are the pane it shows
	// and its size in cells.
	Window bool
	Pane   int
	Cols   int
	Rows   int

	// Horizontal, One and Two describe a pair: One is left of Two when
	// Horizontal is true, above it otherwise.
	Horizontal bool
	One        *Split
	Two        *Split
}

// FromSplits builds the tmux layout that mirrors a kitty tab. The panes sit in
// the same arrangement as the kitty windows, and each pane is at most as large
// as its window: tmux needs the panes of a row or column to line up exactly,
// which kitty's pixel-based geometry does not guarantee, so where they differ
// a pane ends up a cell smaller than its window. It returns nil when the tree
// holds no kitmux window.
func FromSplits(root *Split) *Node {
	node := convert(root)
	if node == nil {
		return nil
	}
	w, h := natural(node)
	fit(node, w, h)
	place(node, 0, 0)
	return node
}

// convert turns the binary split tree into tmux's n-ary form: foreign windows
// are dropped, pairs left with one child collapse, and nested pairs with the
// same direction merge into one node. Leaf sizes are the kitty window sizes.
func convert(s *Split) *Node {
	if s == nil {
		return nil
	}
	if s.Window {
		return &Node{Kind: Leaf, Pane: s.Pane, W: max(s.Cols, minPaneCells), H: max(s.Rows, minPaneCells)}
	}
	one := convert(s.One)
	two := convert(s.Two)
	if one == nil {
		return two
	}
	if two == nil {
		return one
	}
	kind := TopBottom
	if s.Horizontal {
		kind = LeftRight
	}
	node := &Node{Kind: kind, Pane: NoPane}
	for _, child := range []*Node{one, two} {
		if child.Kind == kind {
			node.Children = append(node.Children, child.Children...)
		} else {
			node.Children = append(node.Children, child)
		}
	}
	return node
}

// natural computes, bottom-up, the size each node would have if every pane
// took its window's size: along the split the children and their borders add
// up, across it the smallest child wins (but never less than the largest
// child's minimum). It stores the sizes in the nodes and returns the root's.
func natural(n *Node) (int, int) {
	if n.Kind == Leaf {
		return n.W, n.H
	}
	along, across := 0, 0
	minAcross := 0
	for i, child := range n.Children {
		w, h := natural(child)
		childAlong, childAcross := w, h
		if n.Kind == TopBottom {
			childAlong, childAcross = h, w
		}
		along += childAlong
		if i > 0 {
			along += borderCells
		}
		if i == 0 || childAcross < across {
			across = childAcross
		}
		minW, minH := minimum(child)
		childMin := minH
		if n.Kind == TopBottom {
			childMin = minW
		}
		minAcross = max(minAcross, childMin)
	}
	across = max(across, minAcross)
	if n.Kind == LeftRight {
		n.W, n.H = along, across
	} else {
		n.W, n.H = across, along
	}
	return n.W, n.H
}

// minimum returns the smallest size a node can shrink to.
func minimum(n *Node) (int, int) {
	if n.Kind == Leaf {
		return minPaneCells, minPaneCells
	}
	along, across := 0, 0
	for i, child := range n.Children {
		w, h := minimum(child)
		childAlong, childAcross := w, h
		if n.Kind == TopBottom {
			childAlong, childAcross = h, w
		}
		along += childAlong
		if i > 0 {
			along += borderCells
		}
		across = max(across, childAcross)
	}
	if n.Kind == LeftRight {
		return along, across
	}
	return across, along
}

// fit gives a node its final size and passes it down. Across the split every
// child takes the node's size. Along it the children keep their natural sizes;
// the cells by which those exceed the node are taken one at a time from the
// largest child (the later one on a tie).
func fit(n *Node, w, h int) {
	if n.Kind == Leaf {
		n.W, n.H = w, h
		return
	}
	horizontal := n.Kind == LeftRight
	target := h
	if horizontal {
		target = w
	}
	sizes := make([]int, len(n.Children))
	total := borderCells * (len(n.Children) - 1)
	for i, child := range n.Children {
		sizes[i] = child.H
		if horizontal {
			sizes[i] = child.W
		}
		total += sizes[i]
	}
	excess := total - target
	if excess < 0 {
		// Only when a sibling's minimum forced this node wider than its
		// windows; the last child takes the slack.
		sizes[len(sizes)-1] -= excess
	}
	floors := make([]int, len(n.Children))
	for i, child := range n.Children {
		minW, minH := minimum(child)
		floors[i] = minH
		if horizontal {
			floors[i] = minW
		}
	}
	for ; excess > 0; excess-- {
		victim := -1
		for i := range sizes {
			shrinkable := sizes[i] > floors[i]
			if shrinkable && (victim < 0 || sizes[i] >= sizes[victim]) {
				victim = i
			}
		}
		if victim < 0 {
			break
		}
		sizes[victim]--
	}
	n.W, n.H = w, h
	for i, child := range n.Children {
		if horizontal {
			fit(child, sizes[i], h)
		} else {
			fit(child, w, sizes[i])
		}
	}
}

// place assigns offsets top-down, leaving one border cell between siblings.
func place(n *Node, x, y int) {
	n.X, n.Y = x, y
	for _, child := range n.Children {
		place(child, x, y)
		if n.Kind == LeftRight {
			x += child.W + borderCells
		} else {
			y += child.H + borderCells
		}
	}
}
