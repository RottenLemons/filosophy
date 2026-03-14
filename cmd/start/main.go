package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

const indexDoneFile = ".indexed"

func main() {
	// Resolve project root (two levels up from cmd/start/)
	cwd, err := os.Getwd()
	if err != nil {
		log.Fatal("cannot get working directory:", err)
	}
	root, _ := filepath.Abs(filepath.Join(cwd, "..", ".."))

	// Kill any leftover sidecar from a previous run
	exec.Command("powershell", "-NoProfile", "-Command",
		`Get-CimInstance Win32_Process | Where-Object { $_.CommandLine -like '*sidecar.py*' -and $_.Name -eq 'python.exe' } | ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }`).Run()

	// 1. Start sidecar.py (-u = unbuffered stdout so "Listening" arrives immediately)
	sidecar := exec.Command("python", "-u", "sidecar.py")
	sidecar.Dir = root
	sidecar.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
	sidecarOut, err := sidecar.StdoutPipe()
	if err != nil {
		log.Fatal("failed to pipe sidecar stdout:", err)
	}
	sidecar.Stderr = os.Stderr
	if err := sidecar.Start(); err != nil {
		log.Fatal("failed to start sidecar:", err)
	}
	fmt.Println("[orchestrator] sidecar started, waiting for ready signal...")

	// Wait for "Listening" in sidecar's stdout
	scanner := bufio.NewScanner(sidecarOut)
	ready := false
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Println("[sidecar]", line)
		if strings.Contains(line, "Listening") {
			ready = true
			break
		}
	}
	if !ready {
		log.Fatal("sidecar exited before printing 'Listening'")
	}

	// Keep draining sidecar stdout in background so it doesn't block
	go func() {
		for scanner.Scan() {
			fmt.Println("[sidecar]", scanner.Text())
		}
	}()

	// 2. Start daemon first — it begins collecting file change events immediately
	//    so nothing is missed while the initial index runs.
	daemon := exec.Command("go", "run", ".")
	daemon.Dir = filepath.Join(root, "daemon")
	daemon.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
	daemon.Stdout = os.Stdout
	daemon.Stderr = os.Stderr
	if err := daemon.Start(); err != nil {
		log.Fatal("failed to start daemon:", err)
	}
	fmt.Println("[orchestrator] daemon started (collecting changes)")

	// 3. Start main — with --index on first run, otherwise just search CLI.
	//    main.go automatically falls through to the search loop after indexing.
	indexMarker := filepath.Join(root, indexDoneFile)
	var mainCmd *exec.Cmd
	if _, err := os.Stat(indexMarker); os.IsNotExist(err) {
		fmt.Println("[orchestrator] first run — indexing then search...")
		mainCmd = exec.Command("go", "run", ".", "--index")
	} else {
		fmt.Println("[orchestrator] already indexed, starting search...")
		mainCmd = exec.Command("go", "run", ".")
	}
	mainCmd.Dir = root
	mainCmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
	mainCmd.Stdout = os.Stdout
	mainCmd.Stderr = os.Stderr
	mainCmd.Stdin = os.Stdin
	if err := mainCmd.Start(); err != nil {
		log.Fatal("failed to start main:", err)
	}

	// Mark index as done once main starts (it will index before entering search loop)
	if _, err := os.Stat(indexMarker); os.IsNotExist(err) {
		os.WriteFile(indexMarker, []byte(""), 0644)
	}

	// Handle Ctrl+C — kill all children
	children := []*exec.Cmd{sidecar, daemon, mainCmd}
	cleanup := func() {
		for _, cmd := range children {
			if cmd.Process != nil {
				exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprint(cmd.Process.Pid)).Run()
			}
		}
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	done := make(chan struct{})
	go func() {
		mainCmd.Wait()
		close(done)
	}()

	select {
	case <-sigCh:
		fmt.Println("\n[orchestrator] shutting down everything...")
		cleanup()
		return
	case <-done:
		// Search CLI exited — keep sidecar + daemon running in background
		fmt.Println("[orchestrator] search CLI exited. Sidecar + daemon still running.")
		fmt.Println("[orchestrator] Press Ctrl+C to stop all background processes.")
	}

	// Block until Ctrl+C to keep orchestrator alive for cleanup
	<-sigCh
	fmt.Println("\n[orchestrator] shutting down everything...")
	cleanup()
}
