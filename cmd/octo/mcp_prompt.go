package main

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/Leihb/octo-agent/internal/mcp"
)

// cliOAuthPrompt is the user-facing implementation of mcp.OAuthPrompt for
// `octo chat`. ShowAuthorization prints a small card telling the user
// where to go; Progress just bumps a dot counter so the user can see we're
// actively polling; Done closes the card.
//
// The card looks like:
//
//	┌─ Authorize "lark-project" ──────────────────────────────
//	│ Open: https://project.larksuite.com/b/auth/mcp
//	│ Code: ABCD-EFGH
//	│ Waiting for authorization… ······
//	└─────────────────────────────────────────────────────────
type cliOAuthPrompt struct {
	out        io.Writer
	serverName string

	mu        sync.Mutex
	progress  int
	completed bool
}

func newCLIOAuthPrompt(out io.Writer, serverName string) *cliOAuthPrompt {
	return &cliOAuthPrompt{out: out, serverName: serverName}
}

func (p *cliOAuthPrompt) ShowAuthorization(userCode, verificationURI, verificationURIComplete string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	border := strings.Repeat("─", 56)
	fmt.Fprintf(p.out, "\n┌─ Authorize %q %s\n", p.serverName, border)
	fmt.Fprintf(p.out, "│ Open: %s\n", verificationURI)
	fmt.Fprintf(p.out, "│ Code: %s\n", userCode)
	if verificationURIComplete != "" && verificationURIComplete != verificationURI {
		fmt.Fprintf(p.out, "│ One-step link (code pre-filled): %s\n", verificationURIComplete)
	}
	fmt.Fprint(p.out, "│ Waiting for authorization… ")
}

// Progress emits one dot per poll cycle. Wraps to a fresh line every 40
// dots so the indicator doesn't run past the terminal edge.
func (p *cliOAuthPrompt) Progress() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.completed {
		return
	}
	p.progress++
	if p.progress%40 == 0 {
		fmt.Fprint(p.out, "\n│ ")
	}
	fmt.Fprint(p.out, "·")
}

func (p *cliOAuthPrompt) Done() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.completed {
		return
	}
	p.completed = true
	border := strings.Repeat("─", 56)
	fmt.Fprintf(p.out, "\n│ Authorized.\n└%s\n\n", border+"────────")
}

// Compile-time check that we still implement the interface.
var _ mcp.OAuthPrompt = (*cliOAuthPrompt)(nil)
