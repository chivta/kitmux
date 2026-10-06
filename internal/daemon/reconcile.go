package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"

	"kitmux/internal/control"
	"kitmux/internal/kitty"
	"kitmux/internal/layout"
	"kitmux/internal/tabs"
)

const (
	// relaunchInterval is the least time between two launches of a window
	// for the same pane, so a proxy that dies at once cannot make kitty
	// flicker in a tight loop.
	relaunchInterval = time.Second
	// focusRequestTTL is how long a "focus this when it appears" request
	// waits for its tab or window.
	focusRequestTTL = 5 * time.Second
)

// kwin is a kitty window that shows a tmux pane.
type kwin struct {
	id   int
	tab  int
	os   int
	cols int
	rows int
	key  paneKey
}

// ktab is a kitty tab.
type ktab struct {
	id     int
	os     int
	title  string
	active bool
	layout string
	pairs  *kitty.Pair
	// groups maps a window group id to its window ids.
	groups map[int][]int
	// first is some window of the tab, used to address the tab in actions
	// that take a window.
	first int
}

// view is one look at kitty: what `kitty @ ls` returned, indexed the way the
// reconciler needs it.
type view struct {
	tabs map[int]*ktab
	// mainTabs are the tabs of kitmux's OS window, in tab bar order.
	mainTabs []*ktab
	// panes lists the kitty windows of each pane; more than one is a
	// duplicate to clean up.
	panes   map[paneKey][]*kwin
	windows map[int]*kwin
	// mainPresent is false once kitmux's OS window is gone.
	mainPresent bool
	// anchor is some window in kitmux's OS window; new tabs open next to
	// it so they land in that OS window.
	anchor int
	// activeWindow is the active window of the active tab of kitmux's OS
	// window, 0 if there is none.
	activeWindow int
	// allWindows holds the id of every kitty window, kitmux's or not.
	allWindows map[int]bool
}

// claims pairs tmux windows with the kitty tabs that show them.
type claims struct {
	tabOf    map[windowKey]*ktab
	windowOf map[int]windowKey
}

// newView indexes a `kitty @ ls` reply.
func newView(d *Daemon, snapshot []kitty.OSWindow) *view {
	targetIndex := map[string]int{}
	for _, t := range d.targets {
		targetIndex[t.spec.Name] = t.spec.Index
	}
	v := &view{tabs: map[int]*ktab{}, panes: map[paneKey][]*kwin{}, windows: map[int]*kwin{}, allWindows: map[int]bool{}}
	for _, osw := range snapshot {
		isMain := osw.ID == d.mainOS
		if isMain {
			v.mainPresent = true
		}
		for _, tab := range osw.Tabs {
			kt := &ktab{
				id: tab.ID, os: osw.ID, title: tab.Title, active: tab.IsActive,
				layout: tab.Layout, pairs: tab.LayoutState.Pairs, groups: map[int][]int{},
			}
			for _, group := range tab.Groups {
				kt.groups[group.ID] = group.Windows
			}
			for _, win := range tab.Windows {
				v.allWindows[win.ID] = true
				if kt.first == 0 {
					kt.first = win.ID
				}
				if isMain && tab.IsActive && win.IsActive {
					v.activeWindow = win.ID
				}
				if isMain && v.anchor == 0 {
					v.anchor = win.ID
				}
				target, isTarget := targetIndex[win.UserVars[kitty.VarTarget]]
				pane, isPane := parsePaneID(win.UserVars[kitty.VarPane])
				if !isTarget || !isPane {
					continue
				}
				kw := &kwin{
					id: win.ID, tab: tab.ID, os: osw.ID, cols: win.Columns, rows: win.Lines,
					key: paneKey{target: target, pane: pane},
				}
				v.windows[kw.id] = kw
				v.panes[kw.key] = append(v.panes[kw.key], kw)
			}
			v.tabs[kt.id] = kt
			if isMain {
				v.mainTabs = append(v.mainTabs, kt)
			}
		}
	}
	return v
}

