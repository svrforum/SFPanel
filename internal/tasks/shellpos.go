package tasks

import "strings"

// Where a reference sits in a command step's shell text.
//
// Render replaces a reference in a command with ShellQuote(value), which is
// one word only where the shell reads a word. Inside quotes the quotes it
// adds close the author's and open the value to the shell; inside a heredoc
// body, a substitution or a comment it is not a word at all. shellPlaces
// reads the text the way /bin/sh does, far enough to say for each reference
// whether it stands where a word goes, and refuses it otherwise.
//
// The scanner is deliberately small and errs one way: whatever it does not
// follow ($'…', $[…], quotes inside ${…}, case patterns inside ( ) or $( ),
// a heredoc delimiter it cannot read) stops it, and every reference from
// there on is refused rather than guessed at. Accepting a reference the
// shell reads inside quotes is the failure that matters; refusing one it
// would have read as a word only costs the author a rewrite.

// placeVerdict is how the scanner judged one reference.
type placeVerdict int

const (
	placeUnread   placeVerdict = iota // never reached in a judged place: refused
	placeWord                         // where a word goes: accepted
	placeEnclosed                     // inside quotes, a substitution, a heredoc or a comment
)

// maxShellNesting bounds the scanner's recursion; deeper text is not
// followed.
const maxShellNesting = 64

// shellRef is one reference the scanner is to judge, or skip over.
type shellRef struct {
	name    string
	start   int
	end     int
	judged  bool // a quoted reference; raw and undefined ones are only skipped
	verdict placeVerdict
}

type shellScanner struct {
	s       string
	refs    map[int]*shellRef // by start offset
	lost    bool              // past something not followed: judge nothing more
	pending []heredoc         // heredocs whose bodies start at the next newline
}

type heredoc struct {
	delim     string
	stripTabs bool
}

// shellPlaces judges every reference in s that quote says is quoted when
// rendered, and returns those in document order.
func shellPlaces(s string, quoted func(name string) bool, known func(name string) bool) []*shellRef {
	sc := &shellScanner{s: s, refs: map[int]*shellRef{}}
	var order []*shellRef
	for _, m := range referencePattern.FindAllStringSubmatchIndex(s, -1) {
		name := s[m[2]:m[3]]
		r := &shellRef{name: name, start: m[0], end: m[1], judged: known(name) && quoted(name)}
		sc.refs[m[0]] = r
		order = append(order, r)
	}
	if len(order) == 0 {
		return nil
	}
	sc.command(0, false, true, 0)
	var out []*shellRef
	for _, r := range order {
		if r.judged {
			out = append(out, r)
		}
	}
	return out
}

// ref handles a reference starting at i, if there is one: a judged one gets
// the verdict for where it stands. It returns the offset after it.
func (sc *shellScanner) ref(i int, verdict placeVerdict) (int, bool) {
	r, ok := sc.refs[i]
	if !ok {
		return i, false
	}
	if r.judged && !sc.lost {
		r.verdict = verdict
	}
	return r.end, true
}

func (sc *shellScanner) lose() int {
	sc.lost = true
	return len(sc.s)
}

func isShellBlank(c byte) bool { return c == ' ' || c == '\t' }

// isShellBreak is a character that ends a word without being part of the
// next one.
func isShellBreak(c byte) bool {
	return isShellBlank(c) || c == '\n' || strings.IndexByte(";&|()<>", c) >= 0
}

