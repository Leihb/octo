//go:build linux

package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// policyEnv carries the JSON-encoded Policy to the re-exec shim.
const policyEnv = "OCTO_SANDBOX_POLICY"

// shimArg is the hidden subcommand the parent re-execs into; the shim applies
// Landlock + seccomp to itself, then execs the real command.
const shimArg = "__sandboxed-exec"

// extra device files the confined command may read/write even though they
// aren't under a write root.
var deviceFiles = []string{"/dev/null", "/dev/zero", "/dev/full", "/dev/random", "/dev/urandom", "/dev/tty"}

// Available reports whether Landlock is usable (kernel ≥ 5.13 with Landlock
// enabled). It queries the supported ABI version.
func Available() bool { return landlockABI() >= 1 }

// landlockABI returns the kernel's Landlock ABI version, or -1 if unsupported.
func landlockABI() int {
	v, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, uintptr(unix.LANDLOCK_CREATE_RULESET_VERSION))
	if errno != 0 {
		return -1
	}
	return int(v)
}

// Command re-execs this binary as the sandbox shim, passing the policy via the
// environment and the real command after "--".
func Command(ctx context.Context, command string, p Policy) (*exec.Cmd, error) {
	if !Available() {
		return nil, ErrUnsupported
	}
	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("sandbox: locate self: %w", err)
	}
	data, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("sandbox: marshal policy: %w", err)
	}
	cmd := exec.CommandContext(ctx, self, shimArg, "--", "/bin/sh", "-c", command)
	cmd.Env = append(os.Environ(), policyEnv+"="+string(data))
	return cmd, nil
}

// ShimMain is the re-exec entry point (dispatched from main on the
// __sandboxed-exec subcommand). It applies the sandbox to itself, then execs
// the command that follows "--". It never returns on success.
func ShimMain() int {
	// Keep the security-applying work and the execve on one OS thread: the
	// per-thread Landlock/seccomp restrictions then carry into the exec'd image.
	runtime.LockOSThread()

	cmdArgs := argsAfterDoubleDash(os.Args)
	if len(cmdArgs) == 0 {
		fmt.Fprintln(os.Stderr, "sandbox shim: no command after --")
		return 1
	}

	var p Policy
	if raw := os.Getenv(policyEnv); raw != "" {
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			fmt.Fprintf(os.Stderr, "sandbox shim: bad policy: %v\n", err)
			return 1
		}
	}

	if err := applyLandlock(p); err != nil {
		fmt.Fprintf(os.Stderr, "sandbox shim: landlock: %v\n", err)
		return 1
	}
	if !p.AllowNetwork {
		if err := applyNoNetwork(); err != nil {
			fmt.Fprintf(os.Stderr, "sandbox shim: seccomp: %v\n", err)
			return 1
		}
	}

	path := cmdArgs[0]
	if resolved, err := exec.LookPath(path); err == nil {
		path = resolved
	}
	if err := syscall.Exec(path, cmdArgs, os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "sandbox shim: exec %s: %v\n", path, err)
		return 1
	}
	return 0 // unreachable
}

func argsAfterDoubleDash(args []string) []string {
	for i, a := range args {
		if a == "--" {
			return args[i+1:]
		}
	}
	return nil
}

// handledAccessFS returns the full set of filesystem access rights to govern,
// masked to what the kernel's ABI version supports (newer bits on an older
// kernel would make create_ruleset fail).
func handledAccessFS(abi int) uint64 {
	a := uint64(unix.LANDLOCK_ACCESS_FS_EXECUTE |
		unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
		unix.LANDLOCK_ACCESS_FS_MAKE_CHAR |
		unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
		unix.LANDLOCK_ACCESS_FS_MAKE_REG |
		unix.LANDLOCK_ACCESS_FS_MAKE_SOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_FIFO |
		unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_SYM)
	if abi >= 2 {
		a |= unix.LANDLOCK_ACCESS_FS_REFER
	}
	if abi >= 3 {
		a |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	return a
}

// applyLandlock confines this process to the policy's filesystem roots: read
// (+exec) on ReadRoots, full read/write on WriteRoots, plus read/write on a few
// device files. Paths that don't exist are skipped.
func applyLandlock(p Policy) error {
	abi := landlockABI()
	if abi < 1 {
		return ErrUnsupported
	}
	handled := handledAccessFS(abi)

	attr := struct{ accessFS uint64 }{accessFS: handled}
	fd, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET,
		uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0)
	if errno != 0 {
		return fmt.Errorf("create_ruleset: %w", errno)
	}
	rulesetFd := int(fd)
	defer unix.Close(rulesetFd)

	readDir := handled & uint64(unix.LANDLOCK_ACCESS_FS_READ_FILE|
		unix.LANDLOCK_ACCESS_FS_READ_DIR|
		unix.LANDLOCK_ACCESS_FS_EXECUTE)

	for _, root := range p.WriteRoots {
		if err := addPathRule(rulesetFd, root, handled); err != nil {
			return err
		}
	}
	for _, root := range p.ReadRoots {
		if contains(p.WriteRoots, root) {
			continue
		}
		if err := addPathRule(rulesetFd, root, readDir); err != nil {
			return err
		}
	}
	// Device files: only the file read/write rights are valid on a character
	// device (EXECUTE/TRUNCATE make add_rule return EINVAL). Best-effort — a
	// device-rule failure must never abort the whole sandbox.
	devAccess := handled & uint64(unix.LANDLOCK_ACCESS_FS_READ_FILE|unix.LANDLOCK_ACCESS_FS_WRITE_FILE)
	for _, dev := range deviceFiles {
		_ = addPathRule(rulesetFd, dev, devAccess)
	}

	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("no_new_privs: %w", err)
	}
	if _, _, errno := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, uintptr(rulesetFd), 0, 0); errno != 0 {
		return fmt.Errorf("restrict_self: %w", errno)
	}
	return nil
}

