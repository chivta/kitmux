package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"

	"kitmux/internal/cli"
	"kitmux/internal/kitty"
	"kitmux/internal/tabs"
	"kitmux/internal/tmux"
)

const (
	runtimeDirPrefix  = "kitmux-"
	runtimeDirMode    = 0o700
	runtimeFileMode   = 0o600
	runtimeRandomSize = 4 // bytes; printed as 8 hex digits

	kittySocketName  = "kitty.sock"
	daemonSocketName = "d.sock"
	overlayFileName  = "kitmux.conf"
	sessionFileName  = "session.conf"

	// maxSocketPath keeps socket paths under the 104-byte limit of macOS.
	maxSocketPath = 100

	// SubcommandPane is the kitmux subcommand kitty runs in every pane's
	// window.
	SubcommandPane = "_pane"

	// relistInterval is the safety net behind tmux's notifications, which
	// miss some window changes.
	relistInterval = 2 * time.Second
	// saveInterval is how often tmux-continuum is asked to save.
	saveInterval = time.Minute
	// reconcileDelay collapses bursts of events into one pass over kitty.
	reconcileDelay = 30 * time.Millisecond
	// closeSettleDelay gives kitty time to finish closing things before the
	// daemon looks at what is left.
	closeSettleDelay = 150 * time.Millisecond
	// paintFallbackDelay is how long a new pane window waits for tmux to
	// take its size before it is painted anyway.
	paintFallbackDelay = 700 * time.Millisecond

	firstListTimeout  = 15 * time.Second
	kittyStartTimeout = 20 * time.Second
	kittyPollInterval = 100 * time.Millisecond
	kittyCallTimeout  = 10 * time.Second
	kittyQuitGrace    = 3 * time.Second

	kittyBinary      = "kitty"
	kittyMacOSBinary = "/Applications/kitty.app/Contents/MacOS/kitty"
)

// Options configure a daemon run.
type Options struct {
	Config cli.Config
	// Bin is the kitmux executable; kitty runs it for panes and key
	// actions.
	Bin string
	// Ready, when set, receives one line once the daemon is up or has
	// failed to start, and is then closed. The launcher waits for it.
	Ready io.WriteCloser
}

// Daemon is the running bridge. See the package comment for the threading
// model: every field without a note is owned by the main loop.
type Daemon struct {
	opts Options

	// ctx is cancelled when the daemon stops; background goroutines watch
	// it.
	ctx    context.Context
	cancel context.CancelFunc

	events *queue
	hub    *hub
	// targets is fixed after startup, so other goroutines may index it.
	targets []*target

	runtimeDir   string
	daemonSocket string
	kittySocket  string
	listener     net.Listener

	kitty       *kitty.Client
	kittyCmd    *exec.Cmd
	kittyExited chan struct{}
	// mainOS is the kitty OS window kitmux opened; closing it detaches.
	mainOS int

	stopping        bool
	reconcileQueued bool

	// focusWindows and focusPanes are tmux windows and panes whose kitty
	// tab or window takes the focus as soon as it exists: the ones the
	// user just created with a key. The value is when it was asked for.
	focusWindows map[windowKey]time.Time
	focusPanes   map[paneKey]time.Time
	// lastLaunch throttles reopening a pane's window.
	lastLaunch map[paneKey]time.Time
	// windowPanes maps kitty window ids to panes, as of the last look at
	// kitty.
	windowPanes map[int]paneKey
	// warnedLayout remembers windows whose tab was already reported for not
	// taking the splits layout.
	warnedLayout map[int]bool
}

// Run starts the daemon and blocks until kitty exits, every session ended, or
// ctx is cancelled.
func Run(ctx context.Context, opts Options) error {
	inner, cancel := context.WithCancel(context.Background())
	d := &Daemon{
		opts:         opts,
		ctx:          inner,
		cancel:       cancel,
		events:       newQueue(),
		kittyExited:  make(chan struct{}),
		focusWindows: map[windowKey]time.Time{},
		focusPanes:   map[paneKey]time.Time{},
		lastLaunch:   map[paneKey]time.Time{},
		windowPanes:  map[int]paneKey{},
		warnedLayout: map[int]bool{},
	}
	d.hub = newHub(func(key paneKey) {
		d.events.post(func() { d.repaintLater(key) })
	})
	defer d.cleanup()

	err := d.start()
	d.reportReady(err)
	if err != nil {
		return err
	}
	d.loop(ctx)
	return nil
}

