// filosophy-daemon: standalone file-watcher process.
// Launched by the main app on first index; keeps running after the UI closes.
package main

import (
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"filosophy/daemon"
	"filosophy/shared"
)

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		log.Fatal("cannot get working directory:", err)
	}

	// Write PID so the main app can check/stop us.
	pidFile := filepath.Join(cwd, "filosophy-daemon.pid")
	os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0644)
	defer os.Remove(pidFile)

	// Log to the same file as the main app.
	if lf, err := os.OpenFile(filepath.Join(cwd, "filosophy.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
		log.SetOutput(lf)
	}

	// Read the directory to watch.
	pathFile := filepath.Join(cwd, "filosophy_path.txt")
	content, err := os.ReadFile(pathFile)
	if err != nil || len(strings.TrimSpace(string(content))) == 0 {
		log.Fatal("daemon: no watched directory configured (filosophy_path.txt missing or empty)")
	}
	baseDir := strings.TrimSpace(string(content))

	engine, err := shared.New(
		filepath.Join(cwd, "filosophy.db"),
		filepath.Join(cwd, "text"),
		filepath.Join(cwd, "image"),
	)
	if err != nil {
		log.Fatal("daemon: failed to initialize engine:", err)
	}
	defer engine.Close()

	d := daemon.NewDaemon(baseDir, engine)
	if err := d.Start(); err != nil {
		log.Fatal("daemon: failed to start:", err)
	}
	log.Printf("daemon: watching %s (pid %d)", baseDir, os.Getpid())

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig

	log.Println("daemon: shutting down")
	d.Stop()
}
