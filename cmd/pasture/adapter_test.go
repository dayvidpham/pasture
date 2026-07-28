package main

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
)

func TestNewAdapterCommandIsHiddenAndNotRegistered(t *testing.T) {
	for _, command := range rootCmd.Commands() {
		if command.Name() == "__adapter" {
			t.Fatal("adapter command was registered on rootCmd by its owner file")
		}
	}

	command := NewAdapterCommand()
	if command.Name() != "__adapter" || !command.Hidden {
		t.Fatalf("adapter command name/hidden = %q/%t, want __adapter/true", command.Name(), command.Hidden)
	}
	children := command.Commands()
	if len(children) != 1 || children[0].Name() != "invoke" || !children[0].Hidden {
		t.Fatalf("adapter children = %#v, want one hidden invoke command", children)
	}
	if command.ValidArgsFunction == nil || children[0].ValidArgsFunction == nil {
		t.Fatal("adapter commands must suppress file completion")
	}
	if values, directive := command.ValidArgsFunction(command, nil, ""); len(values) != 0 || directive != cobra.ShellCompDirectiveNoFileComp {
		t.Fatalf("parent completion = %v/%v, want no values/no-file-completion", values, directive)
	}
	if values, directive := children[0].ValidArgsFunction(children[0], nil, ""); len(values) != 0 || directive != cobra.ShellCompDirectiveNoFileComp {
		t.Fatalf("invoke completion = %v/%v, want no values/no-file-completion", values, directive)
	}
}

func TestNewAdapterCommandHelpDoesNotExposeInvoke(t *testing.T) {
	command := NewAdapterCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"--help"})
	if err := command.Execute(); err != nil {
		t.Fatalf("execute adapter help: %v", err)
	}
	if bytes.Contains(output.Bytes(), []byte("invoke")) {
		t.Fatalf("hidden invoke command appeared in ordinary parent help:\n%s", output.String())
	}
}
