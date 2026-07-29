// lex.go
package text

import (
	"fmt"
	"strconv"
	"strings"
)

// tokKind identifies the lexical category of a token.
type tokKind int

const (
	tEOF tokKind = iota
	tEOL // §2: ".gvir is line-oriented; no separators or continuations"
	tIdent
	tInt
	tFloat
	tString
	tPunct // one of ( ) , : [ ] . - =
)

type token struct {
	kind tokKind
	s    string // ident text / unescaped string value / punct spelling / raw numeric text
	i    int64
	f    float64
	hex  bool // float literal was written in the hex-float spelling (§2)
	line int
}

func (t token) String() string {
	switch t.kind {
	case tEOF:
		return "<eof>"
	case tEOL:
		return "<end of line>"
	case tIdent:
		return fmt.Sprintf("identifier %q", t.s)
	case tInt:
		return fmt.Sprintf("integer %s", t.s)
	case tFloat:
		return fmt.Sprintf("float %s", t.s)
	case tString:
		return fmt.Sprintf("string %q", t.s)
	case tPunct:
		return fmt.Sprintf("%q", t.s)
	}
	return "?"
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isHexDigit(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}
func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
func isIdentPart(c byte) bool  { return isIdentStart(c) || isDigit(c) }
func isPunctChar(c byte) bool  { return strings.IndexByte("(),:[].-=", c) >= 0 }

// tokenize lexes .gvir source into a token stream terminated by tEOF.
//
// Unlike the .vir lexer, newlines are *significant*: §2 makes .gvir
// line-oriented with no continuations, and several constructs are not
// self-delimiting without that. `i = thread_in_grid.x` takes no operands
// (§9), so nothing but the line break separates it from the instruction
// below it. One tEOL is emitted per newline; the parser eats runs of them,
// so blank and comment-only lines cost nothing.
//
// Indentation is conventional (§2) and is discarded here.
func tokenize(src []byte) ([]token, error) {
	var toks []token
	line := 1
	i, n := 0, len(src)

	for i < n {
		c := src[i]
		switch {
		case c == '\n':
			toks = append(toks, token{kind: tEOL, line: line})
			line++
			i++
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case c == '/' && i+1 < n && src[i+1] == '/':
			// Comment to end of line; the newline itself still terminates.
			for i < n && src[i] != '\n' {
				i++
			}
		case c == '"':
			start := i
			i++
			for i < n && src[i] != '"' && src[i] != '\n' {
				if src[i] == '\\' && i+1 < n && src[i+1] != '\n' {
					i += 2
					continue
				}
				i++
			}
			if i >= n || src[i] != '"' {
				// No continuations: a string cannot span a line break.
				return nil, fmt.Errorf("line %d: unterminated string literal", line)
			}
			i++ // consume closing quote
			raw := string(src[start:i])
			val, err := strconv.Unquote(raw)
			if err != nil {
				// Fall back to the bytes between the quotes.
				val = raw[1 : len(raw)-1]
			}
			toks = append(toks, token{kind: tString, s: val, line: line})
		case isDigit(c):
			t, next, err := lexNumber(src, i, line)
			if err != nil {
				return nil, err
			}
			toks = append(toks, t)
			i = next
		case isIdentStart(c):
			start := i
			for i < n && isIdentPart(src[i]) {
				i++
			}
			text := string(src[start:i])
			// §2 forbids "__" module-wide — it is reserved as the host
			// symbol ABI separator. This is a lexical rule, not a semantic
			// one, so it belongs here rather than in ir/verify.
			if strings.Contains(text, "__") {
				return nil, fmt.Errorf("line %d: %q contains \"__\", reserved as the host symbol ABI separator (§2)", line, text)
			}
			toks = append(toks, token{kind: tIdent, s: text, line: line})
		case isPunctChar(c):
			toks = append(toks, token{kind: tPunct, s: string(c), line: line})
			i++
		default:
			return nil, fmt.Errorf("line %d: unexpected character %q", line, c)
		}
	}
	toks = append(toks, token{kind: tEOF, line: line})
	return toks, nil
}

// lexNumber lexes an int-literal, a dec-float or a hex-float (§2). The raw
// text is kept on the token: the version declaration needs it to read
// "1.0" as a major/minor pair rather than as a float, and diagnostics read
// better with the source spelling.
//
// The §2 productions are narrower than Go's, and the differences are worth
// a real diagnostic rather than a downstream "unknown type" from the ident
// the leftovers would have lexed as:
//
//	dec-float := "-"? [0-9]+ "." [0-9]+ ("e" "-"? [0-9]+)?
//	hex-float := "-"? "0x" hex+ ("." hex+)? "p" "-"? [0-9]+
//
// The leading "-" is punctuation here; the parser applies it.
func lexNumber(src []byte, i, line int) (token, int, error) {
	start := i
	n := len(src)

	if src[i] == '0' && i+1 < n && src[i+1] == 'x' {
		i += 2
		digits := 0
		for i < n && isHexDigit(src[i]) {
			i++
			digits++
		}
		if i < n && src[i] == '.' {
			i++
			for i < n && isHexDigit(src[i]) {
				i++
				digits++
			}
		}
		if digits == 0 {
			return token{}, 0, fmt.Errorf("line %d: hex-float literal has no digits", line)
		}
		if i >= n || src[i] != 'p' {
			return token{}, 0, fmt.Errorf("line %d: hex-float literal %q requires a 'p' exponent (§2)", line, string(src[start:i]))
		}
		i++
		if i < n && src[i] == '+' {
			return token{}, 0, fmt.Errorf("line %d: a hex-float exponent sign may only be '-' (§2)", line)
		}
		if i < n && src[i] == '-' {
			i++
		}
		expDigits := 0
		for i < n && isDigit(src[i]) {
			i++
			expDigits++
		}
		if expDigits == 0 {
			return token{}, 0, fmt.Errorf("line %d: hex-float literal %q has an empty exponent", line, string(src[start:i]))
		}
		text := string(src[start:i])
		f, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return token{}, 0, fmt.Errorf("line %d: bad hex-float literal %q", line, text)
		}
		return token{kind: tFloat, s: text, f: f, hex: true, line: line}, i, nil
	}

	for i < n && isDigit(src[i]) {
		i++
	}
	isFloat := false
	if i+1 < n && src[i] == '.' && isDigit(src[i+1]) {
		isFloat = true
		i++
		for i < n && isDigit(src[i]) {
			i++
		}
		if i < n && src[i] == 'E' {
			return token{}, 0, fmt.Errorf("line %d: a dec-float exponent is spelled 'e' (§2)", line)
		}
		if i < n && src[i] == 'e' {
			j := i + 1
			if j < n && src[j] == '+' {
				return token{}, 0, fmt.Errorf("line %d: a dec-float exponent sign may only be '-' (§2)", line)
			}
			if j < n && src[j] == '-' {
				j++
			}
			if j >= n || !isDigit(src[j]) {
				return token{}, 0, fmt.Errorf("line %d: dec-float exponent has no digits (§2)", line)
			}
			i = j
			for i < n && isDigit(src[i]) {
				i++
			}
		}
	}
	text := string(src[start:i])

	if !isFloat && i < n && (src[i] == 'e' || src[i] == 'E') {
		return token{}, 0, fmt.Errorf("line %d: %q — a dec-float carries digits on both sides of '.' (§2)", line, text+string(src[i]))
	}

	if isFloat {
		f, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return token{}, 0, fmt.Errorf("line %d: bad float literal %q", line, text)
		}
		return token{kind: tFloat, s: text, f: f, line: line}, i, nil
	}
	v, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return token{}, 0, fmt.Errorf("line %d: bad integer literal %q", line, text)
	}
	return token{kind: tInt, s: text, i: v, line: line}, i, nil
}