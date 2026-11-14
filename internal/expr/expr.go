// Package expr evaluates `when:` conditions.
//
//	exists('data/seed.sql')
//	branch matches '^agent/'
//	profile == 'agent' && !exists('.env')
//
// Operands are quoted strings, variable names or exists(path). Operators
// are ==, !=, matches (regular expression), !, && and ||, with parentheses.
package expr

import (
	"fmt"
	"regexp"
	"strings"
)

// Env is what an expression can see.
type Env struct {
	Vars map[string]string
	// Exists reports whether a path exists, relative to the worktree.
	Exists func(path string) bool
}

// Eval evaluates src. An empty expression is true.
func Eval(src string, env Env) (bool, error) {
	if strings.TrimSpace(src) == "" {
		return true, nil
	}
	toks, err := lex(src)
	if err != nil {
		return false, fmt.Errorf("when %q: %w", src, err)
	}
	p := &parser{toks: toks, env: env}
	v, err := p.or()
	if err == nil && p.pos < len(p.toks) {
		err = fmt.Errorf("unexpected %q", p.toks[p.pos].text)
	}
	if err != nil {
		return false, fmt.Errorf("when %q: %w", src, err)
	}
	return v, nil
}

type kind int

const (
	tStr kind = iota
	tIdent
	tOp
)

type token struct {
	kind kind
	text string
}

func lex(s string) ([]token, error) {
	var toks []token
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n':
			i++
		case c == '\'' || c == '"':
			j := strings.IndexByte(s[i+1:], c)
			if j < 0 {
				return nil, fmt.Errorf("unterminated string")
			}
			toks = append(toks, token{tStr, s[i+1 : i+1+j]})
			i += j + 2
		case isIdent(c):
			j := i
			for j < len(s) && isIdent(s[j]) {
				j++
			}
			toks = append(toks, token{tIdent, s[i:j]})
			i = j
		case strings.HasPrefix(s[i:], "==") || strings.HasPrefix(s[i:], "!=") ||
			strings.HasPrefix(s[i:], "&&") || strings.HasPrefix(s[i:], "||"):
			toks = append(toks, token{tOp, s[i : i+2]})
			i += 2
		case c == '!' || c == '(' || c == ')':
			toks = append(toks, token{tOp, string(c)})
			i++
		default:
			return nil, fmt.Errorf("unexpected character %q", c)
		}
	}
	return toks, nil
}

func isIdent(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

type parser struct {
	toks []token
	pos  int
	env  Env
}

// accept consumes the next token if it is the operator or keyword text.
func (p *parser) accept(text string) bool {
	if p.pos < len(p.toks) && p.toks[p.pos].kind != tStr && p.toks[p.pos].text == text {
		p.pos++
		return true
	}
	return false
}

func (p *parser) or() (bool, error) {
	v, err := p.and()
	for err == nil && p.accept("||") {
		var r bool
		r, err = p.and()
		v = v || r
	}
	return v, err
}

func (p *parser) and() (bool, error) {
	v, err := p.unary()
	for err == nil && p.accept("&&") {
		var r bool
		r, err = p.unary()
		v = v && r
	}
	return v, err
}

func (p *parser) unary() (bool, error) {
	if p.accept("!") {
		v, err := p.unary()
		return !v, err
	}
	if p.accept("(") {
		v, err := p.or()
		if err == nil && !p.accept(")") {
			err = fmt.Errorf("missing )")
		}
		return v, err
	}
	return p.comparison()
}

func (p *parser) comparison() (bool, error) {
	left, err := p.operand()
	if err != nil {
		return false, err
	}
	for _, op := range []string{"==", "!=", "matches"} {
		if !p.accept(op) {
			continue
		}
		right, err := p.operand()
		if err != nil {
			return false, err
		}
		switch op {
		case "==":
			return left == right, nil
		case "!=":
			return left != right, nil
		default:
			re, err := regexp.Compile(right)
			if err != nil {
				return false, err
			}
			return re.MatchString(left), nil
		}
	}
	return left != "" && left != "false", nil
}

// operand returns a string; exists() yields "true" or "false".
func (p *parser) operand() (string, error) {
	if p.pos >= len(p.toks) {
		return "", fmt.Errorf("unexpected end")
	}
	t := p.toks[p.pos]
	p.pos++
	switch t.kind {
	case tStr:
		return t.text, nil
	case tIdent:
		if p.accept("(") {
			if t.text != "exists" {
				return "", fmt.Errorf("unknown function %q", t.text)
			}
			arg, err := p.operand()
			if err != nil {
				return "", err
			}
			if !p.accept(")") {
				return "", fmt.Errorf("missing )")
			}
			if p.env.Exists != nil && p.env.Exists(arg) {
				return "true", nil
			}
			return "false", nil
		}
		v, ok := p.env.Vars[t.text]
		if !ok {
			return "", fmt.Errorf("unknown variable %q", t.text)
		}
		return v, nil
	}
	return "", fmt.Errorf("unexpected %q", t.text)
}
