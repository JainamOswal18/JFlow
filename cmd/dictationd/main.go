package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/JainamOswal18/JFlow/internal/dictation"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	if os.Args[1] == "init" {
		initFiles()
		return
	}
	cfg, err := dictation.LoadConfig(dictation.ConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "dictationd is not configured. Run: dictationd init\n")
			os.Exit(2)
		}
		fatal(err)
	}
	if err := dictation.LoadCredentials(dictation.CredentialsPath()); err != nil {
		fatal(err)
	}
	switch os.Args[1] {
	case "daemon":
		d, err := dictation.NewDaemon(cfg)
		if err != nil {
			fatal(err)
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := d.Run(ctx); err != nil {
			fatal(err)
		}
	case "start", "stop", "toggle", "cancel", "cancel-if-recording", "retry-last", "dismiss-last", "copy-last", "status":
		callAndPrint(cfg, dictation.Command{Action: os.Args[1]})
	case "copy":
		if len(os.Args) != 3 {
			fatal(errors.New("usage: dictationd copy JOB_ID"))
		}
		callAndPrint(cfg, dictation.Command{Action: "copy", JobID: os.Args[2]})
	case "history":
		callAndPrint(cfg, dictation.Command{Action: "history", Query: strings.Join(os.Args[2:], " ")})
	case "delete-history":
		if len(os.Args) != 3 {
			fatal(errors.New("usage: dictationd delete-history JOB_ID"))
		}
		callAndPrint(cfg, dictation.Command{Action: "delete-history", JobID: os.Args[2]})
	case "vocabulary":
		callAndPrint(cfg, dictation.Command{Action: "vocabulary"})
	case "vocabulary-add":
		if len(os.Args) != 4 {
			fatal(errors.New("usage: dictationd vocabulary-add HEARD_TEXT REPLACEMENT"))
		}
		callAndPrint(cfg, dictation.Command{Action: "vocabulary-add", Heard: os.Args[2], Replacement: os.Args[3]})
	case "vocabulary-delete":
		if len(os.Args) != 3 {
			fatal(errors.New("usage: dictationd vocabulary-delete ENTRY_ID"))
		}
		callAndPrint(cfg, dictation.Command{Action: "vocabulary-delete", JobID: os.Args[2]})
	case "library":
		openLibrary(cfg)
	case "retry":
		if len(os.Args) != 3 {
			fatal(errors.New("usage: dictationd retry JOB_ID"))
		}
		callAndPrint(cfg, dictation.Command{Action: "retry", JobID: os.Args[2]})
	case "config-path":
		fmt.Println(dictation.ConfigPath())
	case "credentials-path":
		fmt.Println(dictation.CredentialsPath())
	default:
		usage()
		os.Exit(2)
	}
}

func callAndPrint(cfg dictation.Config, cmd dictation.Command) {
	resp, err := call(cfg, cmd)
	if err != nil {
		fatal(err)
	}
	if !resp.OK {
		fatal(errors.New(resp.Error))
	}
	printJSON(resp)
}

func openLibrary(cfg dictation.Config) {
	if _, err := os.Stat(cfg.LibraryUIPath()); err != nil {
		fatal(fmt.Errorf("JFlow Library UI is unavailable: %w", err))
	}
	// Quickshell can retain a process after its FloatingWindow disappears. Its
	// --no-duplicate mode then exits successfully without showing anything. The
	// library is independent of recording, so replace only this UI instance
	// before every open instead of trusting that stale-instance detection.
	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = exec.CommandContext(stopCtx, "/usr/bin/qs", "kill", "-p", cfg.LibraryUIPath()).Run()
	cancel()
	cmd := exec.CommandContext(context.Background(), "/usr/bin/qs", "-p", cfg.LibraryUIPath())
	// The Library must outlive the short launcher command. A separate session
	// also prevents terminal, desktop-entry, or hotkey wrappers from closing the
	// window when they exit.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		fatal(fmt.Errorf("open JFlow Library: %w", err))
	}
	_ = cmd.Process.Release()
}

func initFiles() {
	path := dictation.ConfigPath()
	if err := dictation.WriteDefaultConfig(path); err != nil {
		fatal(err)
	}
	cred := dictation.CredentialsPath()
	if err := os.MkdirAll(filepath.Dir(cred), 0700); err != nil {
		fatal(err)
	}
	if _, err := os.Stat(cred); os.IsNotExist(err) {
		content := "# Keep this file private (mode 0600). Never commit it.\nELEVENLABS_API_KEY=\n# LLM_API_KEY=\n"
		if err := os.WriteFile(cred, []byte(content), 0600); err != nil {
			fatal(err)
		}
	}
	fmt.Printf("Created %s\nAdd your API key to %s, then enable the user service.\n", path, cred)
}

func call(cfg dictation.Config, cmd dictation.Command) (dictation.Response, error) {
	return dictation.SendCommand(cfg.SocketPath(), cmd)
}
func printJSON(v any) { b, _ := json.MarshalIndent(v, "", "  "); fmt.Println(string(b)) }
func fatal(err error) { fmt.Fprintln(os.Stderr, "dictationd:", err); os.Exit(1) }
func usage() {
	fmt.Fprintln(os.Stderr, "usage: dictationd <init|daemon|start|stop|toggle|cancel|cancel-if-recording|retry-last|dismiss-last|copy-last|copy JOB_ID|retry JOB_ID|status|history [QUERY]|delete-history JOB_ID|vocabulary|vocabulary-add HEARD_TEXT REPLACEMENT|vocabulary-delete ENTRY_ID|library>")
}
