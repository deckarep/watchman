/*
Open Source Initiative OSI - The MIT License (MIT):Licensing

The MIT License (MIT)
Copyright (c) 2023 Ralph Caraveo (deckarep@gmail.com)

Permission is hereby granted, free of charge, to any person obtaining a copy of
this software and associated documentation files (the "Software"), to deal in
the Software without restriction, including without limitation the rights to
use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies
of the Software, and to permit persons to whom the Software is furnished to do
so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/charmbracelet/log"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/fsnotify/fsnotify"
	"github.com/gen2brain/beeep"
	"github.com/google/uuid"
	"github.com/stephen-fox/launchctlutil"

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

const (
	finalExeName = "watchman"

	applicationSupportPath = "~/Library/Application Support/"
	launchDLabelTemplate   = "com.%s.scripts.go.watchman.svc"
)

var (
	launchDLabel = func() string {
		usr, err := user.Current()
		if err != nil {
			log.Fatal("failed to get the current user with err: ", err)
		}
		return fmt.Sprintf(launchDLabelTemplate, usr.Username)
	}()
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

var (
	scheduleExists = false
)

var schedule = func() *Schedule {
	absWorkingDirPath := filepath.Join(getHomePath(applicationSupportPath), launchDLabel)

	// 0. Load configuration
	b, err := os.ReadFile(filepath.Join(absWorkingDirPath, "config.json"))
	if err != nil {
		log.Debug("WARN: configuration file does not exist: ", err.Error())
		return nil
	}

	var mySchedule Schedule
	err = json.Unmarshal(b, &mySchedule)
	if err != nil {
		log.Fatal("failed to unmarshal config file", err)
	}

	scheduleExists = true
	return &mySchedule
}()

type workItem struct {
	OriginalFile string
	TempFile     string
	DestFile     string
	Command      string
	Args         []string
}

var sigs = make(chan os.Signal, 1)
var dispatchChannel = make(chan workItem)
var wg sync.WaitGroup
var processingSet = mapset.NewSet[string]()

func worker() {
	for wrk := range dispatchChannel {
		// We're willing to wait up to 3 minutes.
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute*3)
		defer cancel()

		_, err := exec.CommandContext(ctx, wrk.Command, wrk.Args...).Output()
		if err != nil {
			switch e := err.(type) {
			case *exec.Error:
				log.Fatal("failed to execute this command!:", err)
			case *exec.ExitError:
				log.Info("command exit rc =", e.ExitCode())
			}
		}

		// Simulate time.
		select {
		case <-time.After(time.Millisecond * 10):
		case <-ctx.Done():
			log.Info("context deadline fired, skipping sleep")
		}

		// Cleanup
		processingSet.Remove(wrk.OriginalFile)
		err = os.Rename(wrk.TempFile, wrk.DestFile)
		if err != nil {
			log.Fatalf("Failed to move file: %q with err: %q\n", wrk.TempFile, err)
		}

		// Show completed notification...
		doNotification("File Completed: " + filepath.Base(wrk.DestFile) + " ✅")
	}

	log.Info("worker shutting down...")
	wg.Done()
}

func getHomePath(path string) string {
	usr, err := user.Current()
	if err != nil {
		log.Fatal("failed to get the current user with err: ", err)
	}

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
		log.Debug("Failed to show notification with err: ", err.Error())
	}
}

func copyFile(srcFile, destFile string) error {
	input, err := os.ReadFile(srcFile)
	if err != nil {
		return err
	}

	err = os.WriteFile(destFile, input, 0644)
	if err != nil {

		return err
	}
	return nil
}

// TODO: we shouldn't have to do this wrapper nonsense when this upstream tool is updated.
type wrappedConfig struct {
	kv  map[string]string
	cfg launchctlutil.Configuration
}

func newWrappedConfig(cfg launchctlutil.Configuration) *wrappedConfig {
	return &wrappedConfig{
		cfg: cfg,
		kv:  make(map[string]string),
	}
}

func (wc *wrappedConfig) AddKeyValue(key, value string) {
	wc.kv[key] = value
}

func (wc *wrappedConfig) GetLabel() string {
	return wc.cfg.GetLabel()
}

func (wc *wrappedConfig) GetContents() string {
	contents := wc.cfg.GetContents()
	if len(wc.kv) > 0 {
		var injectedKeyValues []string
		for k, v := range wc.kv {
			injectedKeyValues = append(injectedKeyValues, strings.Repeat(" ", 8)+"<key>"+k+"</key>")
			injectedKeyValues = append(injectedKeyValues, strings.Repeat(" ", 8)+"<string>"+v+"</string>")
		}
		contents = strings.Replace(contents, "        <true/>", "        <true/>\n"+strings.Join(injectedKeyValues, "\n"), -1)
	}
	//log.Info(contents)
	return contents
}

func (wc *wrappedConfig) GetFilePath() (configFilePath string, err error) {
	return wc.cfg.GetFilePath()
}

func (wc *wrappedConfig) GetKind() launchctlutil.Kind {
	return wc.cfg.GetKind()
}

func (wc *wrappedConfig) IsInstalled() (bool, error) {
	return wc.cfg.IsInstalled()
}

func cfgWrapperHack(cfg launchctlutil.Configuration) *wrappedConfig {
	wrapped := newWrappedConfig(cfg)
	return wrapped
}

func install(configFile string) {
	expandedPath := getHomePath("~/Library/Application Support/" + launchDLabel)

	// 1. Create dir
	err := os.MkdirAll(expandedPath, 0750)
	if err != nil && !os.IsExist(err) {
		log.Fatalf("failed to create directory at: %q\n", expandedPath)
	}

	// 2. Move self process to dir
	runningProcessPath, err := os.Executable()
	if err != nil {
		log.Fatal("failed to get exec path with err: ", err)
	}

	err = copyFile(runningProcessPath, filepath.Join(expandedPath, finalExeName))
	if err != nil {
		log.Fatal("failed to copy file to destination directory: ", err)
	}

	// 3. Move config.json to application support directory
	err = copyFile(configFile, filepath.Join(expandedPath, "config.json"))
	if err != nil {
		log.Fatal("failed to move over config.json file with err: ", err.Error())
	}

	// 4. Set permission to executable.
	err = os.Chmod(filepath.Join(expandedPath, finalExeName), 0700)
	if err != nil {
		log.Fatal("failed to change permission of file to executable with err: ", err.Error())
	}

	// 5. Create and install launchd script
	config, err := launchctlutil.NewConfigurationBuilder().
		SetKind(launchctlutil.UserAgent).
		SetLabel(launchDLabel).
		SetRunAtLoad(true).
		SetCommand(filepath.Join(expandedPath, finalExeName)).
		//AddArgument("Hello world!").
		SetLogParentPath("/tmp").
		Build()
	if err != nil {
		log.Fatal(err.Error())
	}

	// 5. Apply hack for unsupported key/value pairs (or until github.com/stephen-fox/launchtlutil gets their act together)
	cfg := cfgWrapperHack(config)
	//cfg.AddKeyValue("WorkingDirectory", expandedPath)
	//cfg.AddKeyValue("RootDirectory", "...")

	// 4. Install the launchd config
	err = launchctlutil.Install(cfg)
	if err != nil {
		log.Fatal(err.Error())
	}
}

func start() {
	log.Infof("starting service: %q\n", launchDLabel)
	err := launchctlutil.Start(launchDLabel, launchctlutil.UserAgent)
	if err != nil {
		log.Fatal("failed to start service with err: ", err)
	}
}

func remove() error {
	//absWorkingDirPath := filepath.Join(getHomePath(applicationSupportPath), launchDLabel)
	err := launchctlutil.Remove(launchDLabel, launchctlutil.UserAgent)
	if err != nil {
		log.Debug("WARN: could not remove service with err: ", err)
	}

	err = launchctlutil.RemoveService(launchDLabel)
	if err != nil {
		log.Debug("WARN: could not stop service with err: ", err)
		return nil
	}

	log.Info("service successfully stopped.")

	return nil
}

func status() {
	details, err := launchctlutil.CurrentStatus(launchDLabel)
	if err != nil {
		log.Fatal("failed to get the current launchctl status with err: ", err.Error())
	}

	// TODO: clean up how this prints out.

	fmt.Printf("Command.status: %q ", details.Status)

	if details.GotPid() {
		fmt.Printf(", Current PID: %d", details.Pid)
	}

	if details.GotLastExitStatus() {
		fmt.Printf(", Last exit status: %d", details.LastExitStatus)
	}
}

func main() {
	log.Debug("Starting the watchmen app!")
	wd, err := os.Getwd()
	if err != nil {
		log.Fatal("failed to get CWD with err:", err)
	}

	log.Infof("Current working directory: %q", wd)

	if len(os.Args) > 1 {
		if os.Args[1] == "install" {
			if len(os.Args) < 3 {
				log.Fatal("must provide a config .json file to install")
			}

			log.Info("installing to correct location...")
			install(os.Args[2])
			return
		} else if os.Args[1] == "status" {
			status()
			return
		} else if os.Args[1] == "start" {
			start()
			return
		} else if os.Args[1] == "remove" {
			log.Info("removing this service...")
			if err := remove(); err != nil {
				log.Fatal("failed to remove service with err: ", err.Error())
			}
			return
		} else if os.Args[1] == "uninstall" {
			// TODO: uninstall should completely remove all traces of service and data in Application Support folder.
			// TODO: Disambiguate between the difference between remove and uninstall.
			return
		}
	}

	// In default mode, just run.
	run()
}

func run() {
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
						log.Debug("Create handleEvent failed with err: ", err.Error())
					}
				}

			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Debug("watcher error: ", err)
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
	log.Infof("Created %d workers...", schedule.MaxWorkers)
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

					wi := workItem{
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
	log.Info("waiting on signal")
	<-sigs

	log.Info("closing dispatch channel")
	close(dispatchChannel)
	log.Info("Signal received...")

	wg.Wait()
	log.Info("Workers shutdown...")
	log.Info("Exiting.")
}
