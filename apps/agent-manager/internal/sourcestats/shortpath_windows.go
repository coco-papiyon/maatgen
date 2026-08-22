//go:build windows

package sourcestats

import "golang.org/x/sys/windows"

// toShortPath resolves repository to its 8.3 short path form when the
// filesystem supports it. cloc's bundled Perl mishandles non-ASCII Windows
// paths (e.g. a Japanese folder name on a Google Drive volume): it chdirs
// into subdirectories while scanning and then fails to chdir back because the
// cached working directory string round-trips through the wrong codepage.
// The short path is pure ASCII, so it sidesteps that bug entirely. If the
// volume doesn't support short names (common for virtual/cloud-synced
// drives), this returns the original path unchanged.
func toShortPath(path string) string {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return path
	}
	buffer := make([]uint16, 4096)
	length, err := windows.GetShortPathName(pointer, &buffer[0], uint32(len(buffer)))
	if err != nil || length == 0 || int(length) > len(buffer) {
		return path
	}
	short := windows.UTF16ToString(buffer[:length])
	if short == "" {
		return path
	}
	return short
}