// start brings everything up: runtime directory, socket, tmux connections and
// kitty.
func (d *Daemon) start() error {
	err := d.setupRuntime()
	if err != nil {
		return err
	}
	d.listener, err = net.Listen("unix", d.daemonSocket)
	if err != nil {
		return &cli.Error{Code: cli.CodeRuntimeDir, Detail: err.Error()}
	}
	go d.acceptLoop()

	err = d.connectTargets()
	if err != nil {
		return err
	}
	err = d.startKitty()
	if err != nil {
		return err
	}
	go d.tick(relistInterval, d.relistAll)
	go d.tick(saveInterval, d.saveAll)
	d.scheduleReconcile()
	return nil
}

// setupRuntime creates the private runtime directory under the first base
// that keeps socket paths short enough.
func (d *Daemon) setupRuntime() error {
	random := make([]byte, runtimeRandomSize)
	_, err := rand.Read(random)
	if err != nil {
		return &cli.Error{Code: cli.CodeRuntimeDir, Detail: err.Error()}
	}
	name := hex.EncodeToString(random)
	var lastErr error
	for _, base := range d.opts.Config.RuntimeBases {
		dir := filepath.Join(base, runtimeDirPrefix+strconv.Itoa(os.Getuid()), name)
		if len(filepath.Join(dir, kittySocketName)) > maxSocketPath {
			lastErr = fmt.Errorf("%s: path too long for a socket", dir)
			continue
		}
		err = os.MkdirAll(dir, runtimeDirMode)
		if err != nil {
			lastErr = err
			continue
		}
		// MkdirAll leaves an existing parent's mode alone.
		err = os.Chmod(filepath.Dir(dir), runtimeDirMode)
		if err != nil {
			lastErr = err
			continue
		}
		d.runtimeDir = dir
		d.daemonSocket = filepath.Join(dir, daemonSocketName)
		d.kittySocket = filepath.Join(dir, kittySocketName)
		return nil
	}
	return &cli.Error{Code: cli.CodeRuntimeDir, Detail: fmt.Sprint(lastErr)}
}

// connectTargets prepares and attaches every target and waits for each one's
// first window list.
func (d *Daemon) connectTargets() error {
	cfg := d.opts.Config
	err := tmux.EnsureControlDir(cfg.Home)
	if err != nil {
		return &cli.Error{Code: cli.CodeInternal, Detail: err.Error()}
	}
	for i, name := range cfg.Targets {
		t := &target{
			d: d,
			spec: tmux.Target{
				Index: i, Name: name, Session: cfg.Session, Lang: cfg.Lang,
				SSH: cfg.SSH, Home: cfg.Home, Batch: true,
			},
		}
		d.targets = append(d.targets, t)
	}
	for _, t := range d.targets {
		ctx, cancel := context.WithTimeout(d.ctx, prepareTimeout)
		prepared, err := t.spec.Prepare(ctx, tmux.ModeStart)
		cancel()
		if err != nil {
			return cli.TargetError(t.spec.Name, err)
		}
		log.Info().Str("target", t.spec.Name).Str("version", prepared.Version).
			Str("state", string(prepared.State)).Msg("target prepared")
		err = t.attach(prepared)
		if err != nil {
			return cli.TargetError(t.spec.Name, err)
		}
		listed := d.pump(func() bool { return t.listed || t.status != statusLive }, firstListTimeout)
		if !listed || t.status != statusLive || len(t.windows) == 0 {
			return &cli.Error{Code: cli.CodeSessionFailed, Target: t.spec.Name, Detail: "no window list from tmux"}
		}
	}
	return nil
}