// look asks kitty for its current state.
func (d *Daemon) look() (*view, bool) {
	ctx, cancel := context.WithTimeout(d.ctx, kittyCallTimeout)
	defer cancel()
	snapshot, err := d.kitty.List(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("cannot list kitty windows")
		return nil, false
	}
	v := newView(d, snapshot)
	d.windowPanes = map[int]paneKey{}
	for id, kw := range v.windows {
		d.windowPanes[id] = kw.key
	}
	return v, true
}

// reconcile makes kitty match tmux's windows and panes, then makes tmux match
// kitty's geometry. tmux decides which windows and panes exist, what they are
// called and in which order their tabs go; kitty decides how the panes of a
// tab are arranged and how large they are. Every pass starts from a fresh look
// at both, so a pass that is interrupted or finds kitty mid-change is simply
// repeated.
func (d *Daemon) reconcile() {
	if d.stopping || d.kitty == nil {
		return
	}
	v, ok := d.look()
	if !ok {
		return
	}
	if !v.mainPresent {
		d.shutdown("kitty window closed", false)
		return
	}
	if d.allGone() {
		d.shutdown("every session ended", true)
		return
	}
	// Opening and moving tabs can take the focus; it goes back to where
	// it was unless the user asked for the new tab or window.
	focusBefore := v.activeWindow
	launched := d.ensureWindows(v)
	if launched {
		v, ok = d.look()
		if !ok {
			return
		}
	}
	c := d.claimTabs(v)
	d.syncTitles(v, c)
	moved := d.syncOrder(v, c)
	d.syncLayouts(v, c)
	focused := d.applyFocus(v, c)
	if (launched || moved) && !focused && v.allWindows[focusBefore] {
		d.focusWindow(focusBefore)
	}
}

// allGone reports whether no target has a session left.
func (d *Daemon) allGone() bool {
	for _, t := range d.targets {
		if t.status != statusGone {
			return false
		}
	}
	return true
}

// claimTabs decides which kitty tab shows which tmux window: the tab that
// holds most of the window's panes, each tab going to one tmux window only.
// Normally every pane of a window sits in one tab; they spread out only after
// tmux moved panes between windows.
func (d *Daemon) claimTabs(v *view) claims {
	c := claims{tabOf: map[windowKey]*ktab{}, windowOf: map[int]windowKey{}}
	for _, t := range d.targets {
		for _, w := range t.windows {
			count := map[int]int{}
			for _, pane := range w.tree.Panes() {
				for _, kw := range v.panes[paneKey{target: t.spec.Index, pane: pane}] {
					count[kw.tab]++
				}
			}
			best := 0
			for tab, n := range count {
				_, taken := c.windowOf[tab]
				if taken {
					continue
				}
				if best == 0 || n > count[best] || (n == count[best] && tab < best) {
					best = tab
				}
			}
			if best != 0 {
				wk := windowKey{target: t.spec.Index, id: w.id}
				c.tabOf[wk] = v.tabs[best]
				c.windowOf[best] = wk
			}
		}
	}
	return c
}

