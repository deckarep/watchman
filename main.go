package main

import (
	"encoding/json"
	"fmt"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/fsnotify/fsnotify"
	"github.com/gen2brain/beeep"
	"github.com/google/uuid"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Schedule struct {
	PrimaryInterval string  `json:"primary_interval"`
	MaxWorkers      int     `json:"max_workers"`
	Entries         []Entry `json:"entries"`
}

type Entry struct {
	Name     string   `json:"name"`
	Interval string   `json:"interval"`
	Command  string   `json:"command"`
	Args     []string `json:"args"`
	Folder   string   `json:"folder"`

	FromExt string `json:"from_ext"`
	ToExt   string `json:"to_ext"`
}

var schedule = func() Schedule {
	// 0. Load configuration
	b, err := os.ReadFile("config.json")
	if err != nil {
		log.Fatal("failed to read configuration file!")
	}

	var mySchedule Schedule
	err = json.Unmarshal(b, &mySchedule)
	if err != nil {
		log.Fatal("failed to unmarshal config file", err)
	}
	return mySchedule
}()

type WorkItem struct {
	OriginalFile string
	TempFile     string
	DestFile     string
	Command      string
	Args         []string
}

var sigs = make(chan os.Signal, 1)
var dispatchChannel = make(chan WorkItem)
var wg sync.WaitGroup
var processingSet = mapset.NewSet[string]()

func worker() {
	for wrk := range dispatchChannel {
		//fmt.Println(wrk.Command, strings.Join(wrk.Args, " "))
		_, err := exec.Command(wrk.Command, wrk.Args...).Output()
		if err != nil {
			switch e := err.(type) {
			case *exec.Error:
				log.Fatal("failed to execute this command!:", err)
			case *exec.ExitError:
				fmt.Println("command exit rc =", e.ExitCode())
			}
		}

		// Simulate time.
		time.Sleep(time.Second * 5)

		// Cleanup
		processingSet.Remove(wrk.OriginalFile)
		err = os.Rename(wrk.TempFile, wrk.DestFile)
		if err != nil {
			log.Fatal("Failed to move file: ", wrk.TempFile)
		}

		// Show completed notification...
		doNotification("File Completed: " + filepath.Base(wrk.DestFile) + " ✅")
	}

	fmt.Println("worker shutting down...")
	wg.Done()
}

func getHomePath(path string) string {
	usr, _ := user.Current()
	dir := usr.HomeDir

	if path == "~" {
		// In case of "~", which won't be caught by the "else if"
		path = dir
	} else if strings.HasPrefix(path, "~/") {
		// Use strings.HasPrefix so we don't match paths like
		// "/something/~/something/"
		path = filepath.Join(dir, path[2:])
	}

	return path
}

func doNotification(body string) {
	err := beeep.Notify("Watchman Notification", body, "assets/information.png")
	if err != nil {
		log.Println("Failed to show notification with err: ", err.Error())
	}
}

func main() {
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal("failed to create new fsnotify watcher")
	}
	defer watcher.Close()

	// 1. create all workers
	instantiateWorkers()

	// 2. start listening for events
	go func() {
		for {
			select {
			case evt, ok := <-watcher.Events:
				if !ok {
					return
				}

				// Only concerned with Created files of a specific type.
				if evt.Has(fsnotify.Create) {
					err := handleEvent(evt.Name)
					if err != nil {
						log.Println("Create handleEvent failed with err: ", err.Error())
					}
				}

			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Println("watcher error: ", err)
			}
		}
	}()

	// 3. Register to relevant directory notifications.
	for _, entry := range schedule.Entries {
		path := getHomePath(entry.Folder)
		err := watcher.Add(path)
		if err != nil {
			log.Fatalf("failed to subscribe to path: %s with err: %s\n", path, err)
		}
	}

	// 4. wait for proper shutdown
	waitShutdown()
}

func instantiateWorkers() {
	for i := 0; i < schedule.MaxWorkers; i++ {
		wg.Add(1)
		go worker()
	}
	log.Printf("Created %d workers...\n", schedule.MaxWorkers)
}

func handleEvent(eventFile string) error {
	for _, entry := range schedule.Entries {
		path := getHomePath(entry.Folder)
		if path == filepath.Dir(eventFile) {
			fullFilePath := eventFile
			if strings.HasSuffix(fullFilePath, entry.FromExt) {
				newFilePath := strings.Replace(fullFilePath, entry.FromExt, entry.ToExt, -1)
				tempFilePath := filepath.Join(path, uuid.New().String()+entry.ToExt)

				if processingSet.Contains(fullFilePath) {
					fmt.Printf("File already in progress: %s (started)\n", fullFilePath)
					continue
				}

				// If we don't already have the new file, let's generate.
				if _, err := os.Stat(newFilePath); err != nil {

					// Copy over and format args with their respective replacements
					newArgs := make([]string, len(entry.Args))
					for i, a := range entry.Args {
						if a == "{SOURCE_FILE}" {
							newArgs[i] = fullFilePath
						} else if a == "{DEST_FILE}" {
							newArgs[i] = tempFilePath
						} else {
							newArgs[i] = a
						}
					}

					wi := WorkItem{
						OriginalFile: fullFilePath,
						TempFile:     tempFilePath,
						DestFile:     newFilePath,
						Command:      entry.Command,
						Args:         newArgs,
					}

					fmt.Printf("File generating: %s -> .mp4 (processing)\n", fullFilePath)
					processingSet.Add(fullFilePath)
					dispatchChannel <- wi
				} else {
					fmt.Printf("File exists: %s (skipping)\n", newFilePath)
				}
			}
		}
	}

	return nil
}

func waitShutdown() {
	fmt.Println("waiting on signal")
	<-sigs

	fmt.Println("closing dispatch channel")
	close(dispatchChannel)
	fmt.Println("Signal received...")

	wg.Wait()
	fmt.Println("Workers shutdown...")
	fmt.Println("Exiting.")
}
