package approval

import (
	"errors"
	"reflect"
	"testing"
)

func TestParseCommandSplitsEveryShellSegment(t *testing.T) {
	segments, err := ParseCommand(`git status | Select-String "main && release" && go test ./...; npm test`)
	if err != nil {
		t.Fatalf("ParseCommand: %v", err)
	}
	want := [][]string{{"git", "status"}, {"Select-String", "main && release"}, {"go", "test", "./..."}, {"npm", "test"}}
	if len(segments) != len(want) {
		t.Fatalf("segments = %d, want %d: %#v", len(segments), len(want), segments)
	}
	for index := range want {
		if !reflect.DeepEqual(segments[index].Argv, want[index]) {
			t.Errorf("segment %d argv = %#v, want %#v", index, segments[index].Argv, want[index])
		}
	}
}

func TestAllSegmentsMustMatchAllowedRules(t *testing.T) {
	segments, err := ParseCommand("git status && go test ./internal/approval")
	if err != nil {
		t.Fatal(err)
	}
	if AllSegmentsAllowed(segments, [][]string{{"git", "status"}}) {
		t.Fatal("compound command was allowed when only one segment matched")
	}
	if !AllSegmentsAllowed(segments, [][]string{{"git", "status"}, {"go", "test", "*"}}) {
		t.Fatal("compound command was denied although every segment matched")
	}
}

func TestMatchArgvWildcardMatchesArgumentsOnlyWithinOneCommand(t *testing.T) {
	tests := []struct {
		name    string
		pattern []string
		argv    []string
		want    bool
	}{
		{"zero arguments", []string{"go", "test", "*"}, []string{"go", "test"}, true},
		{"many arguments", []string{"go", "test", "*"}, []string{"go", "test", "./...", "-count=1"}, true},
		{"executable differs", []string{"go", "test", "*"}, []string{"git", "test"}, false},
		{"windows executable suffix", []string{"git", "status"}, []string{"GIT.EXE", "status"}, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := MatchArgv(test.pattern, test.argv); got != test.want {
				t.Fatalf("MatchArgv(%#v, %#v) = %v, want %v", test.pattern, test.argv, got, test.want)
			}
		})
	}
}

func TestParseCommandRejectsDynamicShellSyntax(t *testing.T) {
	for _, command := range []string{"echo $(whoami)", "echo value > output.txt", "Invoke-Expression test"} {
		t.Run(command, func(t *testing.T) {
			segments, err := ParseCommand(command)
			if !errors.Is(err, ErrDynamicCommand) {
				t.Fatalf("error = %v, want ErrDynamicCommand", err)
			}
			if len(segments) != 1 || len(segments[0].Argv) != 0 {
				t.Fatalf("fallback segments = %#v", segments)
			}
		})
	}
}

func TestParseCommandPreservesWindowsPaths(t *testing.T) {
	segments, err := ParseCommand(`Get-Content C:\data\project\main.go`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Get-Content", `C:\data\project\main.go`}
	if !reflect.DeepEqual(segments[0].Argv, want) {
		t.Fatalf("argv = %#v, want %#v", segments[0].Argv, want)
	}
}
