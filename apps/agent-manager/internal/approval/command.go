package approval

import (
	"errors"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

var ErrDynamicCommand = errors.New("command contains dynamic or unsupported shell syntax")

func ParseCommand(command string) ([]protocol.CommandSegment, error) {
	return parseCommand(command, 0)
}

// parseCommand splits command into per-operation segments wherever it safely
// can, even when one part is unresolvable. A single dynamic or unsupported
// part must never hide the sibling segments that were parsed successfully:
// the caller needs every segment visible so a human can review each one, and
// still refuses to auto-approve the whole command whenever the returned
// error is non-nil.
func parseCommand(command string, depth int) ([]protocol.CommandSegment, error) {
	parts, err := splitCommand(command)
	if err != nil {
		return fallbackSegment(command), err
	}
	segments := make([]protocol.CommandSegment, 0, len(parts))
	var parseErr error
	for _, part := range parts {
		argv, argvErr := splitArgv(part)
		if argvErr != nil || len(argv) == 0 || dynamicExecutable(argv[0]) {
			segments = append(segments, unresolvedSegment(len(segments), part))
			parseErr = ErrDynamicCommand
			continue
		}
		if nested, ok := nestedCommand(argv); ok {
			if depth >= 4 {
				segments = append(segments, unresolvedSegment(len(segments), part))
				parseErr = ErrDynamicCommand
				continue
			}
			inner, innerErr := parseCommand(nested, depth+1)
			if innerErr != nil {
				parseErr = innerErr
				if len(inner) == 1 && len(inner[0].Argv) == 0 {
					inner[0].Command = strings.TrimSpace(part)
				}
			}
			for _, segment := range inner {
				segment.Index = len(segments)
				segments = append(segments, segment)
			}
			continue
		}
		segments = append(segments, protocol.CommandSegment{Index: len(segments), Command: strings.TrimSpace(part), Argv: argv})
	}
	if len(segments) == 0 {
		return fallbackSegment(command), ErrDynamicCommand
	}
	return segments, parseErr
}

// nestedCommand extracts the script passed to a shell wrapper. Approval must
// evaluate the command that the wrapper will execute, rather than allow a
// rule for the wrapper executable to hide the real operation.
func nestedCommand(argv []string) (string, bool) {
	if len(argv) == 0 {
		return "", false
	}
	name := strings.ToLower(strings.TrimSuffix(filepath.Base(strings.ReplaceAll(argv[0], "\\", "/")), ".exe"))
	commandIndex := -1
	for index := 1; index < len(argv); index++ {
		option := strings.ToLower(argv[index])
		switch name {
		case "powershell", "pwsh":
			if option == "-command" || option == "-c" {
				commandIndex = index
			}
			if option == "-encodedcommand" || option == "-e" {
				return "", true
			}
		case "sh", "bash", "dash", "zsh", "ksh":
			if option == "-c" || option == "--command" {
				commandIndex = index
			}
		case "cmd":
			if option == "/c" || option == "/k" {
				commandIndex = index
			}
		}
		if commandIndex >= 0 {
			break
		}
	}
	if commandIndex < 0 || commandIndex+1 >= len(argv) {
		return "", false
	}
	return strings.Join(argv[commandIndex+1:], " "), true
}

func fallbackSegment(command string) []protocol.CommandSegment {
	return []protocol.CommandSegment{{Index: 0, Command: strings.TrimSpace(command), Argv: []string{}}}
}

func unresolvedSegment(index int, part string) protocol.CommandSegment {
	return protocol.CommandSegment{Index: index, Command: strings.TrimSpace(part), Argv: []string{}}
}

func splitCommand(command string) ([]string, error) {
	var parts []string
	start := 0
	quote := rune(0)
	escaped := false
	runes := []rune(command)
	for index := 0; index < len(runes); index++ {
		char := runes[index]
		if escaped {
			escaped = false
			continue
		}
		if char == '\\' && quote != '\'' && index+1 < len(runes) && shellEscapable(runes[index+1]) {
			escaped = true
			continue
		}
		if quote != 0 {
			if char == quote {
				quote = 0
			}
			continue
		}
		if char == '\'' || char == '"' {
			quote = char
			continue
		}
		if char == '`' || char == '(' || char == ')' || char == '<' || char == '>' || char == '{' || char == '}' || (char == '$' && index+1 < len(runes) && runes[index+1] == '(') {
			return nil, ErrDynamicCommand
		}
		separatorLength := 0
		switch char {
		case '|', '&':
			separatorLength = 1
			if index+1 < len(runes) && runes[index+1] == char {
				separatorLength = 2
			}
		case ';', '\n':
			separatorLength = 1
		}
		if separatorLength > 0 {
			part := strings.TrimSpace(string(runes[start:index]))
			if part == "" {
				return nil, ErrDynamicCommand
			}
			parts = append(parts, part)
			index += separatorLength - 1
			start = index + 1
		}
	}
	if quote != 0 || escaped {
		return nil, ErrDynamicCommand
	}
	last := strings.TrimSpace(string(runes[start:]))
	if last != "" {
		parts = append(parts, last)
	}
	return parts, nil
}

func splitArgv(command string) ([]string, error) {
	var argv []string
	var token strings.Builder
	quote := rune(0)
	escaped := false
	flush := func() {
		if token.Len() > 0 {
			argv = append(argv, token.String())
			token.Reset()
		}
	}
	runes := []rune(command)
	for index, char := range runes {
		if escaped {
			token.WriteRune(char)
			escaped = false
			continue
		}
		if char == '\\' && quote != '\'' {
			if index+1 < len(runes) && shellEscapable(runes[index+1]) {
				escaped = true
				continue
			}
			token.WriteRune(char)
			continue
		}
		if quote != 0 {
			if char == quote {
				quote = 0
			} else {
				token.WriteRune(char)
			}
			continue
		}
		if char == '\'' || char == '"' {
			quote = char
			continue
		}
		if unicode.IsSpace(char) {
			flush()
			continue
		}
		token.WriteRune(char)
	}
	if quote != 0 || escaped {
		return nil, ErrDynamicCommand
	}
	flush()
	return argv, nil
}

func shellEscapable(char rune) bool {
	return unicode.IsSpace(char) || strings.ContainsRune(`\"'|&;()<>${}`, char)
}

func dynamicExecutable(executable string) bool {
	value := strings.ToLower(strings.TrimSpace(executable))
	return value == "eval" || value == "invoke-expression" || strings.HasPrefix(value, "$")
}

// SegmentAllowed reports whether segment individually matches one of rules.
// A segment with no argv (unresolved dynamic syntax) never matches, since
// there is nothing static to compare against a rule.
func SegmentAllowed(segment protocol.CommandSegment, rules [][]string) bool {
	if len(segment.Argv) == 0 {
		return false
	}
	for _, rule := range rules {
		if MatchArgv(rule, segment.Argv) {
			return true
		}
	}
	return false
}

func AllSegmentsAllowed(segments []protocol.CommandSegment, rules [][]string) bool {
	if len(segments) == 0 {
		return false
	}
	for _, segment := range segments {
		if !SegmentAllowed(segment, rules) {
			return false
		}
	}
	return true
}

func MatchArgv(pattern, argv []string) bool {
	if len(pattern) == 0 {
		return false
	}
	var match func(int, int) bool
	match = func(patternIndex, argvIndex int) bool {
		if patternIndex == len(pattern) {
			return argvIndex == len(argv)
		}
		if pattern[patternIndex] == "*" {
			for next := argvIndex; next <= len(argv); next++ {
				if match(patternIndex+1, next) {
					return true
				}
			}
			return false
		}
		return argvIndex < len(argv) && equalCommandPart(patternIndex, pattern[patternIndex], argv[argvIndex]) && match(patternIndex+1, argvIndex+1)
	}
	return match(0, 0)
}

func equalCommandPart(index int, expected, actual string) bool {
	if index == 0 {
		expected = strings.TrimSuffix(strings.ToLower(expected), ".exe")
		actual = strings.TrimSuffix(strings.ToLower(actual), ".exe")
	}
	return expected == actual
}
