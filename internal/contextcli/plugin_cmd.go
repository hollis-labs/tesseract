package contextcli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/cortex/internal/plugin"
	fplugin "github.com/hollis-labs/go-plugin"
)

const pluginGitOrg = "hollis-labs"

// RunPluginCmd dispatches plugin subcommands.
func RunPluginCmd(args []string, stdout, stderr *os.File) int {
	if len(args) < 1 {
		fmt.Fprintln(stderr, "usage: cortex plugin <command>")
		fmt.Fprintln(stderr, "commands: list, install, uninstall, disable, enable")
		return 1
	}

	pluginsDir := resolvePluginsDir()

	switch args[0] {
	case "list":
		return pluginList(pluginsDir, stdout)
	case "install":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "usage: cortex plugin install <name>")
			return 1
		}
		return pluginInstall(pluginsDir, args[1], stdout, stderr)
	case "uninstall":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "usage: cortex plugin uninstall <name>")
			return 1
		}
		return pluginUninstall(pluginsDir, args[1], stdout, stderr)
	case "disable":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "usage: cortex plugin disable <name>")
			return 1
		}
		return pluginDisable(pluginsDir, args[1], stdout, stderr)
	case "enable":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "usage: cortex plugin enable <name>")
			return 1
		}
		return pluginEnable(pluginsDir, args[1], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown plugin command: %s\n", args[0])
		return 1
	}
}

func resolvePluginsDir() string {
	if d := os.Getenv("CORTEX_PLUGINS_DIR"); d != "" {
		return d
	}
	return "./plugins"
}

func pluginList(pluginsDir string, stdout *os.File) int {
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(stdout, "No plugins installed.")
			return 0
		}
		fmt.Fprintf(os.Stderr, "read plugins dir: %v\n", err)
		return 1
	}

	found := false
	fmt.Fprintf(stdout, "%-25s %-10s %-10s %s\n", "PLUGIN", "VERSION", "STATUS", "DESCRIPTION")
	fmt.Fprintln(stdout, strings.Repeat("-", 80))

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		name := entry.Name()
		status := plugin.PluginStatusString(pluginsDir, name)

		manifestPath := filepath.Join(pluginsDir, name, "plugin.yaml")
		if status == "disabled" {
			manifestPath = filepath.Join(pluginsDir, name, "plugin.yaml.disabled")
		}
		manifest, err := plugin.ParseManifest(manifestPath)
		if err != nil {
			continue
		}

		found = true
		fmt.Fprintf(stdout, "%-25s %-10s %-10s %s\n",
			name, manifest.Version, status, manifest.Description)
	}

	if !found {
		fmt.Fprintln(stdout, "No plugins installed.")
	}
	return 0
}

func pluginInstall(pluginsDir, name string, stdout, stderr *os.File) int {
	target := filepath.Join(pluginsDir, name)

	if _, err := os.Stat(filepath.Join(target, "plugin.yaml")); err == nil {
		fmt.Fprintf(stderr, "Plugin %q is already installed at %s\n", name, target)
		return 1
	}

	os.MkdirAll(pluginsDir, 0755)

	repoURL := fmt.Sprintf("git@github.com:%s/%s.git", pluginGitOrg, name)
	fmt.Fprintf(stdout, "Installing %s from %s...\n", name, repoURL)

	cmd := exec.Command("git", "clone", "--depth", "1", repoURL, target)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(stderr, "Failed to clone: %v\n", err)
		return 1
	}

	if _, err := os.Stat(filepath.Join(target, "plugin.yaml")); err != nil {
		os.RemoveAll(target)
		fmt.Fprintln(stderr, "Cloned repo does not contain plugin.yaml — not a valid plugin")
		return 1
	}

	manifest, err := plugin.ParseManifest(filepath.Join(target, "plugin.yaml"))
	if err != nil {
		os.RemoveAll(target)
		fmt.Fprintf(stderr, "Failed to parse plugin.yaml: %v\n", err)
		return 1
	}

	if _, ok := plugin.LookupConstructor(manifest.Name); !ok {
		fmt.Fprintf(stdout, "Warning: no compiled-in code for %q — plugin will need to be added to the binary\n", manifest.Name)
	} else {
		fmt.Fprintf(stdout, "Found compiled-in code for %q\n", manifest.Name)
	}

	fmt.Fprintf(stdout, "\nPlugin %q installed to %s\n", name, target)
	return 0
}

func pluginUninstall(pluginsDir, name string, stdout, stderr *os.File) int {
	target := filepath.Join(pluginsDir, name)

	manifestPath := filepath.Join(target, "plugin.yaml")
	disabledPath := filepath.Join(target, "plugin.yaml.disabled")
	if _, err := os.Stat(manifestPath); err != nil {
		if _, err2 := os.Stat(disabledPath); err2 != nil {
			fmt.Fprintf(stderr, "Plugin %q is not installed\n", name)
			return 1
		}
		manifestPath = disabledPath
	}

	manifest, err := plugin.ParseManifest(manifestPath)
	if err != nil {
		fmt.Fprintf(stderr, "Failed to parse plugin.yaml: %v\n", err)
		return 1
	}

	if constructor, ok := plugin.LookupConstructor(manifest.Name); ok {
		p := constructor()
		if uninstallable, ok := p.(fplugin.Uninstallable); ok {
			fmt.Fprintf(stdout, "Running %s cleanup...\n", manifest.Name)
			host := plugin.NewHostMinimal()
			if err := uninstallable.Uninstall(host); err != nil {
				fmt.Fprintf(stderr, "Warning: cleanup error: %v\n", err)
			} else {
				fmt.Fprintln(stdout, "Cleanup complete.")
			}
		}
	}

	if err := os.RemoveAll(target); err != nil {
		fmt.Fprintf(stderr, "Failed to remove %s: %v\n", target, err)
		return 1
	}

	fmt.Fprintf(stdout, "\nPlugin %q uninstalled.\n", name)
	return 0
}

func pluginDisable(pluginsDir, name string, stdout, stderr *os.File) int {
	if err := plugin.DisablePlugin(pluginsDir, name); err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Plugin %q disabled.\n", name)
	return 0
}

func pluginEnable(pluginsDir, name string, stdout, stderr *os.File) int {
	if err := plugin.EnablePlugin(pluginsDir, name); err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Plugin %q enabled.\n", name)
	return 0
}
