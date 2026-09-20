package execx

import (
	"context"
	"strings"
	"testing"
)

func TestRunnerRunsOneShotCommand(t *testing.T) {
	r := OSRunner{}
	got, err := r.Run(context.Background(), Spec{Path: "sh", Args: []string{"-c", "printf out; printf err >&2"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Stdout != "out" || got.Stderr != "err" || got.ExitCode != 0 {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestRunnerEnvOverridesInheritedVariableWithoutDroppingUnrelatedVariables(t *testing.T) {
	t.Setenv("COLABNOMAD_RUNNER_OVERRIDE", "inherited")
	t.Setenv("COLABNOMAD_RUNNER_UNRELATED", "preserved")
	r := OSRunner{}
	got, err := r.Run(context.Background(), Spec{
		Path: "sh",
		Args: []string{"-c", "printf '%s|%s' \"$COLABNOMAD_RUNNER_OVERRIDE\" \"$COLABNOMAD_RUNNER_UNRELATED\""},
		Env:  map[string]string{"COLABNOMAD_RUNNER_OVERRIDE": "overridden"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Stdout != "overridden|preserved" {
		t.Fatalf("unexpected environment: %q", got.Stdout)
	}
}

func TestRunnerReturnsExitCodeOnFailure(t *testing.T) {
	r := OSRunner{}
	got, err := r.Run(context.Background(), Spec{Path: "sh", Args: []string{"-c", "printf nope >&2; exit 7"}})
	if err == nil || got.ExitCode != 7 || !strings.Contains(got.Stderr, "nope") {
		t.Fatalf("got result=%+v err=%v", got, err)
	}
}
