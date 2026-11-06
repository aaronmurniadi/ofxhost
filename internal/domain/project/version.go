package project

import (
	"strconv"
	"strings"
)

// VersionParts returns the numeric parts of a version string or a release tag.
//
// It accepts a leading "v", and stops at the first character that is not a digit
// or a dot, so "v0.4.2-dev-abc1234" reads as {0, 4, 2} and "0.4.2" reads the
// same. The second result is false when the text does not start with a number,
// which is what a branch name or an error page looks like.
func VersionParts(text string) ([]int, bool) {
	trimmed := strings.TrimSpace(text)
	trimmed = strings.TrimPrefix(trimmed, "v")
	trimmed = strings.TrimPrefix(trimmed, "V")
	if trimmed == "" {
		return nil, false
	}
	if trimmed[0] < '0' || trimmed[0] > '9' {
		return nil, false
	}

	end := len(trimmed)
	for i := 0; i < len(trimmed); i++ {
		c := trimmed[i]
		if (c < '0' || c > '9') && c != '.' {
			end = i
			break
		}
	}

	var parts []int
	for _, field := range strings.Split(trimmed[:end], ".") {
		if field == "" {
			// "1..2" is a typo rather than a version, and counting the empty
			// field as zero would make it compare equal to "1.0.2".
			return nil, false
		}
		n, err := strconv.Atoi(field)
		if err != nil {
			return nil, false
		}
		parts = append(parts, n)
	}
	if len(parts) == 0 {
		return nil, false
	}
	return parts, true
}

// CompareVersions orders two versions by their numbers.
//
// It returns a negative value when left is older, zero when the numbers match,
// and a positive value when left is newer. A missing part counts as zero, so
// "0.4" and "0.4.0" are equal. Text with no number sorts before a numbered
// version, which keeps an unreadable tag from winning a comparison.
func CompareVersions(left, right string) int {
	lp, lok := VersionParts(left)
	rp, rok := VersionParts(right)

	switch {
	case !lok && !rok:
		return 0
	case !lok:
		return -1
	case !rok:
		return 1
	}

	n := len(lp)
	if len(rp) > n {
		n = len(rp)
	}
	for i := 0; i < n; i++ {
		var l, r int
		if i < len(lp) {
			l = lp[i]
		}
		if i < len(rp) {
			r = rp[i]
		}
		if l != r {
			if l < r {
				return -1
			}
			return 1
		}
	}
	return 0
}

// SameVersion reports whether a release tag names a build version.
//
// The tag carries a "v" prefix that the version does not, and a development build
// carries a suffix the tag also carries, so the comparison is on the numbers.
func SameVersion(tag, version string) bool {
	return CompareVersions(tag, version) == 0
}

// ReleaseIsNewer reports whether the release the checker found is newer than the
// build that is running.
//
// It returns the newest release's tag, and whether the running build should offer
// it. The second result is false when the running build already matches or beats
// the newest release.
func ReleaseIsNewer(newestTag, currentVersion string) (string, bool) {
	if newestTag == "" {
		return "", false
	}
	if CompareVersions(newestTag, currentVersion) <= 0 {
		return "", false
	}
	return newestTag, true
}

// DisplayVersion returns a version as a user reads it: the About panel shows the
// build version without the "v" of the release tag.
func DisplayVersion(tagOrVersion string) string {
	trimmed := strings.TrimSpace(tagOrVersion)
	trimmed = strings.TrimPrefix(trimmed, "v")
	trimmed = strings.TrimPrefix(trimmed, "V")
	return trimmed
}