// ensureWindows opens a kitty window for every pane that lacks one and closes
// the windows that are in the wrong place: duplicates, windows left in another
// tmux window's tab, and windows of panes that no longer exist. It reports
// whether it changed anything in kitty. A window the user closed behind
// kitmux's back is simply a pane without a window, and comes back here.
func (d *Daemon) ensureWindows(v *view) bool {
	c := d.claimTabs(v)
	changed := false
	for _, t := range d.targets {
		for _, w := range t.windows {
			tab := c.tabOf[windowKey{target: t.spec.Index, id: w.id}]
			present := map[int]bool{}
			ids := map[int]int{}
			for _, pane := range w.tree.Panes() {
				for _, kw := range v.panes[paneKey{target: t.spec.Index, pane: pane}] {
					if tab != nil && kw.tab == tab.id && !present[pane] {
						present[pane] = true
						ids[pane] = kw.id
						continue
					}
					d.closeWindow(kw.id)
					changed = true
				}
			}
			for _, l := range layout.Plan(w.tree, present) {
				key := paneKey{target: t.spec.Index, pane: l.Pane}
				if time.Since(d.lastLaunch[key]) < relaunchInterval {
					// Later launches may split this one; try again
					// on the next pass.
					d.scheduleReconcileIn(relaunchInterval)
					break
				}
				id, err := d.launch(v, t, w, l, ids)
				if err != nil {
					log.Error().Err(err).Str("target", t.spec.Name).Int("pane", l.Pane).Msg("cannot open kitty window")
					break
				}
				ids[l.Pane] = id
				d.lastLaunch[key] = time.Now()
				changed = true
			}
		}
	}
	for key, wins := range v.panes {
		t := d.targets[key.target]
		keep := t.status == statusReconnecting || (t.status == statusLive && (!t.listed || t.hasPane(key.pane)))
		if keep {
			continue
		}
		// The proxy exits when told to, and takes its window with it; a
		// window whose proxy is not connected has to be closed directly.
		told := d.hub.exit(key)
		for _, kw := range wins {
			if !told {
				d.closeWindow(kw.id)
			}
			changed = true
		}
	}
	return changed
}

// launch opens the kitty window for one planned pane and returns its id. ids
// maps the panes of the tmux window to their kitty windows so far.
func (d *Daemon) launch(v *view, t *target, w *window, l layout.Launch, ids map[int]int) (int, error) {
	key := paneKey{target: t.spec.Index, pane: l.Pane}
	spec := kitty.LaunchSpec{Vars: paneVars(t, w, l.Pane), Command: d.paneCommand(t, l.Pane)}
	if l.NextTo == layout.NoPane {
		wk := windowKey{target: t.spec.Index, id: w.id}
		spec.Tab = true
		spec.NextTo = v.anchor
		spec.TabTitle = tabs.Title(t.spec.Name, w.index, w.name)
		_, focus := d.focusWindows[wk]
		spec.KeepFocus = !focus
	} else {
		spec.NextTo = ids[l.NextTo]
		spec.Stacked = l.Stacked
		spec.Bias = l.Bias
		_, focus := d.focusPanes[key]
		spec.KeepFocus = !focus
	}
	ctx, cancel := context.WithTimeout(d.ctx, kittyCallTimeout)
	defer cancel()
	return d.kitty.Launch(ctx, spec)
}

// closeWindow closes one kitty window.
func (d *Daemon) closeWindow(id int) {
	ctx, cancel := context.WithTimeout(d.ctx, kittyCallTimeout)
	defer cancel()
	err := d.kitty.CloseWindow(ctx, id)
	if err != nil {
		log.Debug().Err(err).Int("window", id).Msg("close kitty window")
	}
}

// syncTitles sets each tab's title from its tmux window.
func (d *Daemon) syncTitles(_ *view, c claims) {
	for _, t := range d.targets {
		for _, w := range t.windows {
			tab := c.tabOf[windowKey{target: t.spec.Index, id: w.id}]
			if tab == nil {
				continue
			}
			want := tabs.Title(t.spec.Name, w.index, w.name)
			if tab.title == want {
				continue
			}
			ctx, cancel := context.WithTimeout(d.ctx, kittyCallTimeout)
			err := d.kitty.SetTabTitle(ctx, tab.id, want)
			cancel()
			if err != nil {
				log.Warn().Err(err).Int("tab", tab.id).Msg("cannot set tab title")
			}
		}
	}
}

