package approval

import (
	"errors"
	"strings"
	"unicode"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

var ErrDynamicCommand = errors.New("command contains dynamic or unsupported shell syntax")

func ParseCommand(command string) ([]protocol.CommandSegment, error) {
	parts, err := splitCommand(command)
	if err != nil {
		return fallbackSegment(command), err
	}
	segments := make([]protocol.CommandSegment, 0, len(parts))
	for _, part := range parts {
		argv, err := splitArgv(part)
		if err != nil || len(argv) == 0 || dynamicExecutable(argv[0]) {
			return fallbackSegment(command), ErrDynamicCommand
		}
		segments = append(segments, protocol.CommandSegment{Index: len(segments), Command: strings.TrimSpace(part), Argv: argv})
	}
	if len(segments) == 0 {
		return fallbackSegment(command), ErrDynamicCommand
	}
	return segments, nil
}

func fallbackSegment(command string) []protocol.CommandSegment {
	return []protocol.CommandSegment{{Index: 0, Command: strings.TrimSpace(command), Argv: []string{}}}
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

func AllSegmentsAllowed(segments []protocol.CommandSegment, rules [][]string) bool {
	if len(segments) == 0 {
		return false
	}
	for _, segment := range segments {
		if len(segment.Argv) == 0 {
			return false
		}
		allowed := false
		for _, rule := range rules {
			if MatchArgv(rule, segment.Argv) {
				allowed = true
				break
			}
		}
		if !allowed {
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
