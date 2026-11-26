// Command magnetar is a fast, secure BitTorrent client with a modern interface.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/nikolap994/magnetar/internal/auth"
	"github.com/nikolap994/magnetar/internal/config"
	"github.com/nikolap994/magnetar/internal/desktop"
	"github.com/nikolap994/magnetar/internal/engine"
	"github.com/nikolap994/magnetar/internal/server"
	"github.com/nikolap994/magnetar/internal/update"
	"github.com/nikolap994/magnetar/web"
)

func main() {
	if err := run(); err != nil {
		fatal(err)
	}
}

func usage() {
	fmt.Fprintf(flag.CommandLine.Output(), `Magnetar %s - fast, secure BitTorrent client

Usage:
  magnetar [flags] [magnet-link | file.torrent ...]
  magnetar passwd       set or change the web UI password
  magnetar register     open magnet links and .torrent files with Magnetar
  magnetar unregister   remove those file associations

Flags:
`, engine.Version)
	flag.PrintDefaults()
}

// app bundles what the desktop helpers need.
type app struct {
	settings config.Settings
	keys     *auth.Keyring
	base     string // http://127.0.0.1:port
	profile  string // private browser profile for the app window
	noUI     bool
}

func (a *app) launchURL(magnet string) string {
	u := a.base + "/auth/launch?token=" + url.QueryEscape(a.keys.NewLaunchToken())
	if magnet != "" {
		u += "&magnet=" + url.QueryEscape(magnet)
	}
	return u
}

// openMain shows the main window.
func (a *app) openMain() error {
	if a.noUI {
		return nil
	}
	return desktop.OpenWindow(a.launchURL(""), a.profile, desktop.MainSize, a.settings.AppWindow)
}

// openAddDialog opens the main window with the "add torrent" dialog for a
// magnet link, so the user can pick files and a folder like in qBittorrent.
func (a *app) openAddDialog(magnet string) error {
	if a.noUI {
		return errors.New("no UI")
	}
	return desktop.OpenWindow(a.launchURL(magnet), a.profile, desktop.MainSize, a.settings.AppWindow)
}

// showUI keeps exactly one window: if one is open it is brought to the front
// (with the add dialog for a magnet link); otherwise a window is opened.
func (a *app) showUI(srv *server.Server, eng *engine.Engine, magnet string) {
	if a.noUI {
		return
	}
	if srv.WindowConnected() {
		if magnet != "" {
			eng.Broadcast(engine.Event{Type: "ui-add", Message: magnet})
		}
		eng.Broadcast(engine.Event{Type: "ui-focus"})
		desktop.FocusWindow()
		return
	}
	if magnet != "" {
		a.openAddDialog(magnet)
		return
	}
	a.openMain()
}

