package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Leihb/octo/internal/agent"
)

// replConfig holds everything runREPL needs.
type replConfig struct {
	a        *agent.Agent
	session  *agent.Session
	noSave   bool
	stdin    io.Reader
	stdout   io.Writer
	stderr   io.Writer
	tools    []agent.ToolDefinition
	executor agent.ToolExecutor
}

// runREPL runs the interactive multi-turn loop until the user exits or EOF.
// It returns 0 on clean exit, 1 on unexpected error.
func runREPL(cfg replConfig) int {
	a := cfg.a
	sess := cfg.session

	turns := sess.TurnCount()
	if turns > 0 {
		fmt.Fprintf(cfg.stdout, "Resumed session %s (%d turn", sess.ID, turns)
		if turns != 1 {
			fmt.Fprint(cfg.stdout, "s")
		}
		fmt.Fprintln(cfg.stdout, ")")
	} else {
		fmt.Fprintf(cfg.stdout, "Starting session %s (%s)\n", sess.ID, sess.Model)
	}
	fmt.Fprintln(cfg.stdout, `Type /help for commands, Ctrl-C or /exit to quit.`)
	fmt.Fprintln(cfg.stdout)

	scanner := bufio.NewScanner(cfg.stdin)
	for {
		fmt.Fprint(cfg.stdout, "you> ")

		if !scanner.Scan() {
			// EOF (Ctrl-D) or read error.
			fmt.Fprintln(cfg.stdout)
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// Slash commands.
		if strings.HasPrefix(line, "/") {
			cmd := strings.ToLower(strings.Fields(line)[0])
			switch cmd {
			case "/exit", "/quit":
				break
			case "/help":
				printReplHelp(cfg.stdout)
				continue
			case "/cost":
				printCost(cfg.stdout, a)
				continue
			case "/save":
				if err := saveSession(cfg.stdout, sess, a); err != nil {
					fmt.Fprintf(cfg.stderr, "save: %v\n", err)
				}
				continue
			case "/sessions":
				if err := printSessions(cfg.stdout); err != nil {
					fmt.Fprintf(cfg.stderr, "sessions: %v\n", err)
				}
				continue
			default:
				fmt.Fprintf(cfg.stdout, "Unknown command %q. Type /help for a list.\n", cmd)
				continue
			}
			// /exit or /quit falls through here.
			break
		}

		// Regular message — streaming turn (or agentic loop when tools enabled).
		startedAt := time.Now()
		var (
			reply agent.Reply
			err   error
		)
		if len(cfg.tools) > 0 && cfg.executor != nil {
			reply, err = a.RunStream(context.Background(), line, cfg.tools, cfg.executor, func(delta string) {
				fmt.Fprint(cfg.stdout, delta)
			})
		} else {
			reply, err = a.TurnStream(context.Background(), line, func(delta string) {
				fmt.Fprint(cfg.stdout, delta)
			})
		}
		if err != nil {
			fmt.Fprintf(cfg.stderr, "\nerror: %v\n", err)
			continue
		}
		_ = startedAt
		_ = reply
		fmt.Fprintln(cfg.stdout) // newline after streamed reply

		// Auto-save after every turn unless opted out.
		if !cfg.noSave {
			sess.SyncFrom(a.History)
			if err := sess.Save(); err != nil {
				// Non-fatal: warn but don't break the session.
				fmt.Fprintf(cfg.stderr, "(auto-save failed: %v)\n", err)
			}
		}
	}

	// Final save on exit.
	if !cfg.noSave {
		sess.SyncFrom(a.History)
		if err := sess.Save(); err != nil {
			fmt.Fprintf(cfg.stderr, "session save: %v\n", err)
			return 1
		}
		path, _ := sess.SavePath()
		fmt.Fprintf(cfg.stdout, "\nSession saved → %s\n", path)
	}
	return 0
}

func printReplHelp(w io.Writer) {
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  /help       Show this message")
	fmt.Fprintln(w, "  /cost       Show token usage and estimated cost for this session")
	fmt.Fprintln(w, "  /save       Save the session now (it also auto-saves after each turn)")
	fmt.Fprintln(w, "  /sessions   List the 10 most recent sessions")
	fmt.Fprintln(w, "  /exit       Save and exit  (also: /quit, Ctrl-C, Ctrl-D)")
}

func printCost(w io.Writer, a *agent.Agent) {
	in, out := a.SessionTokens()
	cost := a.SessionCostUSD()
	fmt.Fprintf(w, "Tokens: %d in / %d out  |  est. $%.6f\n", in, out, cost)
}

func saveSession(w io.Writer, sess *agent.Session, a *agent.Agent) error {
	sess.SyncFrom(a.History)
	if err := sess.Save(); err != nil {
		return err
	}
	path, _ := sess.SavePath()
	fmt.Fprintf(w, "Saved → %s\n", path)
	return nil
}

func printSessions(w io.Writer) error {
	sessions, err := agent.ListSessions(10)
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		fmt.Fprintln(w, "No saved sessions.")
		return nil
	}
	fmt.Fprintln(w, "Recent sessions (newest first):")
	for _, s := range sessions {
		turns := s.TurnCount()
		fmt.Fprintf(w, "  %s  %-30s  %d turn", s.ID, s.Model, turns)
		if turns != 1 {
			fmt.Fprint(w, "s")
		}
		fmt.Fprintln(w)
	}
	return nil
}