// command reads a command context: the top level, a subshell, or the inside
// of $( ) / <( ). words says whether a reference here is in a word place
// (not inside a substitution); closer says a ')' ends it. It returns the
// offset after the closing ')', or the end of the text.
func (sc *shellScanner) command(i int, closer, words bool, depth int) int {
	if depth > maxShellNesting {
		return sc.lose()
	}
	s := sc.s
	wordStart := true
	for i < len(s) && !sc.lost {
		verdict := placeEnclosed
		if words {
			verdict = placeWord
		}
		if end, ok := sc.ref(i, verdict); ok {
			i, wordStart = end, false
			continue
		}
		c := s[i]
		switch {
		case c == '\\':
			if i+1 < len(s) && s[i+1] == '\n' {
				i += 2 // a line continuation joins the lines: nothing changes
				continue
			}
			if _, ok := sc.refs[i+1]; ok {
				// \'value' escapes the quote Render opens.
				return sc.lose()
			}
			i, wordStart = i+2, false
		case c == '\'':
			i, wordStart = sc.single(i+1), false
		case c == '"':
			i, wordStart = sc.double(i+1, depth), false
		case c == '`':
			i, wordStart = sc.backtick(i+1), false
		case c == '$':
			i, wordStart = sc.dollar(i, false, depth), false
		case c == '(' && i+1 < len(s) && s[i+1] == '(':
			i, wordStart = sc.arith(i+2), false
		case c == '(':
			sub := words
			if i > 0 && (s[i-1] == '<' || s[i-1] == '>') {
				sub = false // <( ) and >( ) are substitutions
			}
			i, wordStart = sc.command(i+1, true, sub, depth+1), true
		case c == ')':
			if closer {
				return i + 1
			}
			// A case pattern's ')' at the top level closes nothing.
			i, wordStart = i+1, true
		case c == '#' && wordStart:
			i = sc.comment(i)
		case c == '<' && strings.HasPrefix(s[i:], "<<<"):
			i, wordStart = i+3, true
		case c == '<' && strings.HasPrefix(s[i:], "<<"):
			i, wordStart = sc.heredocStart(i+2), false
		case c == '\n':
			i, wordStart = sc.heredocBodies(i+1), true
		case isShellBreak(c):
			i, wordStart = i+1, true
		case wordStart && depth > 0 && strings.HasPrefix(s[i:], "case") && (i+4 == len(s) || isShellBreak(s[i+4])):
			// A case pattern's unbalanced ')' would close this context
			// early; that is not followed.
			return sc.lose()
		default:
			i, wordStart = i+1, false
		}
	}
	return i
}

// single reads to the end of a single-quoted string.
func (sc *shellScanner) single(i int) int {
	for i < len(sc.s) {
		if end, ok := sc.ref(i, placeEnclosed); ok {
			i = end
			continue
		}
		if sc.s[i] == '\'' {
			return i + 1
		}
		i++
	}
	return i
}

// double reads to the end of a double-quoted string.
func (sc *shellScanner) double(i, depth int) int {
	s := sc.s
	for i < len(s) && !sc.lost {
		if end, ok := sc.ref(i, placeEnclosed); ok {
			i = end
			continue
		}
		switch s[i] {
		case '"':
			return i + 1
		case '\\':
			i += 2
		case '`':
			i = sc.backtick(i + 1)
		case '$':
			i = sc.dollar(i, true, depth)
		default:
			i++
		}
	}
	return min(i, len(s))
}

// backtick reads to the first unescaped backquote, which is where both
// bash and dash end an old-style command substitution.
func (sc *shellScanner) backtick(i int) int {
	s := sc.s
	for i < len(s) {
		if end, ok := sc.ref(i, placeEnclosed); ok {
			i = end
			continue
		}
		switch s[i] {
		case '`':
			return i + 1
		case '\\':
			i += 2
		default:
			i++
		}
	}
	return min(i, len(s))
}

// dollar reads what a '$' at i starts, inside double quotes or not.
func (sc *shellScanner) dollar(i int, inDouble bool, depth int) int {
	s := sc.s
	next := byte(0)
	if i+1 < len(s) {
		next = s[i+1]
	}
	switch {
	case strings.HasPrefix(s[i:], "$(("):
		return sc.arith(i + 3)
	case next == '(':
		return sc.command(i+2, true, false, depth+1)
	case next == '{':
		return sc.param(i+2, depth)
	case next == '[':
		return sc.lose() // $[ ] is bash's old arithmetic
	case next == '\'' && !inDouble:
		return sc.lose() // $' ' is bash's escaped string; dash reads it otherwise
	case !inDouble && sc.refs[i+1] != nil:
		return sc.lose() // $'value' would be read as $' '
	}
	return i + 1
}

