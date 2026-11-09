package approval

import (
	"errors"
	"reflect"
	"strings"
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
	segments, err := ParseCommand(`"C:\Program Files\PowerShell\7\pwsh.exe" -Command "gofmt -w internal/storage/sqlite/store_test.go; $cache; go test ./internal/storage/sqlite ./internal/githubmonitor"`)
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

func TestParseCommandKeepsSiblingsOfPowerShellConditional(t *testing.T) {
	command := `"C:\Program Files\PowerShell\7\pwsh.exe" -Command '$env:GOCACHE = Join-Path $env:TEMP '"'wintable-pr21-gocache'; go vet ./...; if ("'$LASTEXITCODE -eq 0) { pwsh -NoProfile -File build.ps1 }'`
	segments, err := ParseCommand(command)
	if !errors.Is(err, ErrDynamicCommand) || len(segments) != 3 {
		t.Fatalf("segments=%#v err=%v", segments, err)
	}
	if !reflect.DeepEqual(segments[1].Argv, []string{"go", "vet", "./..."}) {
		t.Fatalf("vet segment = %#v", segments[1])
	}
	for _, index := range []int{0, 2} {
		if len(segments[index].Argv) != 0 || segments[index].Command == "" {
			t.Fatalf("expected unresolved segment: %#v", segments[index])
		}
	}
	if AllSegmentsAllowed(segments, [][]string{{"*"}}) {
		t.Fatal("unsupported syntax must not match remembered rules")
	}
}

func TestParseCommandKeepsUnsupportedGroupsOpaque(t *testing.T) {
	for _, command := range []string{
		`git status; if ($ok) { echo one; echo two }; go vet ./...`,
		`git status; echo $(whoami; hostname); go vet ./...`,
		`git status; echo value > output.txt; go vet ./...`,
	} {
		segments, err := ParseCommand(command)
		if !errors.Is(err, ErrDynamicCommand) || len(segments) != 3 || len(segments[1].Argv) != 0 || !reflect.DeepEqual(segments[2].Argv, []string{"go", "vet", "./..."}) {
			t.Fatalf("command=%q segments=%#v err=%v", command, segments, err)
		}
	}
	for _, command := range []string{`git status; if ($ok) { echo one`, `git status; echo $(whoami}`, `git status; echo "unterminated`} {
		segments, err := ParseCommand(command)
		if err == nil || AllSegmentsAllowed(segments, [][]string{{"*"}}) {
			t.Fatalf("malformed command resolved: %q: %#v", command, segments)
		}
	}
}

func TestParseCommandKeepsAPICommandAfterPowerShellLabelExpressions(t *testing.T) {
	command := `"C:\Program Files\PowerShell\7\pwsh.exe" -Command '$pr = gh pr view 21 --repo coco-papiyon/wintable --json labels | ConvertFrom-Json; $keep = @($pr.labels.name | Where-Object { $_ -notmatch '"'"'^status:'"' -and "'$_ -notmatch '"'"'^(計画済み|実装済み|要修正|実装'"\\d+回目|要修正\\d+回目)"'$'"' }); "'$payload = @{labels = @($keep) + @('"'status:fix-1')} | ConvertTo-Json; [IO.File]::WriteAllText((Join-Path (Get-Location) '.cache/pr21-labels.json'), "'$payload, [Text.UTF8Encoding]::new($false)); gh api repos/coco-papiyon/wintable/issues/21/labels --method PUT --input .cache/pr21-labels.json'`
	segments, err := ParseCommand(command)
	if !errors.Is(err, ErrDynamicCommand) {
		t.Fatalf("error = %v, want ErrDynamicCommand", err)
	}
	want := [][]string{
		{}, {"ConvertFrom-Json"}, {}, {}, {"ConvertTo-Json"}, {},
		{"gh", "api", "repos/coco-papiyon/wintable/issues/21/labels", "--method", "PUT", "--input", ".cache/pr21-labels.json"},
	}
	if len(segments) != len(want) {
		t.Fatalf("segments = %#v", segments)
	}
	for index, argv := range want {
		if len(argv) != len(segments[index].Argv) || (len(argv) > 0 && !reflect.DeepEqual(segments[index].Argv, argv)) || segments[index].Index != index || segments[index].Command == "" {
			t.Errorf("segment %d = %#v, want argv %#v", index, segments[index], argv)
		}
	}
	if AllSegmentsAllowed(segments, [][]string{{"*"}}) {
		t.Fatal("dynamic label expressions must not be auto-approved")
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

func TestPowerShellEnvironmentAssignmentRules(t *testing.T) {
	for _, command := range []string{
		`$env:GOCACHE="C:\Program Files\cache"`,
		"$env:GOCACHE=\"C:\\cache\tfiles\"",
		`pwsh -Command '$env:GOCACHE="C:\Program Files\cache"'`,
	} {
		parsed, err := ParseCommand(command)
		if err != nil || !AllSegmentsAllowed(parsed, [][]string{{"$env:GOCACHE=*"}}) {
			t.Fatalf("static assignment not allowed: %q: %#v, %v", command, parsed, err)
		}
	}
	segments, err := ParseCommand(`pwsh -Command "$env:GOCACHE = 'C:\tmp\cache'; go test ./..."`)
	if err != nil || len(segments) != 2 {
		t.Fatalf("segments=%#v err=%v", segments, err)
	}
	rule := []string{"$env:GOCACHE=*"}
	if !SegmentAllowed(segments[0], [][]string{rule}) {
		t.Fatal("assignment did not match")
	}
	if AllSegmentsAllowed(segments, [][]string{rule}) {
		t.Fatal("assignment rule allowed following command")
	}
	if MatchArgv(rule, []string{"$env:OTHER=value"}) {
		t.Fatal("matched another variable")
	}
	for _, command := range []string{`$env:GOCACHE=$other`, `$env:GOCACHE="$(whoami)"`, `$env:GOCACHE=Get-Location`, `$env:GOCACHE='x' + 'y'`, `$env:GOCACHE="C:\Program Files\$other"`, `$env:GOCACHE="x" + " y"`, `$env:GOCACHE="unterminated`} {
		parsed, err := ParseCommand(command)
		if err == nil || AllSegmentsAllowed(parsed, [][]string{rule}) {
			t.Fatalf("dynamic assignment resolved: %s", command)
		}
	}
}

func TestPowerShellLiteralVariableAssignment(t *testing.T) {
	for _, command := range []string{
		`$old='C:/data/dev/github/wintable/.cache/issue12/dist/mit-release-acceptance'`,
		`$old = "C:/Program Files/release"`,
		`pwsh -Command "$old='C:/release'"`,
	} {
		segments, err := ParseCommand(command)
		if err != nil || len(segments) != 1 || len(segments[0].Argv) != 1 || !strings.HasPrefix(segments[0].Argv[0], "$old=") {
			t.Fatalf("command=%q segments=%#v err=%v", command, segments, err)
		}
	}
	for _, command := range []string{`$old=$other`, `$old=Get-Location`, `$old="C:/$other"`, `$old='x' + 'y'`, `$old='x'; Remove-Item C:/release`} {
		segments, err := ParseCommand(command)
		if err == nil && AllSegmentsAllowed(segments, [][]string{{"$old=*"}}) {
			t.Fatalf("assignment rule allowed dynamic or sibling command: %q", command)
		}
	}
}
