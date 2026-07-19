// Package text implements the .vir human-readable form (README arrow 2).
package text

import (
	"fmt"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Lexer — one token stream per logical line (line breaks are significant, §1)
// ---------------------------------------------------------------------------

type tokKind int

const (
	tIdent tokKind = iota
	tInt
	tFloat
	tString
	tPunct // one of , ( ) [ ] : = . % $ # + * !
	tEllipsis
)

type tok struct {
	kind tokKind
	text string
}

type line struct {
	num  int
	toks []tok
}

type parser struct {
	lines []line
	pos   int
}

func (p *parser) lexAll(src string) error {
	for i, raw := range strings.Split(src, "\n") {
		toks, err := lexLine(raw)
		if err != nil {
			return fmt.Errorf("line %d: %w", i+1, err)
		}
		if len(toks) > 0 {
			p.lines = append(p.lines, line{num: i + 1, toks: toks})
		}
	}
	return nil
}

// lexLine tokenizes both ordinary module-grammar lines and asm-block lines.
// Asm operands (registers, immediates, memory refs) are "independently
// lexed" per §4, but we still use this single lexer for them — it just
// additionally recognizes the punctuation asm syntax needs ('%','$','#',
// '+','*','!'); the asm-specific *parsing* (readAsmMemory etc.) is what
// actually gives those characters their dialect-specific meaning.
func lexLine(s string) ([]tok, error) {
	var toks []tok
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case c == '/' && i+1 < len(s) && s[i+1] == '/':
			return toks, nil // comment to end of line (§3)
		case c == '"':
			j := i + 1
			var b strings.Builder
			for j < len(s) && s[j] != '"' {
				if s[j] == '\\' {
					if j+1 >= len(s) {
						return nil, fmt.Errorf("unterminated escape")
					}
					switch s[j+1] {
					case '0':
						b.WriteByte(0)
					case 'n':
						b.WriteByte('\n')
					case 'r':
						b.WriteByte('\r')
					case 't':
						b.WriteByte('\t')
					case '\\':
						b.WriteByte('\\')
					case '"':
						b.WriteByte('"')
					case 'x':
						if j+3 >= len(s) {
							return nil, fmt.Errorf("bad \\x escape")
						}
						v, err := strconv.ParseUint(s[j+2:j+4], 16, 8)
						if err != nil {
							return nil, fmt.Errorf("bad \\x escape: %w", err)
						}
						b.WriteByte(byte(v))
						j += 2
					default:
						return nil, fmt.Errorf("unknown escape \\%c", s[j+1])
					}
					j += 2
				} else {
					b.WriteByte(s[j])
					j++
				}
			}
			if j >= len(s) {
				return nil, fmt.Errorf("unterminated string literal")
			}
			toks = append(toks, tok{tString, b.String()})
			i = j + 1
		case strings.HasPrefix(s[i:], "..."):
			toks = append(toks, tok{tEllipsis, "..."})
			i += 3
		case strings.HasPrefix(s[i:], "-Inf"):
			// float-literal alt "-Inf" (§1.1); must be checked before the
			// generic '-'-prefixed-number case below, which requires a digit.
			toks = append(toks, tok{tIdent, "-Inf"})
			i += 4
		case c == ',' || c == '(' || c == ')' || c == '[' || c == ']' || c == ':' ||
			c == '=' || c == '.' || c == '%' || c == '$' || c == '#' || c == '+' ||
			c == '*' || c == '!':
			toks = append(toks, tok{tPunct, string(c)})
			i++
		case c == '-' || (c >= '0' && c <= '9'):
			j := i
			if c == '-' {
				j++
				if j >= len(s) || s[j] < '0' || s[j] > '9' {
					return nil, fmt.Errorf("stray '-'")
				}
			}
			isFloat := false
			for j < len(s) {
				d := s[j]
				if d >= '0' && d <= '9' {
					j++
				} else if d == '.' && j+1 < len(s) && s[j+1] >= '0' && s[j+1] <= '9' {
					isFloat = true
					j++
				} else if (d == 'e' || d == 'E') && isFloat {
					j++
					if j < len(s) && s[j] == '-' {
						j++
					}
				} else {
					break
				}
			}
			k := tInt
			if isFloat {
				k = tFloat
			}
			toks = append(toks, tok{k, s[i:j]})
			i = j
		case isIdentStart(c):
			j := i
			for j < len(s) && isIdentChar(s[j]) {
				j++
			}
			toks = append(toks, tok{tIdent, s[i:j]})
			i = j
		default:
			return nil, fmt.Errorf("unexpected character %q", c)
		}
	}
	return toks, nil
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
func isIdentChar(c byte) bool { return isIdentStart(c) || (c >= '0' && c <= '9') }

// ---------------------------------------------------------------------------
// Line cursor helpers
// ---------------------------------------------------------------------------

func (p *parser) peek() *line {
	if p.pos >= len(p.lines) {
		return nil
	}
	return &p.lines[p.pos]
}

func (p *parser) next() *line {
	l := p.peek()
	if l != nil {
		p.pos++
	}
	return l
}

func (l *line) errf(format string, args ...any) error {
	return fmt.Errorf("line %d: %s", l.num, fmt.Sprintf(format, args...))
}

// first returns the leading identifier of a line, or "" if the line doesn't
// start with one. Used throughout to dispatch on the line's keyword without
// committing to a parse.
func first(l *line) string {
	if len(l.toks) > 0 && l.toks[0].kind == tIdent {
		return l.toks[0].text
	}
	return ""
}

// cursor within one line
type lc struct {
	l *line
	i int
}

func (c *lc) peek() (tok, bool) {
	if c.i >= len(c.l.toks) {
		return tok{}, false
	}
	return c.l.toks[c.i], true
}
func (c *lc) peekN(n int) (tok, bool) {
	idx := c.i + n
	if idx < 0 || idx >= len(c.l.toks) {
		return tok{}, false
	}
	return c.l.toks[idx], true
}
func (c *lc) next() (tok, bool) {
	t, ok := c.peek()
	if ok {
		c.i++
	}
	return t, ok
}
func (c *lc) accept(kind tokKind, text string) bool {
	t, ok := c.peek()
	if ok && t.kind == kind && (text == "" || t.text == text) {
		c.i++
		return true
	}
	return false
}
func (c *lc) expectIdent() (string, error) {
	t, ok := c.next()
	if !ok || t.kind != tIdent {
		return "", c.l.errf("expected identifier")
	}
	return t.text, nil
}
func (c *lc) expectPunct(p string) error {
	if !c.accept(tPunct, p) {
		return c.l.errf("expected %q", p)
	}
	return nil
}
func (c *lc) done() bool { _, ok := c.peek(); return !ok }

func expectInt(c *lc) (int, error) {
	n, err := expectInt64(c)
	return int(n), err
}

func expectInt64(c *lc) (int64, error) {
	tk, ok := c.next()
	if !ok || tk.kind != tInt {
		return 0, c.l.errf("expected integer literal")
	}
	return strconv.ParseInt(tk.text, 10, 64)
}