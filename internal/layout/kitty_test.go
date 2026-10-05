package layout

import "testing"

// window builds a kitty window leaf.
func window(pane, cols, rows int) *Split {
	return &Split{Window: true, Pane: pane, Cols: cols, Rows: rows}
}

// sideBySide builds a pair with one left of two.
func sideBySide(one, two *Split) *Split {
	return &Split{Horizontal: true, One: one, Two: two}
}

// stacked builds a pair with one above two.
func stacked(one, two *Split) *Split {
	return &Split{One: one, Two: two}
}

func TestFromSplits(t *testing.T) {
	cases := []struct {
		name string
		tree *Split
		want string
	}{
		{
			name: "single window",
			tree: window(3, 190, 61),
			want: "190x61,0,0,3",
		},
		{
			name: "two side by side add a border column",
			tree: sideBySide(window(1, 94, 61), window(2, 94, 61)),
			want: "189x61,0,0{94x61,0,0,1,94x61,95,0,2}",
		},
		{
			name: "two stacked add a border row",
			tree: stacked(window(1, 190, 30), window(2, 190, 30)),
			want: "190x61,0,0[190x30,0,0,1,190x30,0,31,2]",
		},
		{
			name: "column that lines up exactly",
			tree: sideBySide(window(1, 94, 61), stacked(window(2, 94, 42), window(3, 94, 18))),
			want: "189x61,0,0{94x61,0,0,1,94x61,95,0[94x42,95,0,2,94x18,95,43,3]}",
		},
		{
			name: "column taller than its neighbour shrinks its largest pane",
			tree: sideBySide(window(1, 94, 61), stacked(window(2, 94, 42), window(3, 94, 19))),
			want: "189x61,0,0{94x61,0,0,1,94x61,95,0[94x41,95,0,2,94x19,95,42,3]}",
		},
		{
			name: "column shorter than its neighbour shrinks the neighbour",
			tree: sideBySide(window(1, 94, 61), stacked(window(2, 94, 41), window(3, 94, 18))),
			want: "189x60,0,0{94x60,0,0,1,94x60,95,0[94x41,95,0,2,94x18,95,42,3]}",
		},
		{
			name: "row narrower than the window above it",
			tree: stacked(window(1, 100, 20), sideBySide(window(2, 49, 20), window(3, 49, 20))),
			want: "99x41,0,0[99x20,0,0,1,99x20,0,21{49x20,0,21,2,49x20,50,21,3}]",
		},
		{
			name: "row wider than the window above it shrinks its largest panes",
			tree: stacked(window(1, 100, 20), sideBySide(window(2, 50, 20), window(3, 51, 20))),
			want: "100x41,0,0[100x20,0,0,1,100x20,0,21{50x20,0,21,2,49x20,51,21,3}]",
		},
		{
			name: "nested pairs in the same direction flatten",
			tree: sideBySide(window(1, 30, 10), sideBySide(window(2, 30, 10), sideBySide(window(3, 30, 10), window(4, 30, 10)))),
			want: "123x10,0,0{30x10,0,0,1,30x10,31,0,2,30x10,62,0,3,30x10,93,0,4}",
		},
		{
			name: "left-nested pairs flatten too",
			tree: stacked(stacked(window(1, 80, 5), window(2, 80, 6)), window(3, 80, 7)),
			want: "80x20,0,0[80x5,0,0,1,80x6,0,6,2,80x7,0,13,3]",
		},
		{
			name: "foreign window beside a pane is dropped",
			tree: sideBySide(window(1, 94, 61), nil),
			want: "94x61,0,0,1",
		},
		{
			name: "foreign window inside a nested pair collapses the pair",
			tree: stacked(sideBySide(nil, window(1, 60, 20)), sideBySide(window(2, 60, 20), window(3, 60, 20))),
			want: "60x41,0,0[60x20,0,0,1,60x20,0,21{30x20,0,21,2,29x20,31,21,3}]",
		},
		{
			name: "zero sized window is treated as one cell",
			tree: sideBySide(window(1, 0, 0), window(2, 10, 5)),
			want: "12x1,0,0{1x1,0,0,1,10x1,2,0,2}",
		},
		{
			name: "deep mix",
			tree: sideBySide(
				stacked(sideBySide(window(5, 19, 7), window(7, 20, 7)), stacked(sideBySide(window(6, 19, 7), window(8, 20, 7)), window(9, 40, 8))),
				window(10, 39, 24),
			),
			want: "80x24,0,0{40x24,0,0[40x7,0,0{19x7,0,0,5,20x7,20,0,7},40x7,0,8{19x7,0,8,6,20x7,20,8,8},40x8,0,16,9],39x24,41,0,10}",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node := FromSplits(tc.tree)
			if node == nil {
				t.Fatal("FromSplits returned nil")
			}
			if node.String() != tc.want {
				t.Fatalf("got  %s\nwant %s", node.String(), tc.want)
			}
		})
	}
}

func TestFromSplitsWithoutPanes(t *testing.T) {
	for _, tree := range []*Split{nil, sideBySide(nil, nil), stacked(nil, sideBySide(nil, nil))} {
		if FromSplits(tree) != nil {
			t.Fatalf("FromSplits(%+v) should be nil", tree)
		}
	}
}

// TestFromSplitsIsConsistent checks the property tmux enforces when it parses
// a layout: children tile their parent exactly.
func TestFromSplitsIsConsistent(t *testing.T) {
	trees := []*Split{
		sideBySide(window(1, 94, 61), stacked(window(2, 94, 40), window(3, 93, 23))),
		stacked(sideBySide(window(1, 10, 3), stacked(window(2, 7, 1), window(3, 9, 9))), sideBySide(window(4, 30, 2), window(5, 2, 2))),
		sideBySide(stacked(window(1, 3, 3), window(2, 300, 3)), stacked(window(3, 1, 1), sideBySide(window(4, 5, 50), window(5, 5, 2)))),
	}
	for i, tree := range trees {
		node := FromSplits(tree)
		checkTiles(t, i, node)
	}
}

// checkTiles fails when a node's children do not tile it exactly.
func checkTiles(t *testing.T, tree int, n *Node) {
	t.Helper()
	if n.W < minPaneCells || n.H < minPaneCells {
		t.Fatalf("tree %d: node %s is smaller than a pane", tree, n)
	}
	if n.Kind == Leaf {
		return
	}
	x, y := n.X, n.Y
	for _, child := range n.Children {
		if child.X != x || child.Y != y {
			t.Fatalf("tree %d: child %s of %s is at %d,%d, want %d,%d", tree, child, n, child.X, child.Y, x, y)
		}
		if n.Kind == LeftRight {
			if child.H != n.H {
				t.Fatalf("tree %d: child %s does not span the height of %s", tree, child, n)
			}
			x += child.W + borderCells
		} else {
			if child.W != n.W {
				t.Fatalf("tree %d: child %s does not span the width of %s", tree, child, n)
			}
			y += child.H + borderCells
		}
		checkTiles(t, tree, child)
	}
	if n.Kind == LeftRight && x-borderCells != n.X+n.W {
		t.Fatalf("tree %d: children of %s cover %d columns", tree, n, x-borderCells-n.X)
	}
	if n.Kind == TopBottom && y-borderCells != n.Y+n.H {
		t.Fatalf("tree %d: children of %s cover %d rows", tree, n, y-borderCells-n.Y)
	}
}