// startKitty writes the overlay and session files, starts kitty with the
// first pane as its first window, and waits until remote control answers.
func (d *Daemon) startKitty() error {
	bin, err := findKitty()
	if err != nil {
		return &cli.Error{Code: cli.CodeKittyFailed, Detail: err.Error()}
	}
	var userConfigs []string
	_, err = os.Stat(d.opts.Config.KittyConf)
	if err == nil {
		userConfigs = append(userConfigs, d.opts.Config.KittyConf)
	}
	keys := kitty.DefaultKeys(runtime.GOOS)
	ctx, cancel := context.WithTimeout(d.ctx, kittyCallTimeout)
	closeTab, err := kitty.CloseTabKeys(ctx, bin, userConfigs)
	cancel()
	if err != nil {
		log.Warn().Err(err).Msg("cannot list close_tab keys; close tab gets no confirmation")
	}
	keys = keys.WithCloseTab(closeTab)
	log.Info().Strs("close_tab", keys.CloseTab).Msg("kitmux key bindings")
	overlay, err := kitty.RenderOverlay(d.opts.Bin, d.daemonSocket, keys)
	if err != nil {
		return &cli.Error{Code: cli.CodeKittyFailed, Detail: err.Error()}
	}
	overlayPath := filepath.Join(d.runtimeDir, overlayFileName)
	err = os.WriteFile(overlayPath, []byte(overlay), runtimeFileMode)
	if err != nil {
		return &cli.Error{Code: cli.CodeRuntimeDir, Detail: err.Error()}
	}

	first := d.targets[0]
	w := first.windows[0]
	pane := w.tree.Panes()[0]
	session := kitty.RenderSession(
		tabs.Title(first.spec.Name, w.index, w.name),
		paneVars(first, w, pane),
		d.paneCommand(first, pane),
	)
	sessionPath := filepath.Join(d.runtimeDir, sessionFileName)
	err = os.WriteFile(sessionPath, []byte(session), runtimeFileMode)
	if err != nil {
		return &cli.Error{Code: cli.CodeRuntimeDir, Detail: err.Error()}
	}

	configs := append(userConfigs, overlayPath)
	d.kittyCmd, err = kitty.Start(kitty.StartOptions{
		Bin: bin, Configs: configs, Socket: d.kittySocket, Session: sessionPath, Log: kittyLog{},
	})
	if err != nil {
		return &cli.Error{Code: cli.CodeKittyFailed, Detail: err.Error()}
	}
	go func() {
		_ = d.kittyCmd.Wait()
		close(d.kittyExited)
	}()
	d.kitty = kitty.NewClient(bin, d.kittySocket)

	key := paneKey{target: first.spec.Index, pane: pane}
	deadline := time.Now().Add(kittyStartTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-d.kittyExited:
			return &cli.Error{Code: cli.CodeKittyFailed, Detail: "kitty exited while starting"}
		case <-time.After(kittyPollInterval):
		}
		ctx, cancel := context.WithTimeout(d.ctx, kittyCallTimeout)
		windows, listErr := d.kitty.List(ctx)
		cancel()
		if listErr != nil {
			continue
		}
		v := newView(d, windows)
		found := v.panes[key]
		if len(found) > 0 {
			d.mainOS = found[0].os
			return nil
		}
	}
	return &cli.Error{Code: cli.CodeKittyFailed, Detail: "kitty did not answer on its remote-control socket"}
}

// loop is the main loop: it runs posted functions until the daemon stops.
func (d *Daemon) loop(ctx context.Context) {
	for !d.stopping {
		select {
		case <-ctx.Done():
			d.shutdown("signal", false)
		case <-d.kittyExited:
			d.shutdown("kitty exited", false)
		case <-d.events.wake:
			d.runPosted()
		}
	}
}

// runPosted runs everything in the queue.
func (d *Daemon) runPosted() {
	for _, fn := range d.events.take() {
		if d.stopping {
			return
		}
		fn()
	}
}

// pump runs posted functions until the condition holds or the timeout passes,
// and reports whether it held. It lets startup wait for replies without the
// main loop running yet.
func (d *Daemon) pump(until func() bool, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for !until() {
		select {
		case <-d.events.wake:
			d.runPosted()
		case <-deadline:
			return until()
		}
	}
	return true
}