func run() error {
	var (
		dirFlag   = flag.String("dir", "", "config/data directory (default: OS user config dir)")
		noBrowser = flag.Bool("no-browser", false, "do not open the interface on start")
		headless  = flag.Bool("headless", false, "server mode: no window, no tray icon")
		port      = flag.Int("port", 0, "override web UI port")
		verbose   = flag.Bool("verbose", false, "enable debug logging")
		version   = flag.Bool("version", false, "print version and exit")
		afterUpd  = flag.Bool("after-update", false, "internal: started by the updater; wait for the old version to exit")
	)
	flag.Usage = usage
	flag.Parse()
	if *version {
		fmt.Println("magnetar", engine.Version)
		return nil
	}

	cfgDir, dataDir, err := config.Dirs(*dirFlag)
	if err != nil {
		return err
	}
	store, err := config.Load(cfgDir)
	if err != nil {
		return err
	}
	keys, err := auth.LoadKeyring(cfgDir)
	if err != nil {
		return err
	}

	switch flag.Arg(0) {
	case "passwd":
		return setPassword(store)
	case "register":
		return register()
	case "unregister":
		return unregister()
	}

	logger, closeLog := setupLogging(dataDir, *verbose)
	defer closeLog()

	settings := store.Get()
	if *port != 0 {
		settings.WebPort = *port
	}
	a := &app{
		settings: settings,
		keys:     keys,
		base:     "http://" + net.JoinHostPort(localHost(settings.WebHost), strconv.Itoa(settings.WebPort)),
		profile:  filepath.Join(dataDir, "window"),
		noUI:     *headless,
	}
	webAddr := net.JoinHostPort(settings.WebHost, strconv.Itoa(settings.WebPort))

	if *afterUpd {
		update.CleanupOld()
	}
	ln, err := listenWeb(webAddr, *afterUpd)
	if err != nil {
		if isAddrInUse(err) {
			// Another instance is (probably) running: hand it our arguments and show it.
			return handoff(a, flag.Args(), !*noBrowser)
		}
		return fmt.Errorf("listen on %s: %w", webAddr, err)
	}

	eng, err := engine.New(settings, dataDir, logger)
	if err != nil {
		ln.Close()
		return err
	}

	// Keep saved settings in sync when the speed schedule toggles alternative limits.
	eng.SetSettingsHook(func(s config.Settings) {
		cur := store.Get()
		cur.AltSpeedEnabled = s.AltSpeedEnabled
		if err := store.Update(cur); err != nil {
			logger.Warn("save schedule state", "err", err)
		}
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Restart with the same flags after an in-place update.
	flag.Visit(func(f *flag.Flag) {
		if f.Name != "after-update" {
			update.RestartArgs = append(update.RestartArgs, "-"+f.Name+"="+f.Value.String())
		}
	})
	updater := update.New(engine.Version, logger, func() bool { return store.Get().AutoUpdateCheck }, stop)
	go updater.Run(ctx)

	var srv *server.Server
	srv = server.New(eng, store, keys, web.FS(), logger, server.Options{
		Port:      settings.WebPort,
		Shutdown:  stop,
		Register:  register,
		AutoStart: desktop.SetAutostart,
		Updater:   updater,
		ShowUI:    func(magnet string) { a.showUI(srv, eng, magnet) },
	})
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ctx, ln) }()

	if !*headless {
		go desktopFeedback(ctx, eng, store)
	}

	prompted := false
	for _, arg := range flag.Args() {
		if isMagnet(arg) && settings.ShowAddDialog && !*headless && !prompted {
			if err := a.openAddDialog(arg); err == nil {
				prompted = true
				continue
			}
		}
		if err := addArg(eng, arg); err != nil && !errors.Is(err, engine.ErrExists) {
			logger.Warn("could not add", "arg", arg, "err", err)
		}
	}

	logger.Info("Magnetar is running", "ui", a.base, "data", dataDir)
	if !*headless && hasConsole() {
		fmt.Printf("\n  Magnetar %s is running\n  Open: %s\n  (sign-in link valid for 2 minutes; press Ctrl+C to quit)\n\n",
			engine.Version, a.launchURL(""))
	}
	if settings.OpenBrowser && !*noBrowser && !prompted {
		if err := a.openMain(); err != nil {
			logger.Warn("could not open the interface", "err", err)
		}
	}

	if settings.TrayIcon && !*headless {
		go func() {
			select {
			case <-ctx.Done():
			case err = <-errc:
				stop()
			}
			desktop.StopTray()
		}()
		desktop.RunTray(desktop.TrayActions{
			Open:     func() { a.showUI(srv, eng, "") },
			IsAlt:    func() bool { return store.Get().AltSpeedEnabled },
			AltSpeed: func(on bool) error { return setAltSpeed(eng, store, on) },
			PauseAll: func() { eng.PauseAll() },
			ResumeAll: func() {
				if err := eng.ResumeAll(); err != nil {
					logger.Warn("resume all", "err", err)
				}
			},
			Quit: stop,
		})
		<-ctx.Done()
	} else {
		select {
		case <-ctx.Done():
		case err = <-errc:
		}
	}

	logger.Info("shutting down")
	stop()
	// Close the app window too: ask it nicely, then end the browser process
	// that uses Magnetar's private profile.
	eng.AnnounceShutdown()
	time.Sleep(700 * time.Millisecond)
	desktop.CloseWindows()
	if cerr := eng.Close(); cerr != nil {
		logger.Warn("close engine", "err", cerr)
	}
	return err
}

