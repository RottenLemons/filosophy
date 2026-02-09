package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/kardianos/service"
	"github.com/syncthing/notify"
)

var logger service.Logger

const (
	// Track file/folder creation, deletion, and rename
	structureEventMask = notify.FileNotifyChangeFileName | notify.FileNotifyChangeDirName
	// Track content changes (size and last write)
	contentEventMask = notify.FileNotifyChangeSize | notify.FileNotifyChangeLastWrite
)

// shouldFilterPath returns true if the path should be ignored for content changes
func shouldFilterPath(path string) bool {
	parts := strings.Split(path, "\\")
	if len(parts) < 4 {
		return true
	}
	name := parts[3]
	// Filter out AppData, hidden files (starting with .), and ntuser.dat
	return name == "AppData" || strings.HasPrefix(name, ".") || strings.HasPrefix(strings.ToLower(name), "ntuser.dat")
}

// shouldFilterFolder returns true if the folder should be skipped
func shouldFilterFolder(name string) bool {
	return name == "AppData" || strings.HasPrefix(name, ".") || strings.HasPrefix(strings.ToLower(name), "ntuser")
}

// isFileTooLarge returns true if file is larger than 100MB
const maxFileSize = 100 * 1024 * 1024 // 100MB

func isFileTooLarge(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false // If we can't stat, don't filter
	}
	if info.IsDir() {
		return false
	}
	return info.Size() > maxFileSize
}

type program struct {
	exit                       chan struct{}
	structureChan, contentChan chan notify.EventInfo
	baseDir                    string
}

func (p *program) Start(s service.Service) error {
	var err error
	p.baseDir, err = os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}
	p.exit = make(chan struct{})
	// Channel for structure changes (create, delete, rename) - ALL files
	p.structureChan = make(chan notify.EventInfo, 500)
	// Channel for content changes - filtered files only
	p.contentChan = make(chan notify.EventInfo, 500)

	// Start should not block. Do the actual work async.
	go p.run()
	return nil
}
func (p *program) run() {
	// Do work here
	if err := notify.Watch(p.baseDir, p.structureChan, structureEventMask); err != nil {
		fmt.Println("Error watching structure:", err)
		return
	}
	defer notify.Stop(p.structureChan)

	entries, err := os.ReadDir(p.baseDir)
	if err != nil {
		fmt.Println("Error reading base directory:", err)
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if shouldFilterFolder(name) {
			continue
		}

		// Set up recursive watch for this folder
		watchPath := filepath.Join(p.baseDir, name, "...")
		if err := notify.Watch(watchPath, p.contentChan, contentEventMask); err != nil {
			fmt.Println("Error watching content for", name, ":", err)
			continue
		}
		fmt.Println("Watching:", watchPath)
	}

	// Also watch the base directory itself (non-recursive) for content changes
	if err := notify.Watch(p.baseDir, p.contentChan, contentEventMask); err != nil {
		fmt.Println("Error watching base directory content:", err)
	} else {
		fmt.Println("Watching base:", p.baseDir)
	}
	defer notify.Stop(p.contentChan)

	// Goroutine for structure changes (create, delete, rename) - no filtering
	go func() {
		for ei := range p.structureChan {
			fmt.Println("[STRUCTURE]", ei.Event(), ei.Path())
		}
	}()

	// Goroutine for content changes - already filtered by folder selection, skip files > 100MB
	go func() {
		for ei := range p.contentChan {
			if !isFileTooLarge(ei.Path()) && !shouldFilterPath(ei.Path()) {
				fmt.Println("[CONTENT]", ei.Event(), ei.Path())
			}
		}
	}()
	<-p.exit
}
func (p *program) Stop(s service.Service) error {
	// Stop should not block. Return with a few seconds.
	close(p.exit)
	close(p.structureChan)
	close(p.contentChan)
	return nil
}

func main() {
	svcFlag := flag.String("service", "", "Control the system service.")
	flag.Parse()
	options := make(service.KeyValue)
	options["Restart"] = "on-success"
	options["SuccessExitStatus"] = "1 2 8 SIGKILL"
	svcConfig := &service.Config{
		Name:        "GoServiceTest",
		DisplayName: "Go Service Test",
		Description: "This is a test Go service.",
		Option:      options,
	}

	prg := &program{}
	s, err := service.New(prg, svcConfig)
	if err != nil {
		fmt.Println(err)
	}
	errs := make(chan error, 5)
	logger, err = s.Logger(errs)
	if err != nil {
		logger.Error(err)
	}

	go func() {
		for {
			err := <-errs
			if err != nil {
				log.Print(err)
			}
		}
	}()

	if len(*svcFlag) != 0 {
		err := service.Control(s, *svcFlag)
		if err != nil {
			log.Printf("Valid actions: %q\n", service.ControlAction)
			log.Fatal(err)
		}
		return
	}

	err = s.Run()
	if err != nil {
		fmt.Println(err)
	}
}

// func main() {
// 	done := make(chan struct{})
// 	baseDir, err := os.UserHomeDir()
// if err != nil {
//     log.Fatal( err )
// }

// 	// Channel for structure changes (create, delete, rename) - ALL files
// 	structureChan := make(chan notify.EventInfo, 500)
// 	// Channel for content changes - filtered files only
// 	contentChan := make(chan notify.EventInfo, 500)

// 	// Watch for structure changes (create, delete, rename)
// 	if err := notify.Watch("C:/Users/Mahir/", structureChan, structureEventMask); err != nil {
// 		fmt.Println("Error watching structure:", err)
// 		return
// 	}
// 	defer notify.Stop(structureChan)

// 	// Read all directories in base folder and set up recursive watches for non-hidden, non-AppData folders
// 	entries, err := os.ReadDir(baseDir)
// 	if err != nil {
// 		fmt.Println("Error reading base directory:", err)
// 		return
// 	}

// 	for _, entry := range entries {
// 		if !entry.IsDir() {
// 			continue
// 		}
// 		name := entry.Name()
// 		if shouldFilterFolder(name) {
// 			continue
// 		}

// 		// Set up recursive watch for this folder
// 		watchPath := filepath.Join(baseDir, name, "...")
// 		if err := notify.Watch(watchPath, contentChan, contentEventMask); err != nil {
// 			fmt.Println("Error watching content for", name, ":", err)
// 			continue
// 		}
// 		fmt.Println("Watching:", watchPath)
// 	}

// 	// Also watch the base directory itself (non-recursive) for content changes
// 	if err := notify.Watch(baseDir, contentChan, contentEventMask); err != nil {
// 		fmt.Println("Error watching base directory content:", err)
// 	} else {
// 		fmt.Println("Watching base:", baseDir)
// 	}
// 	defer notify.Stop(contentChan)

// 	// Goroutine for structure changes (create, delete, rename) - no filtering
// 	go func() {
// 		for ei := range structureChan {
// 			fmt.Println("[STRUCTURE]", ei.Event(), ei.Path())
// 		}
// 	}()

// 	// Goroutine for content changes - already filtered by folder selection, skip files > 100MB
// 	go func() {
// 		for ei := range contentChan {
// 			if !isFileTooLarge(ei.Path()) && !shouldFilterPath(ei.Path()) {
// 				fmt.Println("[CONTENT]", ei.Event(), ei.Path())
// 			}
// 		}
// 	}()

// 	<-done
// }

// Search C:\Users\Mahir\AppData\Roaming\Microsoft\Windows\Start Menu\Programs for program exe and search userprofile. thats it
// DO NOT READ APPDATA STUFF, and also windows stuff, just add the path
