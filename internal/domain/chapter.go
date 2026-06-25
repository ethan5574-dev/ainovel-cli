package domain

import (
	"fmt"
	"unicode/utf8"
)

// ReviewInterval is the global review interval (triggered every N chapters).
const ReviewInterval = 5

// ShouldReview determines whether a global review is needed based on the number of completed chapters (short/medium story mode).
func ShouldReview(completedCount int) (bool, string) {
	if completedCount > 0 && completedCount%ReviewInterval == 0 {
		return true, fmt.Sprintf("Completed %d chapters, triggering global review", completedCount)
	}
	return false, ""
}

// ShouldArcReview determines whether an arc-level/volume-level review is needed in long-form mode.
func ShouldArcReview(isArcEnd, isVolumeEnd bool, volume, arc int) (bool, string) {
	if isVolumeEnd {
		return true, fmt.Sprintf("Volume %d Arc %d ended (Volume end), triggering arc+volume review", volume, arc)
	}
	if isArcEnd {
		return true, fmt.Sprintf("Volume %d Arc %d ended, triggering arc review", volume, arc)
	}
	return false, ""
}

// WordCount counts the number of runes in a string.
func WordCount(content string) int {
	return utf8.RuneCountInString(content)
}
