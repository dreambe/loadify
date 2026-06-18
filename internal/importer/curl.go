package importer

import (
	"fmt"
	"strings"
)

// parseCurl parses a single `curl` command line. It understands -X/--request,
// -H/--header, -d/--data/--data-raw/--data-binary, and the URL (quoted or
// bare). Line continuations (\) are folded first.
func parseCurl(s string) ([]req, error) {
	s = strings.ReplaceAll(s, "\\\n", " ")
	toks, err := tokenize(s)
	if err != nil {
		return nil, err
	}
	r := req{Headers: map[string]string{}}
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		next := func() string {
			if i+1 < len(toks) {
				i++
				return toks[i]
			}
			return ""
		}
		switch {
		case t == "curl":
		case t == "-X" || t == "--request":
			r.Method = next()
		case t == "-H" || t == "--header":
			h := next()
			k, v, ok := strings.Cut(h, ":")
			if !ok {
				return nil, fmt.Errorf("importer: malformed -H header %q (expected \"Name: value\")", h)
			}
			r.Headers[strings.TrimSpace(k)] = strings.TrimSpace(v)
		case t == "-d" || t == "--data" || t == "--data-raw" || t == "--data-binary" || t == "--data-ascii":
			r.Body = next()
			if r.Method == "" {
				r.Method = "POST"
			}
		case t == "--url":
			r.URL = next()
		case t == "-A" || t == "--user-agent":
			r.Headers["User-Agent"] = next()
		case t == "-b" || t == "--cookie":
			r.Headers["Cookie"] = next()
		case strings.HasPrefix(t, "-"):
			// Skip unknown flags; consume a value if the flag clearly takes one.
			if !strings.HasPrefix(t, "--") && len(t) == 2 && strings.ContainsAny(t, "ouT") {
				next()
			}
		default:
			if r.URL == "" && (strings.HasPrefix(t, "http://") || strings.HasPrefix(t, "https://")) {
				r.URL = t
			}
		}
	}
	if r.URL == "" {
		return nil, fmt.Errorf("importer: no URL found in curl command")
	}
	return []req{r}, nil
}

// tokenize splits a shell-ish command honoring single and double quotes.
func tokenize(s string) ([]string, error) {
	var toks []string
	var cur strings.Builder
	var quote rune
	inTok := false
	flush := func() {
		if inTok {
			toks = append(toks, cur.String())
			cur.Reset()
			inTok = false
		}
	}
	for _, c := range s {
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				cur.WriteRune(c)
			}
		case c == '\'' || c == '"':
			quote = c
			inTok = true
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			flush()
		default:
			cur.WriteRune(c)
			inTok = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("importer: unterminated quote in curl command")
	}
	flush()
	return toks, nil
}
