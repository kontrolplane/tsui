package tsui

import (
	"strings"
)

// SubjectMatches reports whether subject matches the pattern using NATS wildcard rules:
// `*` matches a single token and `>` matches one or more trailing tokens.
func SubjectMatches(pattern, subject string) bool {
	if pattern == "" || subject == "" {
		return false
	}
	pt := strings.Split(pattern, ".")
	st := strings.Split(subject, ".")
	for i, p := range pt {
		if p == ">" {
			return i == len(pt)-1 && len(st) > i
		}
		if i >= len(st) {
			return false
		}
		if p != "*" && p != st[i] {
			return false
		}
	}
	return len(pt) == len(st)
}

// SubjectsOverlap reports whether some subject matches both a and b, either of which may contain wildcards.
func SubjectsOverlap(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	at := strings.Split(a, ".")
	bt := strings.Split(b, ".")
	for i := 0; i < len(at) && i < len(bt); i++ {
		if at[i] == ">" || bt[i] == ">" {
			return true
		}
		if at[i] != "*" && bt[i] != "*" && at[i] != bt[i] {
			return false
		}
	}
	return len(at) == len(bt)
}

// HasWildcard reports whether subject contains a `*` or `>` token.
func HasWildcard(subject string) bool {
	for _, t := range strings.Split(subject, ".") {
		if t == "*" || t == ">" {
			return true
		}
	}
	return false
}

// ValidPublishSubject reports whether subject can be published to: non-empty tokens, no wildcards, no whitespace.
func ValidPublishSubject(subject string) bool {
	if subject == "" || strings.ContainsAny(subject, " \t\r\n") || HasWildcard(subject) {
		return false
	}
	for _, t := range strings.Split(subject, ".") {
		if t == "" {
			return false
		}
	}
	return true
}

// SubjectPrefix returns the literal part of a subject filter, used to prefill the publish form.
// "orders.>" becomes "orders." and "orders.eu" stays "orders.eu".
func SubjectPrefix(filter string) string {
	tokens := strings.Split(filter, ".")
	for i, t := range tokens {
		if t == "*" || t == ">" {
			if i == 0 {
				return ""
			}
			return strings.Join(tokens[:i], ".") + "."
		}
	}
	return filter
}

// ValidFilterSubject reports whether subject is a subject a stream can be filtered by: non-empty
// tokens, no whitespace, and `>` only as the last token.
func ValidFilterSubject(subject string) bool {
	if subject == "" || strings.ContainsAny(subject, " \t\r\n") {
		return false
	}
	tokens := strings.Split(subject, ".")
	for i, t := range tokens {
		if t == "" || (t == ">" && i != len(tokens)-1) {
			return false
		}
	}
	return true
}