// addPathRule grants access beneath path. A missing path is skipped (not an
// error) so generous default roots stay harmless.
func addPathRule(rulesetFd int, path string, access uint64) error {
	if access == 0 {
		return nil
	}
	fd, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil // missing/inaccessible → skip
	}
	defer unix.Close(fd)

	attr := unix.LandlockPathBeneathAttr{Allowed_access: access, Parent_fd: int32(fd)}
	_, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE,
		uintptr(rulesetFd), uintptr(unix.LANDLOCK_RULE_PATH_BENEATH),
		uintptr(unsafe.Pointer(&attr)), 0, 0, 0)
	if errno != 0 {
		return fmt.Errorf("add_rule %s: %w", path, errno)
	}
	return nil
}

// applyNoNetwork installs a seccomp filter that denies socket(AF_INET) and
// socket(AF_INET6) with EACCES, leaving AF_UNIX and all other syscalls alone.
// This blocks IP networking (including DNS) for the confined command.
//
// Limitation: the filter matches on the native socket(2) syscall number, so a
// process making a 32-bit/compat syscall could bypass it — acceptable for the
// ordinary-command threat model.
func applyNoNetwork() error {
	const (
		denyRet  = uint32(unix.SECCOMP_RET_ERRNO) | (uint32(unix.EACCES) & 0x0000ffff)
		allowRet = uint32(unix.SECCOMP_RET_ALLOW)
		// seccomp_data offsets.
		offNR   = 0
		offArg0 = 16
	)
	filter := []unix.SockFilter{
		bpfStmt(unix.BPF_LD|unix.BPF_W|unix.BPF_ABS, offNR),
		bpfJump(unix.BPF_JMP|unix.BPF_JEQ|unix.BPF_K, uint32(unix.SYS_SOCKET), 0, 3), // not socket → allow
		bpfStmt(unix.BPF_LD|unix.BPF_W|unix.BPF_ABS, offArg0),                        // domain
		bpfJump(unix.BPF_JMP|unix.BPF_JEQ|unix.BPF_K, uint32(unix.AF_INET), 2, 0),    // AF_INET → deny
		bpfJump(unix.BPF_JMP|unix.BPF_JEQ|unix.BPF_K, uint32(unix.AF_INET6), 1, 0),   // AF_INET6 → deny
		bpfStmt(unix.BPF_RET|unix.BPF_K, allowRet),
		bpfStmt(unix.BPF_RET|unix.BPF_K, denyRet),
	}
	prog := unix.SockFprog{Len: uint16(len(filter)), Filter: &filter[0]}

	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("no_new_privs: %w", err)
	}
	if err := unix.Prctl(unix.PR_SET_SECCOMP, uintptr(unix.SECCOMP_MODE_FILTER),
		uintptr(unsafe.Pointer(&prog)), 0, 0); err != nil {
		return fmt.Errorf("set_seccomp: %w", err)
	}
	return nil
}

func bpfStmt(code uint16, k uint32) unix.SockFilter {
	return unix.SockFilter{Code: code, K: k}
}

func bpfJump(code uint16, k uint32, jt, jf uint8) unix.SockFilter {
	return unix.SockFilter{Code: code, Jt: jt, Jf: jf, K: k}
}

func contains(roots []string, target string) bool {
	for _, r := range roots {
		if r == target {
			return true
		}
	}
	return false
}
