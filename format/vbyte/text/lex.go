// lex.go
package text

import (
	"fmt"
	"strconv"
)

// tokKind identifies the lexical category of a token.
type tokKind int

const (
	tEOF tokKind = iota
	tIdent
	tInt
	tFloat
	tString
	tPunct // one of ( ) , : [ ] . - =
)

type token struct {
	kind tokKind
	s    string // ident text / unescaped string value / punct spelling
	i    int64
	f    float64
	line int
}

func (t token) String() string {
	switch t.kind {
	case tEOF:
		return "<eof>"
	case tIdent:
		return fmt.Sprintf("identifier %q", t.s)
	case tInt:
		return fmt.Sprintf("integer %d", t.i)
	case tFloat:
		return fmt.Sprintf("float %g", t.f)
	case tString:
		return fmt.Sprintf("string %q", t.s)
	case tPunct:
		return fmt.Sprintf("%q", t.s)
	}
	return "?"
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
func isIdentPart(c byte) bool { return isIdentStart(c) || isDigit(c) }

// tokenize lexes the entire .vir source into a token stream, terminated by
// a single tEOF token. Newlines and indentation are not significant (§2:
// "Indentation is purely conventional") — every construct in the grammar
// is self-delimiting via keywords, parens, commas, or "end", so this
// lexer treats all whitespace uniformly and comments ("//" to end of
// line) are stripped entirely.
func tokenize(src []byte) ([]token, error) {
	var toks []token
	line := 1
	i := 0
	n := len(src)

	for i < n {
		c := src[i]
		switch {
		case c == '\n':
			line++
			i++
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case c == '/' && i+1 < n && src[i+1] == '/':
			for i < n && src[i] != '\n' {
				i++
			}
		case c == '"':
			startLine := line
			start := i
			i++
			for i < n && src[i] != '"' {
				if src[i] == '\\' && i+1 < n {
					i += 2
					continue
				}
				if src[i] == '\n' {
					line++
				}
				i++
			}
			if i >= n {
				return nil, fmt.Errorf("line %d: unterminated string literal", startLine)
			}
			i++ // consume closing quote
			raw := string(src[start:i])
			val, err := strconv.Unquote(raw)
			if err != nil {
				// Fall back to the literal grammar (§2.3): no escape
				// processing, just the bytes between the quotes.
				val = raw[1 : len(raw)-1]
			}
			toks = append(toks, token{kind: tString, s: val, line: startLine})
		case isDigit(c):
			start := i
			for i < n && isDigit(src[i]) {
				i++
			}
			isFloat := false
			if i < n && src[i] == '.' && i+1 < n && isDigit(src[i+1]) {
				isFloat = true
				i++
				for i < n && isDigit(src[i]) {
					i++
				}
			}
			if i < n && (src[i] == 'e' || src[i] == 'E') {
				j := i + 1
				if j < n && (src[j] == '+' || src[j] == '-') {
					j++
				}
				if j < n && isDigit(src[j]) {
					isFloat = true
					i = j
					for i < n && isDigit(src[i]) {
						i++
					}
				}
			}
			text := string(src[start:i])
			if isFloat {
				f, err := strconv.ParseFloat(text, 64)
				if err != nil {
					return nil, fmt.Errorf("line %d: bad float literal %q", line, text)
				}
				toks = append(toks, token{kind: tFloat, f: f, line: line})
			} else {
				v, err := strconv.ParseInt(text, 10, 64)
				if err != nil {
					return nil, fmt.Errorf("line %d: bad integer literal %q", line, text)
				}
				toks = append(toks, token{kind: tInt, i: v, line: line})
			}
		case isIdentStart(c):
			start := i
			for i < n && isIdentPart(src[i]) {
				i++
			}
			toks = append(toks, token{kind: tIdent, s: string(src[start:i]), line: line})
		case c == '(' || c == ')' || c == ',' || c == ':' || c == '[' || c == ']' || c == '.' || c == '-' || c == '=':
			toks = append(toks, token{kind: tPunct, s: string(c), line: line})
			i++
		default:
			return nil, fmt.Errorf("line %d: unexpected character %q", line, c)
		}
	}
	toks = append(toks, token{kind: tEOF, line: line})
	return toks, nil
}