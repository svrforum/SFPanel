package exec

import "bufio"

const (
	// ScanInitial is the starting capacity of the per-scanner read buffer.
	// 64 KB matches bufio.Scanner's own default and covers the vast majority
	// of subprocess output line lengths without holding more memory than
	// needed up front.
	ScanInitial = 64 * 1024
	// ScanMax is the largest line bufio.Scanner will accept. The library
	// default is 64 KB and any longer line errors with bufio.ErrTooLong,
	// silently truncating the rest of the stream — fatal for log lines,
	// apt-get errors, docker compose output, anything that can emit a
	// long line under uncommon-but-real conditions. 1 MB matches the
	// previous one-off settings in logs/, packages/, and docker/compose.
	ScanMax = 1024 * 1024
)

// PrepareScanner enlarges a fresh *bufio.Scanner's buffer to ScanInitial
// with a ScanMax ceiling. Use this everywhere we scan subprocess output
// or anything else not strictly bounded by the producer — the bufio
// default has bitten enough live handlers that the cost of remembering
// is higher than the cost of always calling this.
func PrepareScanner(s *bufio.Scanner) {
	s.Buffer(make([]byte, ScanInitial), ScanMax)
}