// syncOrder moves kitmux's tabs into target and window-index order and reports
// whether it moved any. kitty can only move the active tab, so each tab to
// move is focused first; the caller puts the focus back.
func (d *Daemon) syncOrder(v *view, c claims) bool {
	current := make([]tabs.Tab, len(v.mainTabs))
	for i, kt := range v.mainTabs {
		current[i] = tabs.Tab{ID: kt.id}
		wk, managed := c.windowOf[kt.id]
		if !managed {
			continue
		}
		w := d.targets[wk.target].windowByID(wk.id)
		if w == nil {
			continue
		}
		current[i].Managed = true
		current[i].Target = wk.target
		current[i].Index = w.index
	}
	moves := tabs.Moves(current)
	if len(moves) == 0 {
		return false
	}
	ctx, cancel := context.WithTimeout(d.ctx, kittyCallTimeout)
	defer cancel()
	for _, move := range moves {
		kt := v.tabs[move.ID]
		err := d.kitty.FocusTab(ctx, kt.id)
		for step := 0; err == nil && step < move.Steps; step++ {
			err = d.kitty.MoveActiveTabBackward(ctx, kt.first)
		}
		if err != nil {
			log.Warn().Err(err).Int("tab", kt.id).Msg("cannot move tab")
			break
		}
	}
	return true
}

// focusWindow gives a kitty window the focus.
func (d *Daemon) focusWindow(id int) {
	ctx, cancel := context.WithTimeout(d.ctx, kittyCallTimeout)
	defer cancel()
	err := d.kitty.FocusWindow(ctx, id)
	if err != nil {
		log.Warn().Err(err).Int("window", id).Msg("cannot focus window")
	}
}

// syncLayouts applies each tab's kitty geometry to its tmux window: pane
// order first (tmux assigns panes to layout cells by order), then the window
// size and the layout. A layout is sent only when it differs from what tmux
// reports and was not just sent, which is what lets the two sides settle.
func (d *Daemon) syncLayouts(v *view, c claims) {
	for _, t := range d.targets {
		conn := t.conn.Load()
		if conn == nil {
			continue
		}
		for _, w := range t.windows {
			tab := c.tabOf[windowKey{target: t.spec.Index, id: w.id}]
			if tab == nil {
				continue
			}
			state := t.syncOf(w.id)
			if state.busy {
				continue
			}
			if tab.layout != kitty.LayoutSplits {
				d.forceSplits(tab)
				continue
			}
			node := layout.FromSplits(buildSplit(v, tab, t, w))
			if node == nil {
				continue
			}
			tmuxOrder := w.tree.Panes()
			kittyOrder := node.Panes()
			if !samePanes(tmuxOrder, kittyOrder) {
				// A pane has no window yet; wait for it.
				continue
			}
			want := layout.Format(node)
			size := fmt.Sprintf("%dx%d", node.W, node.H)
			swaps := swapCommands(tmuxOrder, kittyOrder)
			resize := "refresh-client -C " + w.id + ":" + size
			if want == w.layout && len(swaps) == 0 {
				if state.size != size {
					state.size = size
					conn.Send(resize, nil)
				}
				continue
			}
			if state.applied == want && len(swaps) == 0 {
				// tmux was given this layout and reports another: it
				// did not take it (another client may dictate the
				// size). Sending it again would loop.
				continue
			}
			cmds := append(swaps, resize, "select-layout -t "+w.id+" "+control.Quote(want))
			d.sendLayout(t, w.id, state, cmds, want, size)
		}
	}
}

// sendLayout sends a layout batch and updates the window's sync state when
// the reply arrives.
func (d *Daemon) sendLayout(t *target, windowID string, state *syncState, cmds []string, want, size string) {
	conn := t.conn.Load()
	if conn == nil {
		return
	}
	state.busy = true
	generation := t.generation
	log.Info().Str("target", t.spec.Name).Str("window", windowID).Str("layout", want).Int("swaps", len(cmds)-2).Msg("sending layout")
	conn.Batch(cmds, func(replies []control.Reply, err error) {
		d.events.post(func() {
			if generation != t.generation {
				return
			}
			state.busy = false
			state.applied = want
			state.size = size
			failed := err != nil || len(replies) != len(cmds) || replies[len(replies)-1].Failed
			if failed && len(replies) > 0 {
				log.Warn().Str("target", t.spec.Name).Str("window", windowID).Str("layout", want).
					Str("tmux", replies[len(replies)-1].Text()).Msg("tmux rejected layout")
			}
			t.requestList()
		})
	})
}

