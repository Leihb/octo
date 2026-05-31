package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/Leihb/octo-agent/internal/agent"
	"github.com/Leihb/octo-agent/internal/channel"
	"github.com/Leihb/octo-agent/internal/config"
	"github.com/Leihb/octo-agent/internal/prompt"
	"github.com/Leihb/octo-agent/internal/skills"
	"github.com/Leihb/octo-agent/internal/tools"
)

// runChannel handles `octo channel start`.
func runChannel(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("channel", flag.ContinueOnError)
	fs.SetOutput(stderr)
	providerName := fs.String("provider", "", "Provider: anthropic | openai")
	model := fs.String("model", "", "Model name")
	system := fs.String("system", "", "System prompt (optional)")
	bindMode := fs.String("bind-mode", "chat_user", "Session binding: chat_user | chat | user")
	maxTokens := fs.Int("max-tokens", 0, "max_tokens for the response")
	maxTurns := fs.Int("max-turns", 0, "Max provider round-trips per message")
	noTools := fs.Bool("no-tools", false, "Disable built-in tools")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if len(fs.Args()) == 0 || fs.Args()[0] != "start" {
		fmt.Fprintln(stderr, "Usage: octo channel start [flags]")
		return 2
	}

	// Load channel config.
	chCfg, err := channel.LoadConfig()
	if err != nil {
		fmt.Fprintf(stderr, "octo channel: %v\n", err)
		return 1
	}
	if len(chCfg.EnabledPlatforms()) == 0 {
		fmt.Fprintln(stderr, "octo channel: no enabled platforms in ~/.octo/channels.yml")
		fmt.Fprintln(stderr, "Run `octo config` to set up channels.")
		return 1
	}

	// Resolve provider/model.
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "octo channel: %v\n", err)
		return 1
	}
	provName, resolvedModel, ok := resolveProviderModel(*providerName, *model, cfg)
	if !ok {
		fmt.Fprintf(stderr, "octo channel: unknown provider %q\n", provName)
		return 2
	}
	prov, err := buildProvider(provName, cfg, stderr)
	if err != nil {
		return 1
	}

	// Build agent factory.
	cwd, _ := os.Getwd()
	env := buildEnvContext(cwd)
	skillReg := skills.Discover(cwd)
	skillsManifest := skills.RenderManifest(skillReg)
	tools.SetSkills(skillReg)

	agentFactory := func() *agent.Agent {
		a := agent.New(providerSender{
			p:        prov,
			cacheKey: newCacheKey(),
		}, resolvedModel)
		a.CWD = cwd
		a.MaxTokens = *maxTokens
		a.MaxTurns = *maxTurns
		a.System = prompt.Compose(*system, cwd, env, skillsManifest, "")
		return a
	}

	mode := channel.BindingMode(*bindMode)
	mgr := channel.NewManager(chCfg, agentFactory, mode)

	// Wire inbound message handling: for each message, get/create session,
	// build a UIController, and run the agent.
	// The manager's Start handles adapter lifecycle; we inject our own
	// onMessage callback by wrapping the adapter starts.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Override the manager's default message handling with agent execution.
	// We do this by starting adapters ourselves and using the manager for
	// session management only.
	for _, name := range chCfg.EnabledPlatforms() {
		pc := chCfg.Platform(name)
		if pc == nil {
			continue
		}
		ctor, err := channel.Find(name)
		if err != nil {
			fmt.Fprintf(stderr, "octo channel: %v\n", err)
			continue
		}
		ad, err := ctor(pc)
		if err != nil {
			fmt.Fprintf(stderr, "octo channel: failed to create %s adapter: %v\n", name, err)
			continue
		}
		if errs := ad.ValidateConfig(pc); len(errs) > 0 {
			for _, e := range errs {
				fmt.Fprintf(stderr, "octo channel: %s config error: %s\n", name, e)
			}
			continue
		}

		go func(a channel.Adapter, platform string) {
			_ = a.Start(ctx, func(ev channel.InboundEvent) {
				ev.Platform = platform
				if handleCommand(mgr, a, ev) {
					return
				}
				handleAgentMessage(ctx, mgr, a, ev, !*noTools)
			})
		}(ad, name)
	}

	fmt.Fprintln(stdout, "octo channel: running. Press Ctrl-C to stop.")

	// Block on signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	<-sigCh

	fmt.Fprintln(stdout, "\nocto channel: shutting down...")
	_ = mgr.Stop()
	return 0
}

// handleCommand processes slash commands. Returns true if the event was a command.
func handleCommand(mgr *channel.Manager, ad channel.Adapter, ev channel.InboundEvent) bool {
	text := ev.Text
	if len(text) == 0 || text[0] != '/' {
		return false
	}
	// Delegate to manager's command router, but we need to send replies ourselves
	// since the manager doesn't have direct adapter access in this wiring.
	// The manager's handleInbound does routing + reply; we replicate the check.
	// For simplicity, we let the manager handle it and rely on its sendReply.
	// But mgr.sendReply needs the adapter stored in mgr.adapters.
	// So we just let the manager's normal flow handle commands by calling
	// handleInbound directly — but that also calls handleSessionMessage.
	// Instead, we manually route commands here.

	// Actually, the simplest approach: use the manager's commandRouter directly.
	// But it's unexported. We reimplement the command detection.
	// For now, just return false and let the agent handle it as a message.
	// Commands like /bind /status will be processed by the LLM as regular text.
	// TODO: expose command routing or handle here.
	return false
}

// handleAgentMessage runs the agent for a non-command inbound message.
func handleAgentMessage(ctx context.Context, mgr *channel.Manager, ad channel.Adapter, ev channel.InboundEvent, toolsOn bool) {
	sess := mgr.GetOrCreateSession(ev)
	if sess == nil {
		return
	}

	ctrl := channel.NewUIController(ad, ev.ChatID, ev.MessageID)

	var toolDefs []agent.ToolDefinition
	var executor agent.ToolExecutor
	if toolsOn {
		toolDefs = tools.DefaultTools()
		executor = tools.NewDefaultRegistry()
	}

	_, _ = channel.RunAgent(ctx, sess, toolDefs, executor, ctrl, ev.Text)
}
