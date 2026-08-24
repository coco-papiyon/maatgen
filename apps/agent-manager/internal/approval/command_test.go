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

func TestParseCommandEvaluatesPowerShellCommandPayload(t *testing.T) {
	segments, err := ParseCommand(`"C:\Program Files\PowerShell\7\pwsh.exe" -Command "gh pr create --base main --head issue_3"`)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 1 || !reflect.DeepEqual(segments[0].Argv, []string{"gh", "pr", "create", "--base", "main", "--head", "issue_3"}) {
		t.Fatalf("segments = %#v", segments)
	}
}

func TestParseCommandEvaluatesNestedShellCommandPayload(t *testing.T) {
	segments, err := ParseCommand(`bash -c "git status && go test ./internal/approval"`)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 2 || !reflect.DeepEqual(segments[0].Argv, []string{"git", "status"}) || !reflect.DeepEqual(segments[1].Argv, []string{"go", "test", "./internal/approval"}) {
		t.Fatalf("segments = %#v", segments)
	}
}

func TestParseCommandKeepsResolvedSegmentsAlongsideAnUnresolvedOne(t *testing.T) {
	segments, err := ParseCommand(`"C:\Program Files\PowerShell\7\pwsh.exe" -Command "gofmt -w internal/storage/sqlite/store_test.go; $env:GOCACHE='C:\tmp\maatgen-gocache-dedupe'; go test ./internal/storage/sqlite ./internal/githubmonitor"`)
	if !errors.Is(err, ErrDynamicCommand) {
		t.Fatalf("error = %v, want ErrDynamicCommand", err)
	}
	if len(segments) != 3 {
		t.Fatalf("segments = %#v", segments)
	}
	if want := []string{"gofmt", "-w", "internal/storage/sqlite/store_test.go"}; !reflect.DeepEqual(segments[0].Argv, want) {
		t.Errorf("segment 0 argv = %#v, want %#v", segments[0].Argv, want)
	}
	if len(segments[1].Argv) != 0 || segments[1].Command == "" {
		t.Errorf("segment 1 = %#v, want unresolved with non-empty command text", segments[1])
	}
	if want := []string{"go", "test", "./internal/storage/sqlite", "./internal/githubmonitor"}; !reflect.DeepEqual(segments[2].Argv, want) {
		t.Errorf("segment 2 argv = %#v, want %#v", segments[2].Argv, want)
	}
}

func TestSegmentAllowedMatchesIndividualSegments(t *testing.T) {
	segments, err := ParseCommand("git status && go test ./internal/approval")
	if err != nil {
		t.Fatal(err)
	}
	rules := [][]string{{"git", "status"}}
	if !SegmentAllowed(segments[0], rules) {
		t.Fatal("expected first segment to be allowed")
	}
	if SegmentAllowed(segments[1], rules) {
		t.Fatal("expected second segment to not be allowed")
	}
}

func TestParseCommandRejectsEncodedPowerShellPayload(t *testing.T) {
	segments, err := ParseCommand(`pwsh -EncodedCommand Z2V0LWNvbnRlbnQ=`)
	if !errors.Is(err, ErrDynamicCommand) {
		t.Fatalf("error = %v, want ErrDynamicCommand", err)
	}
	if len(segments) != 1 || len(segments[0].Argv) != 0 {
		t.Fatalf("fallback segments = %#v", segments)
	}
}
