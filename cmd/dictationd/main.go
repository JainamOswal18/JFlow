package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

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
	case "start", "stop", "toggle", "cancel", "cancel-if-recording", "retry-last", "dismiss-last", "copy-last", "status", "history":
		resp, err := call(cfg, dictation.Command{Action: os.Args[1]})
		if err != nil {
			fatal(err)
		}
		if !resp.OK {
			fatal(errors.New(resp.Error))
		}
		printJSON(resp)
	case "retry":
		if len(os.Args) != 3 {
			fatal(errors.New("usage: dictationd retry JOB_ID"))
		}
		resp, err := call(cfg, dictation.Command{Action: "retry", JobID: os.Args[2]})
		if err != nil {
			fatal(err)
		}
		if !resp.OK {
			fatal(errors.New(resp.Error))
		}
		printJSON(resp)
	case "config-path":
		fmt.Println(dictation.ConfigPath())
	case "credentials-path":
		fmt.Println(dictation.CredentialsPath())
	default:
		usage()
		os.Exit(2)
	}
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
	fmt.Fprintln(os.Stderr, "usage: dictationd <init|daemon|start|stop|toggle|cancel|cancel-if-recording|retry-last|dismiss-last|copy-last|retry JOB_ID|status|history>")
}
