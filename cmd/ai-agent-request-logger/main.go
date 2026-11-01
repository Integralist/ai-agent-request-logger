package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const defaultPort = 8787

// runParams supplies the command inputs and streams so startup behaviour stays testable.
type runParams struct {
	// ctx carries process cancellation to setup and the HTTP listener.
	ctx context.Context
	// args contains command-line flags.
	args []string
	// in provides interactive setup answers.
	in io.Reader
	// out receives instructions and the startup banner.
	out io.Writer
	// errOut receives flag and setup diagnostics.
	errOut io.Writer
	// logger records proxy lifecycle events.
	logger *slog.Logger
}

// main reports command failures and is the only function that sets the process exit status.
func main() {
	if err := execute(); err != nil {
		slog.Error("command_failed", "err", err)
		os.Exit(1)
	}
}

// execute creates the cancellable process context and runs the CLI.
func execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	return run(runParams{ctx: ctx, args: os.Args[1:], in: os.Stdin, out: os.Stdout, errOut: os.Stderr, logger: logger})
}

// run parses options, resolves the harness, and keeps its local proxy alive until cancellation.
func run(params runParams) error {
	flags := flag.NewFlagSet("ai-agent-request-logger", flag.ContinueOnError)
	flags.SetOutput(params.errOut)
	port := flags.Int("port", configuredPort(), "local HTTP port (or set PORT)")
	force := flags.Bool("force", false, "forget the saved choice and run setup again")
	logDirFlag := flags.String("log-dir", "", "capture directory (default: ./.ai-agent-request-logger/logs)")
	if err := flags.Parse(params.args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("command: parse flags: %w", err)
	}
	if err := validatePort(*port); err != nil {
		return err
	}
	if params.logger == nil {
		params.logger = slog.New(slog.DiscardHandler)
	}
	choiceFile, err := choicePath()
	if err != nil {
		return err
	}
	if *force {
		if err := clearChoice(choiceFile); err != nil {
			fmt.Fprintf(params.errOut, "Could not forget the saved selection: %v\n", err)
		}
	}
	reader := bufio.NewReader(params.in)
	choice, found := AgentChoice{}, false
	if !*force {
		choice, found, err = loadChoice(choiceFile)
		if err != nil {
			fmt.Fprintf(params.errOut, "Could not read the saved selection: %v\n", err)
		}
	}
	if found {
		fmt.Fprintf(params.out, "Using remembered harness: %s (%s)\n", choice.Agent, choice.Provider)
	} else {
		choice, remember, err := askChoice(params.ctx, reader, params.out)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return fmt.Errorf("command: setup ended before a choice was entered: %w", err)
			}
			return err
		}
		if remember && choice.Agent != otherChoiceID && choice.Provider != otherChoiceID {
			if err := saveChoice(choiceFile, choice); err != nil {
				fmt.Fprintf(params.errOut, "Could not save your selection: %v\n", err)
			}
		}
	}
	target, err := ResolveChoice(choice, *port)
	if err != nil && found {
		fmt.Fprintf(params.out, "The saved selection is no longer valid: %v\nChoose a harness again.\n", err)
		if clearErr := clearChoice(choiceFile); clearErr != nil {
			fmt.Fprintf(params.errOut, "Could not forget the saved selection: %v\n", clearErr)
		}
		choice, remember, askErr := askChoice(params.ctx, reader, params.out)
		if askErr != nil {
			return askErr
		}
		if remember && choice.Agent != otherChoiceID && choice.Provider != otherChoiceID {
			if saveErr := saveChoice(choiceFile, choice); saveErr != nil {
				fmt.Fprintf(params.errOut, "Could not save your selection: %v\n", saveErr)
			}
		}
		target, err = ResolveChoice(choice, *port)
	}
	if err != nil {
		fmt.Fprintln(params.out, err)
		return nil
	}
	logDir := strings.TrimSpace(*logDirFlag)
	if logDir == "" {
		workingDir, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("command: get working directory: %w", err)
		}
		logDir = filepath.Join(workingDir, ".ai-agent-request-logger", "logs")
	} else {
		logDir, err = filepath.Abs(logDir)
		if err != nil {
			return fmt.Errorf("command: resolve log directory: %w", err)
		}
	}
	printStartup(params.out, target, *port, logDir)
	handler := newProxyHandler(target, logDir, params.logger)
	return serveUntilCancelled(params.ctx, fmt.Sprintf("127.0.0.1:%d", *port), handler)
}

// configuredPort reads PORT or falls back to the standard local listener port.
func configuredPort() int {
	value := strings.TrimSpace(os.Getenv("PORT"))
	if value == "" {
		return defaultPort
	}
	port, err := strconv.Atoi(value)
	if err != nil {
		return defaultPort
	}
	return port
}

// printStartup explains the resolved route and the exact setup needed to start the harness.
func printStartup(out io.Writer, target Target, port int, logDir string) {
	fmt.Fprintln(out)
	fmt.Fprintln(out, strings.Repeat("-", 72))
	fmt.Fprintf(out, "Agent:     %s (%s)\n", target.AgentLabel, target.ProviderLabel)
	fmt.Fprintf(out, "Listening: http://localhost:%d\n", port)
	fmt.Fprintf(out, "Forwards:  %s\n", target.UpstreamBaseURL)
	fmt.Fprintf(out, "Logs:      %s\n", logDir)
	fmt.Fprintln(out, strings.Repeat("-", 72))
	for _, file := range target.Setup {
		fmt.Fprintf(out, "\nMerge this %s snippet into %s:\n\n%s\n", file.Language, file.Path, file.Body)
	}
	if len(target.Setup) > 0 {
		fmt.Fprintln(out, "\nThen start the harness in another terminal with:")
	} else {
		fmt.Fprintln(out, "\nStart the harness in another terminal with:")
	}
	fmt.Fprintf(out, "\n  %s\n", target.Command)
	for _, note := range target.Notes {
		fmt.Fprintf(out, "\nNote: %s\n", note)
	}
	for _, warning := range target.Warnings {
		fmt.Fprintf(out, "\nWarning: %s\n", warning)
	}
	fmt.Fprintln(out, "\nCaptures can contain prompts, code, tool definitions, and model output. Keep the log directory private.")
	fmt.Fprintln(out, "Press Ctrl+C to stop the proxy.")
}
