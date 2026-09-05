package app

import "strings"

func isCodexManagerCommand(command string) bool {
	switch command {
	case "sessions", "list", "new", "attach", "stop", "prune", "setup", "_run-session", "_install-release":
		return true
	}
	return false
}

// codexDirect classifies, but never edits, argv. Value-taking options are skipped
// so a profile named "exec" or a model named "review" cannot become a command.
// Unknown options are delegated directly to Codex for forward compatibility;
// explicit `new --backend codex -- ...` remains available for managed launches.
func codexDirect(args []string) bool {
	positional := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		if arg == "--help" || arg == "-h" || arg == "--version" || arg == "-V" {
			return true
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			key, _, attached := strings.Cut(arg, "=")
			if !strings.HasPrefix(arg, "--") && len(arg) > 2 {
				key, attached = arg[:2], true
			}
			switch key {
			case "--profile", "-p", "--model", "-m", "--config", "-c", "--cd", "-C",
				"--sandbox", "-s", "--ask-for-approval", "-a", "--permission-profile", "-P",
				"--image", "-i", "--add-dir", "--remote", "--remote-auth-token-env",
				"--local-provider", "--enable", "--disable":
				if !attached {
					i++
					if i >= len(args) || strings.HasPrefix(args[i], "-") {
						return true
					} // let Codex diagnose a missing value
				}
			case "--oss", "--search", "--full-auto", "--no-alt-screen", "--strict-config",
				"--dangerously-bypass-approvals-and-sandbox", "--yolo", "--dangerously-bypass-hook-trust",
				"--last", "--all":
			default:
				return true
			}
			continue
		}
		if !positional {
			positional = true
			switch arg {
			case "exec", "e", "review", "login", "logout", "mcp", "mcp-server", "app-server", "exec-server",
				"app", "completion", "debug", "apply", "a", "cloud", "cloud-tasks", "features",
				"execpolicy", "sandbox", "plugin", "remote-control", "archive", "delete", "unarchive",
				"doctor", "update", "help":
				return true
			}
		}
	}
	return false
}
