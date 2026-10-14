package agents

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v3"
)

// The install scripts allow remote restarts (remote-restart-enabled in the
// agent's config.yml) only when the command whoever installs the agent runs
// says so: --allow-remote-restart, or -AllowRemoteRestart on Windows. These
// tests run the shell parts with sh and check the rest of the wiring.

// between returns the part of script from the line starting with from up to,
// not including, the line starting with to.
func between(t *testing.T, script, from, to string) string {
	t.Helper()
	i := strings.Index(script, from)
	if i < 0 {
		t.Fatalf("marker %q not found", from)
	}
	j := strings.Index(script[i:], to)
	if j < 0 {
		t.Fatalf("marker %q not found after %q", to, from)
	}
	return script[i : i+j]
}

// runSh runs a shell snippet with the given environment and arguments and
// returns what it prints.
func runSh(t *testing.T, snippet string, env []string, args ...string) string {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh to run the script with")
	}
	cmd := exec.Command(sh, append([]string{"-c", snippet, "sh"}, args...)...)
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH")}, env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("snippet failed: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

const logStubs = `info() { :; }; warning() { :; }; success() { :; }
`

func TestInstallScriptAllowsRemoteRestartOnlyFromItsArguments(t *testing.T) {
	block := between(t, string(PatchmonInstallScript),
		"# --allow-remote-restart: this host allows", "# remote_restart_allowed_in reports")
	snippet := logStubs + block + `echo "$ALLOW_REMOTE_RESTART"`
	cases := []struct {
		name string
		env  []string
		args []string
		want string
	}{
		{"no argument", nil, nil, "false"},
		{"the argument", nil, []string{"--allow-remote-restart"}, "true"},
		{"with --force", nil, []string{"--force", "--allow-remote-restart"}, "true"},
		{"a variable the server could set", []string{"ALLOW_REMOTE_RESTART=true"}, nil, "false"},
		{"another argument", nil, []string{"--allow-remote-restart-now"}, "false"},
	}
	for _, c := range cases {
		if got := runSh(t, snippet, c.env, c.args...); got != c.want {
			t.Errorf("%s: ALLOW_REMOTE_RESTART=%q, want %q", c.name, got, c.want)
		}
	}
}

func TestInstallScriptReadsTheSwitchFromAConfig(t *testing.T) {
	fn := between(t, string(PatchmonInstallScript),
		"remote_restart_allowed_in() {", "# restart_agent_service restarts")
	dir := t.TempDir()
	files := map[string]string{
		"on":        "integrations:\n    docker: false\n    remote-restart-enabled: true\n",
		"crlf":      "integrations:\r\n  remote-restart-enabled: true\r\n",
		"off":       "integrations:\n    remote-restart-enabled: false\n",
		"quoted":    "integrations:\n    remote-restart-enabled: \"true\"\n",
		"commented": "integrations:\n    # remote-restart-enabled: true\n",
	}
	want := map[string]string{"on": "yes", "crlf": "yes", "off": "no", "quoted": "no", "commented": "no", "missing": "no"}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name, w := range want {
		got := runSh(t, fn+`
if remote_restart_allowed_in "$1"; then echo yes; else echo no; fi`, nil, filepath.Join(dir, name))
		if got != w {
			t.Errorf("%s: %s, want %s", name, got, w)
		}
	}
}

// The config the script writes holds the switch, and a reinstall looks at
// the config it replaces before moving it away.
func TestInstallScriptWritesTheSwitch(t *testing.T) {
	script := string(PatchmonInstallScript)
	heredoc := between(t, script, "cat > /etc/patchmon/config.yml << EOF", "\nEOF\n")
	for _, value := range []string{"true", "false"} {
		rendered := strings.NewReplacer(
			"$PATCHMON_URL", "https://patchmon.example.net",
			"${SKIP_SSL_VERIFY:-false}", "false",
			"${ALLOW_REMOTE_RESTART}", value,
			"$(date)", "today",
		).Replace(heredoc[strings.Index(heredoc, "\n")+1:])
		var cfg struct {
			Integrations map[string]any `yaml:"integrations"`
		}
		if err := yaml.Unmarshal([]byte(rendered), &cfg); err != nil {
			t.Fatalf("config.yml does not parse: %v\n%s", err, rendered)
		}
		if got := cfg.Integrations["remote-restart-enabled"]; got != (value == "true") {
			t.Errorf("remote-restart-enabled = %v (%T), want the boolean %s", got, got, value)
		}
	}
	carry := strings.Index(script, "if remote_restart_allowed_in /etc/patchmon/config.yml; then")
	move := strings.Index(script, "mv /etc/patchmon/config.yml /etc/patchmon/config.yml.backup")
	if carry < 0 || move < 0 || carry > move {
		t.Fatalf("the previous config is not read before it is moved away (read at %d, moved at %d)", carry, move)
	}
}

func TestWindowsInstallScriptAllowsRemoteRestartOnlyFromItsArguments(t *testing.T) {
	script := string(PatchmonWindowsInstallScript)
	if !strings.Contains(script, `$AllowRemoteRestart = $args -contains "-AllowRemoteRestart"`) {
		t.Fatal("the script does not read -AllowRemoteRestart from its arguments")
	}
	if strings.Count(script, "$AllowRemoteRestart =") != 1 {
		t.Fatal("$AllowRemoteRestart is set more than once")
	}
	if strings.Contains(strings.ToLower(script), "$env:patchmon_allow_remote_restart") {
		t.Fatal("a variable the server sets could allow remote restarts")
	}
	call := `@("--config", $configFile, "config", "remote-restart", "on")`
	set := strings.Index(script, call)
	service := strings.Index(script, "New-Service -Name $serviceName")
	if set < 0 || service < 0 || set > service {
		t.Fatal("remote restarts are not allowed before the service is created")
	}
}

func TestEnrollmentScriptsPassTheArgumentOn(t *testing.T) {
	for name, script := range map[string]string{
		"direct-host": string(DirectHostAutoEnrollScript),
		"proxmox":     string(ProxmoxAutoEnrollScript),
	} {
		block := between(t, script, `INSTALL_ARGS=""`, "\ndone\n") + "\ndone\n"
		if got := runSh(t, block+`echo "[$INSTALL_ARGS]"`, nil, "--allow-remote-restart"); got != "[--allow-remote-restart]" {
			t.Errorf("%s with the argument: %s", name, got)
		}
		if got := runSh(t, block+`echo "[$INSTALL_ARGS]"`, []string{"INSTALL_ARGS=--allow-remote-restart"}); got != "[]" {
			t.Errorf("%s without it: %s", name, got)
		}
	}
	if !strings.Contains(string(DirectHostAutoEnrollScript), `| sh -s -- $INSTALL_ARGS`) {
		t.Error("direct-host enrollment does not pass the argument to the install script")
	}
	if strings.Count(string(ProxmoxAutoEnrollScript), "sh patchmon-install.sh $INSTALL_ARGS") != 2 {
		t.Error("proxmox enrollment does not pass the argument on both install attempts")
	}
}