// desktopFeedback shows native notifications for finished downloads and keeps
// the tray tooltip updated with live speeds.
func desktopFeedback(ctx context.Context, eng *engine.Engine, store *config.Store) {
	events, cancel := eng.Subscribe()
	defer cancel()
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			if ev.Type == "completed" && store.Get().Notifications {
				desktop.Notify("Download complete", ev.Name)
			}
		case <-tick.C:
			st := eng.Stats()
			tip := fmt.Sprintf("Magnetar\n↓ %s  ↑ %s", humanRate(st.DownRate), humanRate(st.UpRate))
			if st.Active > 0 {
				tip += fmt.Sprintf(" · %d active", st.Active)
			}
			desktop.SetTrayTooltip(tip)
		}
	}
}

func humanRate(b int64) string {
	units := []string{"B/s", "KiB/s", "MiB/s", "GiB/s"}
	v, i := float64(b), 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d %s", b, units[0])
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}

// listenWeb binds the web UI. Right after an update the previous version may
// still be shutting down, so keep retrying for a while instead of handing off.
func listenWeb(addr string, afterUpdate bool) (net.Listener, error) {
	deadline := time.Now().Add(30 * time.Second)
	for {
		ln, err := net.Listen("tcp", addr)
		if err == nil || !afterUpdate || !isAddrInUse(err) || time.Now().After(deadline) {
			return ln, err
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func setAltSpeed(eng *engine.Engine, store *config.Store, on bool) error {
	s := store.Get()
	s.AltSpeedEnabled = on
	if err := eng.ApplySettings(s); err != nil {
		return err
	}
	return store.Update(s)
}

func localHost(h string) string {
	if h == "0.0.0.0" || h == "::" || h == "" {
		return "127.0.0.1"
	}
	return h
}

func isMagnet(s string) bool { return strings.HasPrefix(strings.ToLower(s), "magnet:") }

func isAddrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE) || strings.Contains(strings.ToLower(err.Error()), "address already in use") ||
		strings.Contains(err.Error(), "Only one usage of each socket address")
}

func addArg(eng *engine.Engine, arg string) error {
	if isMagnet(arg) {
		_, err := eng.Add(engine.AddOptions{Magnet: arg})
		return err
	}
	b, err := readTorrentFile(arg)
	if err != nil {
		return err
	}
	_, err = eng.Add(engine.AddOptions{TorrentFile: b})
	return err
}

func readTorrentFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, engine.MaxTorrentFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(b) > engine.MaxTorrentFileSize {
		return nil, errors.New("torrent file is too large")
	}
	return b, nil
}

