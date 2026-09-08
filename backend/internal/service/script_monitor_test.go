package service

import (
	"strings"
	"testing"
)

func TestScriptMonitorCommandUsesConfiguredDirectory(t *testing.T) {
	command := scriptMonitorCommand("./check.sh", "/opt/ops scripts")
	if !strings.Contains(command, "cd -- '/opt/ops scripts' || exit 125") {
		t.Fatalf("configured directory was not quoted in command: %q", command)
	}
	if !strings.Contains(command, scriptMonitorDirectoryMarker) || !strings.HasSuffix(command, "./check.sh") {
		t.Fatalf("command must capture the effective directory before the script: %q", command)
	}
}

func TestScriptMonitorOutputDirectory(t *testing.T) {
	directory, output := scriptMonitorOutputDirectory(scriptMonitorDirectoryMarker+"/opt/ops\nok\n", "SSH 默认目录")
	if directory != "/opt/ops" || output != "ok\n" {
		t.Fatalf("unexpected parsed result: directory=%q output=%q", directory, output)
	}
}
