//go:build !windows

package sourcestats

func toShortPath(path string) string { return path }
