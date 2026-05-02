// filosophy-daemon: standalone file-watcher process.
// Launched by the main app on first index; keeps running after the UI closes.
package main

import (
	_ "embed"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"filosophy/daemon"
	"filosophy/shared"

	"github.com/getlantern/systray"
)

//go:embed icon.ico
var trayIcon []byte

// Package-level state shared between main and tray callbacks.
var (
	daemonInstance *daemon.Daemon
	engineInstance *shared.Engine
	pidFilePath    string
)

// getHomeSubdirs mirrors the logic in main.go — returns non-hidden, non-system
// top-level subdirectories of the home directory.
func getHomeSubdirs(home string) []string {
	entries, err := os.ReadDir(home)
	if err != nil {
		return nil
	}
	skip := map[string]bool{"appdata": true}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") || skip[strings.ToLower(name)] {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		log.Fatal("cannot get working directory:", err)
	}

	// Write PID so the main app can check/stop us.
	pidFilePath = filepath.Join(cwd, "filosophy-daemon.pid")
	os.WriteFile(pidFilePath, []byte(strconv.Itoa(os.Getpid())), 0644)

	// Log to the same file as the main app.
	if lf, err := os.OpenFile(filepath.Join(cwd, "filosophy.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
		log.SetOutput(lf)
	}

	// Compute which home subdirectories to watch (same logic as main.go).
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatal("daemon: cannot determine home directory:", err)
	}
	config := shared.LoadConfig(cwd)
	config.EnsureDefaultIncludedDirs(home)
	subdirs := getHomeSubdirs(home)
	var baseDirs []string
	seen := make(map[string]bool)
	add := func(dir string) {
		key := strings.ToLower(filepath.Clean(dir))
		if key == "" || seen[key] || config.IsExcluded(dir) {
			return
		}
		seen[key] = true
		baseDirs = append(baseDirs, dir)
	}
	for _, dir := range shared.DefaultIndexedDirs(home) {
		if config.IsIncluded(dir) {
			add(dir)
		}
	}
	for _, d := range config.GetExtraDirs() {
		add(d)
	}
	for _, name := range subdirs {
		p := filepath.Join(home, name)
		if config.IsIncluded(p) {
			add(p)
		}
	}

	engine, err := shared.New(
		filepath.Join(cwd, "filosophy.db"),
		filepath.Join(cwd, "text"),
		filepath.Join(cwd, "image"),
		shared.HardwareConfig{},
	)
	if err != nil {
		log.Fatal("daemon: failed to initialize engine:", err)
	}
	engineInstance = engine

	d := daemon.NewDaemon(baseDirs, engine)
	daemonInstance = d
	if err := d.Start(); err != nil {
		log.Fatal("daemon: failed to start:", err)
	}
	log.Printf("daemon: watching %v (pid %d)", baseDirs, os.Getpid())

	// Forward OS signals (SIGINT/SIGTERM) to systray.Quit so the tray
	// onExit callback handles cleanup uniformly.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		systray.Quit()
	}()

	// systray.Run blocks until systray.Quit() is called.
	systray.Run(onTrayReady, onTrayExit)
}

// onTrayReady is called by systray once the tray icon is initialised.
func onTrayReady() {
	systray.SetIcon(trayIcon)
	systray.SetTooltip("Filosophy")

	mQuit := systray.AddMenuItem("Exit", "Stop Filosophy daemon")

	go func() {
		<-mQuit.ClickedCh
		systray.Quit()
	}()
}

// onTrayExit is called by systray after Quit() — performs clean shutdown.
func onTrayExit() {
	log.Println("daemon: shutting down")

	// Kill the Wails app if it is still running.
	cwd := filepath.Dir(pidFilePath)
	if data, err := os.ReadFile(filepath.Join(cwd, "filosophy-app.pid")); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
			if proc, err := os.FindProcess(pid); err == nil {
				proc.Kill()
			}
		}
	}

	if daemonInstance != nil {
		daemonInstance.Stop()
	}
	if engineInstance != nil {
		engineInstance.Close()
	}
	os.Remove(pidFilePath)
}