// param reads a ${ } expansion. Plain ones are followed; quotes, escapes and
// substitutions inside one are read differently by different shells, so
// they are not.
func (sc *shellScanner) param(i, depth int) int {
	s := sc.s
	for i < len(s) && !sc.lost {
		if end, ok := sc.ref(i, placeEnclosed); ok {
			i = end
			continue
		}
		switch s[i] {
		case '}':
			return i + 1
		case '\'', '"', '`', '\\':
			return sc.lose()
		case '$':
			if i+1 < len(s) && s[i+1] == '{' {
				if depth > maxShellNesting {
					return sc.lose()
				}
				i = sc.param(i+2, depth+1)
				continue
			}
			if i+1 < len(s) && s[i+1] == '(' {
				return sc.lose()
			}
			i++
		default:
			i++
		}
	}
	return min(i, len(s))
}

// arith reads (( )) or $(( )) to its closing "))".
func (sc *shellScanner) arith(i int) int {
	s := sc.s
	open := 0
	for i < len(s) && !sc.lost {
		if end, ok := sc.ref(i, placeEnclosed); ok {
			i = end
			continue
		}
		switch s[i] {
		case '(':
			open++
		case ')':
			if open > 0 {
				open--
				break
			}
			if i+1 < len(s) && s[i+1] == ')' {
				return i + 2
			}
			// dash reads (( as two subshells; this one was not arithmetic.
			return sc.lose()
		case '\'', '"', '`', '\\':
			return sc.lose()
		}
		i++
	}
	return i
}

// comment reads to the end of the line, leaving the newline.
func (sc *shellScanner) comment(i int) int {
	for i < len(sc.s) && sc.s[i] != '\n' {
		if end, ok := sc.ref(i, placeEnclosed); ok {
			i = end
			continue
		}
		i++
	}
	return i
}

// heredocStart reads a heredoc's delimiter after "<<" and queues its body
// for the next newline.
func (sc *shellScanner) heredocStart(i int) int {
	s := sc.s
	h := heredoc{}
	if i < len(s) && s[i] == '-' {
		h.stripTabs = true
		i++
	}
	for i < len(s) && isShellBlank(s[i]) {
		i++
	}
	var delim strings.Builder
	for i < len(s) && !isShellBreak(s[i]) {
		if _, ok := sc.refs[i]; ok {
			return sc.lose()
		}
		switch c := s[i]; c {
		case '\'', '"':
			end := strings.IndexByte(s[i+1:], c)
			if end < 0 {
				return sc.lose()
			}
			part := s[i+1 : i+1+end]
			if c == '"' && strings.ContainsAny(part, "$`\\") {
				return sc.lose()
			}
			delim.WriteString(part)
			i += end + 2
		case '\\':
			if i+1 >= len(s) {
				return sc.lose()
			}
			delim.WriteByte(s[i+1])
			i += 2
		case '$', '`':
			return sc.lose()
		default:
			delim.WriteByte(c)
			i++
		}
	}
	if delim.Len() == 0 || strings.Contains(delim.String(), "\n") {
		return sc.lose()
	}
	h.delim = delim.String()
	sc.pending = append(sc.pending, h)
	return i
}

// heredocBodies reads the bodies of the heredocs queued on the line that
// just ended, from i, and returns where the command text goes on.
func (sc *shellScanner) heredocBodies(i int) int {
	s := sc.s
	for len(sc.pending) > 0 {
		h := sc.pending[0]
		sc.pending = sc.pending[1:]
		for {
			if i >= len(s) {
				return len(s)
			}
			lineEnd := strings.IndexByte(s[i:], '\n')
			next := len(s)
			if lineEnd >= 0 {
				lineEnd += i
				next = lineEnd + 1
			} else {
				lineEnd = len(s)
			}
			line := s[i:lineEnd]
			if h.stripTabs {
				line = strings.TrimLeft(line, "\t")
			}
			if line == h.delim {
				i = next
				break
			}
			for j := i; j < lineEnd; j++ {
				if end, ok := sc.ref(j, placeEnclosed); ok {
					j = end - 1
				}
			}
			i = next
		}
	}
	return i
}