// forceSplits switches a kitmux tab back to the splits layout, the only one
// whose geometry kitmux can read.
func (d *Daemon) forceSplits(tab *ktab) {
	ctx, cancel := context.WithTimeout(d.ctx, kittyCallTimeout)
	defer cancel()
	err := d.kitty.GotoLayout(ctx, tab.id, kitty.LayoutSplits)
	if err == nil {
		d.scheduleReconcile()
		return
	}
	if !d.warnedLayout[tab.id] {
		d.warnedLayout[tab.id] = true
		log.Error().Err(err).Int("tab", tab.id).Str("layout", tab.layout).
			Msg("kitmux tabs need kitty's splits layout; add it to enabled_layouts")
	}
}

// applyFocus focuses tabs and windows that were asked for before they
// existed, drops requests that waited too long, and reports whether it
// focused anything.
func (d *Daemon) applyFocus(v *view, c claims) bool {
	ctx, cancel := context.WithTimeout(d.ctx, kittyCallTimeout)
	defer cancel()
	focused := false
	for wk, asked := range d.focusWindows {
		tab := c.tabOf[wk]
		if tab != nil {
			focused = true
			err := d.kitty.FocusTab(ctx, tab.id)
			if err != nil {
				log.Warn().Err(err).Msg("cannot focus new tab")
			}
		}
		if tab != nil || time.Since(asked) > focusRequestTTL {
			delete(d.focusWindows, wk)
		}
	}
	for key, asked := range d.focusPanes {
		wins := v.panes[key]
		if len(wins) > 0 {
			focused = true
			err := d.kitty.FocusWindow(ctx, wins[0].id)
			if err != nil {
				log.Warn().Err(err).Msg("cannot focus new window")
			}
		}
		if len(wins) > 0 || time.Since(asked) > focusRequestTTL {
			delete(d.focusPanes, key)
		}
	}
	return focused
}

// buildSplit converts a kitty tab's split tree into the form the layout
// package takes, keeping only the windows that show panes of the tmux window
// w.
func buildSplit(v *view, tab *ktab, t *target, w *window) *layout.Split {
	if tab.pairs == nil {
		return nil
	}
	wanted := map[int]bool{}
	for _, pane := range w.tree.Panes() {
		wanted[pane] = true
	}
	leaf := func(group int) *layout.Split {
		for _, id := range tab.groups[group] {
			kw := v.windows[id]
			if kw != nil && kw.key.target == t.spec.Index && wanted[kw.key.pane] {
				return &layout.Split{Window: true, Pane: kw.key.pane, Cols: kw.cols, Rows: kw.rows}
			}
		}
		return nil
	}
	var walk func(pair *kitty.Pair) *layout.Split
	side := func(child *kitty.PairChild) *layout.Split {
		if child == nil {
			return nil
		}
		if child.Pair != nil {
			return walk(child.Pair)
		}
		return leaf(child.Group)
	}
	walk = func(pair *kitty.Pair) *layout.Split {
		return &layout.Split{Horizontal: pair.Horizontal, One: side(pair.One), Two: side(pair.Two)}
	}
	return walk(tab.pairs)
}

// samePanes reports whether two pane lists hold the same panes, in any order.
func samePanes(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[int]bool{}
	for _, pane := range a {
		seen[pane] = true
	}
	for _, pane := range b {
		if !seen[pane] {
			return false
		}
	}
	return true
}

// swapCommands returns the swap-pane commands that reorder tmux's panes from
// current to want.
func swapCommands(current, want []int) []string {
	order := append([]int{}, current...)
	var cmds []string
	for i := range want {
		if order[i] == want[i] {
			continue
		}
		at := i
		for order[at] != want[i] {
			at++
		}
		cmds = append(cmds, "swap-pane -d -s "+paneID(want[i])+" -t "+paneID(order[i]))
		order[i], order[at] = order[at], order[i]
	}
	return cmds
}