// handoff sends magnets/torrents to the running instance and shows its UI.
func handoff(a *app, args []string, showUI bool) error {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(a.base + "/api/session")
	if err != nil {
		return fmt.Errorf("web port is in use by another program (%v); change it with --port", err)
	}
	resp.Body.Close()

	// The running instance owns the single window: ask it to show the add
	// dialog (or itself) instead of opening another window from here.
	desktop.AllowFocus()
	showOnly := func(magnet string) error {
		b, _ := json.Marshal(map[string]string{"magnet": magnet})
		req, _ := http.NewRequest(http.MethodPost, a.base+"/api/ui/open", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Magnetar-Request", "1")
		req.Header.Set("Authorization", "Launch "+a.keys.NewLaunchToken())
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("running instance: %s", resp.Status)
		}
		return nil
	}

	prompted := false
	for _, arg := range args {
		if isMagnet(arg) && a.settings.ShowAddDialog {
			if err := showOnly(arg); err == nil {
				prompted = true
				continue
			}
		}
		var body bytes.Buffer
		var ctype string
		if isMagnet(arg) {
			json.NewEncoder(&body).Encode(map[string]string{"magnet": arg})
			ctype = "application/json"
		} else {
			data, err := readTorrentFile(arg)
			if err != nil {
				fmt.Fprintln(os.Stderr, "skip", arg+":", err)
				continue
			}
			ctype, err = multipartTorrent(&body, filepath.Base(arg), data)
			if err != nil {
				return err
			}
		}
		req, _ := http.NewRequest(http.MethodPost, a.base+"/api/torrents", &body)
		req.Header.Set("Content-Type", ctype)
		req.Header.Set("X-Magnetar-Request", "1")
		req.Header.Set("Authorization", "Launch "+a.keys.NewLaunchToken())
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			fmt.Fprintf(os.Stderr, "running instance rejected %s: %s\n", arg, resp.Status)
		}
	}
	if showUI && !prompted {
		return showOnly("")
	}
	return nil
}

func setPassword(store *config.Store) error {
	fmt.Print("New web UI password (min 8 chars, empty to remove): ")
	pw, err := readSecret()
	if err != nil {
		return err
	}
	s := store.Get()
	if pw == "" {
		s.PasswordHash = ""
		if err := store.Update(s); err != nil {
			return err
		}
		fmt.Println("Password removed.")
		return nil
	}
	fmt.Print("Repeat password: ")
	pw2, err := readSecret()
	if err != nil {
		return err
	}
	if pw != pw2 {
		return errors.New("passwords do not match")
	}
	h, err := auth.HashPassword(pw)
	if err != nil {
		return err
	}
	s.PasswordHash = h
	if err := store.Update(s); err != nil {
		return err
	}
	fmt.Println("Password saved. Restart Magnetar if it is running.")
	return nil
}

func readSecret() (string, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		b, err := term.ReadPassword(fd)
		fmt.Println()
		return string(b), err
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func setupLogging(dataDir string, verbose bool) (*slog.Logger, func()) {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	var w io.Writer = os.Stderr
	closer := func() {}
	if f, err := os.OpenFile(filepath.Join(dataDir, "magnetar.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		if fi, err := f.Stat(); err == nil && fi.Size() > 10<<20 {
			f.Truncate(0)
		}
		if hasConsole() {
			w = io.MultiWriter(os.Stderr, f)
		} else {
			w = f
		}
		closer = func() {
			if err := f.Sync(); err != nil {
				fmt.Fprintln(os.Stderr, "magnetar: flush log:", err)
			}
			if err := f.Close(); err != nil {
				fmt.Fprintln(os.Stderr, "magnetar: close log:", err)
			}
		}
	}
	h := slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})
	return slog.New(&levelFilter{Handler: h, min: level}), closer
}

// levelFilter quiets the BitTorrent library's info chatter unless verbose.
type levelFilter struct {
	slog.Handler
	min slog.Level
}

func (l *levelFilter) Enabled(ctx context.Context, lv slog.Level) bool {
	return lv >= l.min && l.Handler.Enabled(ctx, lv)
}

func (l *levelFilter) WithAttrs(attrs []slog.Attr) slog.Handler {
	for _, a := range attrs {
		if a.Key == "component" && a.Value.String() == "bittorrent" && l.min > slog.LevelDebug {
			return &levelFilter{Handler: l.Handler.WithAttrs(attrs), min: slog.LevelWarn}
		}
	}
	return &levelFilter{Handler: l.Handler.WithAttrs(attrs), min: l.min}
}

func (l *levelFilter) WithGroup(name string) slog.Handler {
	return &levelFilter{Handler: l.Handler.WithGroup(name), min: l.min}
}

func logoSVG() []byte {
	b, err := fs.ReadFile(web.FS(), "logo.svg")
	if err != nil {
		return nil
	}
	return b
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "magnetar:", err)
	showFatal(err)
	os.Exit(1)
}