// after runs fn on the main loop once the delay passed.
func (d *Daemon) after(delay time.Duration, fn func()) {
	time.AfterFunc(delay, func() { d.events.post(fn) })
}

// tick posts fn to the main loop at a fixed interval until the daemon stops.
func (d *Daemon) tick(interval time.Duration, fn func()) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			d.events.post(fn)
		case <-d.ctx.Done():
			return
		}
	}
}

// relistAll refreshes every live target's windows and looks at kitty again.
func (d *Daemon) relistAll() {
	for _, t := range d.targets {
		if t.status == statusLive {
			t.requestList()
		}
	}
	d.scheduleReconcile()
}

// saveAll runs the tmux-continuum save tick on every live target.
func (d *Daemon) saveAll() {
	for _, t := range d.targets {
		if t.status == statusLive {
			t.saveTick()
		}
	}
}

// scheduleReconcile queues one pass over kitty, shortly from now.
func (d *Daemon) scheduleReconcile() {
	d.scheduleReconcileIn(reconcileDelay)
}

// scheduleReconcileIn queues one pass over kitty after the delay, unless one
// is queued already.
func (d *Daemon) scheduleReconcileIn(delay time.Duration) {
	if d.reconcileQueued {
		return
	}
	d.reconcileQueued = true
	d.after(delay, func() {
		d.reconcileQueued = false
		d.reconcile()
	})
}

// repaintLater repaints a pane in full, if it still exists.
func (d *Daemon) repaintLater(key paneKey) {
	t := d.targets[key.target]
	if t.status == statusLive && t.hasPane(key.pane) {
		t.paint(key.pane, true)
	}
}

// shutdown stops the daemon. tmux is only detached from: no window or pane is
// killed on any path through here. quitKitty also ends the kitty instance,
// which is what happens when the last session ended.
func (d *Daemon) shutdown(reason string, quitKitty bool) {
	if d.stopping {
		return
	}
	log.Info().Str("reason", reason).Msg("daemon stopping")
	d.stopping = true
	d.cancel()
	if d.listener != nil {
		_ = d.listener.Close()
	}
	for _, t := range d.targets {
		t.shutdown()
	}
	d.hub.closeAll()
	if quitKitty && d.kittyCmd != nil {
		_ = d.kittyCmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-d.kittyExited:
		case <-time.After(kittyQuitGrace):
			_ = d.kittyCmd.Process.Kill()
		}
	}
}

// cleanup releases what start set up, on every way out of Run.
func (d *Daemon) cleanup() {
	d.shutdown("exit", false)
	if d.runtimeDir != "" {
		_ = os.RemoveAll(d.runtimeDir)
	}
}

// reportReady tells the launcher how startup went.
func (d *Daemon) reportReady(err error) {
	if d.opts.Ready == nil {
		return
	}
	_, _ = io.WriteString(d.opts.Ready, cli.EncodeReady(err)+"\n")
	_ = d.opts.Ready.Close()
}

// paneVars are the user variables of a pane's kitty window.
func paneVars(t *target, w *window, pane int) []string {
	return []string{
		kitty.VarTarget + "=" + t.spec.Name,
		kitty.VarWindow + "=" + w.id,
		kitty.VarPane + "=" + paneID(pane),
	}
}

// paneCommand is the command line kitty runs in a pane's window.
func (d *Daemon) paneCommand(t *target, pane int) []string {
	return []string{d.opts.Bin, SubcommandPane, d.daemonSocket, strconv.Itoa(t.spec.Index), paneID(pane)}
}

// findKitty locates the kitty executable.
func findKitty() (string, error) {
	path, err := exec.LookPath(kittyBinary)
	if err == nil {
		return path, nil
	}
	_, statErr := os.Stat(kittyMacOSBinary)
	if statErr == nil {
		return kittyMacOSBinary, nil
	}
	return "", err
}

// kittyLog writes kitty's own output to the daemon log.
type kittyLog struct{}

// Write logs one chunk of kitty output.
func (kittyLog) Write(p []byte) (int, error) {
	log.Info().Str("kitty", string(p)).Msg("kitty output")
	return len(p), nil
}
