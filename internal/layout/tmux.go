// Package layout converts between tmux window layouts and kitty's split tree.
// It parses and formats tmux layout strings, builds the tmux layout that
// matches a kitty tab's geometry, and plans the kitty windows to open for the
// panes of a tmux window.
package layout

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Kind tells how a layout node arranges its children.
type Kind int

const (
	// Leaf is a single pane.
	Leaf Kind = iota
	// LeftRight places its children side by side; tmux writes it as "{...}".
	LeftRight
	// TopBottom stacks its children; tmux writes it as "[...]".
	TopBottom
)

const (
	// NoPane marks a leaf without a pane id, and a launch without an anchor.
	NoPane = -1
	// borderCells is the space tmux puts between two sibling panes.
	borderCells = 1
	// minPaneCells is the smallest width or height tmux gives a pane.
	minPaneCells = 1

	checksumHexDigits = 4
	checksumBits      = 16
)

// ErrInvalidLayout is returned for a string that is not a tmux layout.
var ErrInvalidLayout = errors.New("invalid tmux layout")

// Node is one cell of a tmux layout tree: a pane or a split holding children.
// Sizes and offsets are in character cells.
type Node struct {
	Kind       Kind
	W, H, X, Y int
	// Pane is the pane number (the id without "%") of a Leaf.
	Pane     int
	Children []*Node
}

// Parse reads a tmux layout string such as
// "f91d,120x40,0,0{60x40,0,0,0,59x40,61,0,1}". The checksum prefix is optional
// and is not verified.
func Parse(s string) (*Node, error) {
	p := &parser{s: stripChecksum(s)}
	node, err := p.node()
	if err != nil {
		return nil, err
	}
	if p.pos != len(p.s) {
		return nil, fmt.Errorf("%w: trailing data at offset %d", ErrInvalidLayout, p.pos)
	}
	return node, nil
}

// Format returns the layout string tmux accepts in select-layout: the checksum
// followed by the layout body.
func Format(n *Node) string {
	body := n.String()
	return fmt.Sprintf("%0*x,%s", checksumHexDigits, Checksum(body), body)
}

// String returns the layout body without its checksum.
func (n *Node) String() string {
	var b strings.Builder
	n.write(&b)
	return b.String()
}

// write appends the node in tmux's layout syntax.
func (n *Node) write(b *strings.Builder) {
	fmt.Fprintf(b, "%dx%d,%d,%d", n.W, n.H, n.X, n.Y)
	switch n.Kind {
	case Leaf:
		if n.Pane != NoPane {
			fmt.Fprintf(b, ",%d", n.Pane)
		}
	case LeftRight:
		n.writeChildren(b, '{', '}')
	case TopBottom:
		n.writeChildren(b, '[', ']')
	}
}

// writeChildren appends the children between the given brackets.
func (n *Node) writeChildren(b *strings.Builder, open, closing byte) {
	b.WriteByte(open)
	for i, child := range n.Children {
		if i > 0 {
			b.WriteByte(',')
		}
		child.write(b)
	}
	b.WriteByte(closing)
}

// Panes returns the pane numbers of the leaves in layout order, which is also
// tmux's pane index order.
func (n *Node) Panes() []int {
	var out []int
	n.walkLeaves(func(leaf *Node) { out = append(out, leaf.Pane) })
	return out
}

// Leaf returns the leaf showing the given pane, or nil.
func (n *Node) Leaf(pane int) *Node {
	var found *Node
	n.walkLeaves(func(leaf *Node) {
		if leaf.Pane == pane {
			found = leaf
		}
	})
	return found
}

// walkLeaves calls fn for every leaf in layout order.
func (n *Node) walkLeaves(fn func(*Node)) {
	if n.Kind == Leaf {
		fn(n)
		return
	}
	for _, child := range n.Children {
		child.walkLeaves(fn)
	}
}

// Checksum computes tmux's 16-bit rotating checksum of a layout body.
func Checksum(body string) uint16 {
	var sum uint16
	for i := 0; i < len(body); i++ {
		sum = (sum >> 1) + ((sum & 1) << (checksumBits - 1))
		sum += uint16(body[i])
	}
	return sum
}

// stripChecksum removes a leading "xxxx," checksum when present. A layout body
// starts with "WxH", so its first four characters are never all hex digits.
func stripChecksum(s string) string {
	if len(s) <= checksumHexDigits || s[checksumHexDigits] != ',' {
		return s
	}
	for i := 0; i < checksumHexDigits; i++ {
		if !isHexDigit(s[i]) {
			return s
		}
	}
	return s[checksumHexDigits+1:]
}

// parser is a recursive-descent reader of a layout body.
type parser struct {
	s   string
	pos int
}

// node parses one cell and its children.
func (p *parser) node() (*Node, error) {
	n := &Node{Pane: NoPane}
	fields := []struct {
		dst   *int
		after byte
	}{{&n.W, 'x'}, {&n.H, ','}, {&n.X, ','}, {&n.Y, 0}}
	for _, f := range fields {
		value, err := p.number()
		if err != nil {
			return nil, err
		}
		*f.dst = value
		if f.after == 0 {
			continue
		}
		err = p.expect(f.after)
		if err != nil {
			return nil, err
		}
	}
	switch p.peek() {
	case '{':
		n.Kind = LeftRight
		return n, p.children(n, '}')
	case '[':
		n.Kind = TopBottom
		return n, p.children(n, ']')
	case ',':
		// ",<digits>" is this leaf's pane id unless an "x" follows, in which
		// case it is the size of the next sibling.
		end := p.pos + 1
		for end < len(p.s) && isDigit(p.s[end]) {
			end++
		}
		if end > p.pos+1 && (end == len(p.s) || p.s[end] != 'x') {
			pane, err := strconv.Atoi(p.s[p.pos+1 : end])
			if err != nil {
				return nil, fmt.Errorf("%w: pane id at offset %d", ErrInvalidLayout, p.pos)
			}
			n.Pane = pane
			p.pos = end
		}
	}
	return n, nil
}

// children parses a bracketed, comma-separated list of cells into n.
func (p *parser) children(n *Node, closing byte) error {
	p.pos++ // opening bracket
	for {
		child, err := p.node()
		if err != nil {
			return err
		}
		n.Children = append(n.Children, child)
		switch p.peek() {
		case ',':
			p.pos++
		case closing:
			p.pos++
			return nil
		default:
			return fmt.Errorf("%w: expected ',' or %q at offset %d", ErrInvalidLayout, closing, p.pos)
		}
	}
}

// number parses an unsigned decimal number.
func (p *parser) number() (int, error) {
	start := p.pos
	for p.pos < len(p.s) && isDigit(p.s[p.pos]) {
		p.pos++
	}
	if p.pos == start {
		return 0, fmt.Errorf("%w: expected a number at offset %d", ErrInvalidLayout, start)
	}
	value, err := strconv.Atoi(p.s[start:p.pos])
	if err != nil {
		return 0, fmt.Errorf("%w: number at offset %d", ErrInvalidLayout, start)
	}
	return value, nil
}

// expect consumes the given byte.
func (p *parser) expect(c byte) error {
	if p.peek() != c {
		return fmt.Errorf("%w: expected %q at offset %d", ErrInvalidLayout, c, p.pos)
	}
	p.pos++
	return nil
}

// peek returns the next byte, or 0 at the end.
func (p *parser) peek() byte {
	if p.pos >= len(p.s) {
		return 0
	}
	return p.s[p.pos]
}

// isDigit reports whether c is a decimal digit.
func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

// isHexDigit reports whether c is a lowercase or uppercase hex digit.
func isHexDigit(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}
