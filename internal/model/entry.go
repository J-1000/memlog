package model

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/oklog/ulid/v2"
)

const (
	OpAdd       = "add"
	OpSupersede = "supersede"
	OpRetract   = "retract"
)

type Entry struct {
	ID      string   `json:"id"`
	TS      string   `json:"ts"`
	Op      string   `json:"op"`
	Fact    string   `json:"fact"`
	Tags    []string `json:"tags"`
	Subject string   `json:"subject"`
	Session string   `json:"session"`
	Agent   string   `json:"agent"`
	Source  string   `json:"source"`
	Ref     *string  `json:"ref"`
}

var tagRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

func ValidTag(s string) bool { return tagRE.MatchString(s) }

func NewID(t time.Time, entropy *ulid.MonotonicEntropy) string {
	return ulid.MustNew(ulid.Timestamp(t), entropy).String()
}

func NormalizeTags(tags []string) []string {
	out := slices.Clone(tags)
	slices.Sort(out)
	return slices.Compact(out)
}

func Validate(e Entry) error {
	if _, err := ulid.ParseStrict(e.ID); err != nil {
		return fmt.Errorf("id must be a ULID")
	}
	ts, err := time.Parse(time.RFC3339, e.TS)
	if err != nil || e.TS != ts.UTC().Format(time.RFC3339) {
		return fmt.Errorf("ts must be RFC3339 UTC with seconds precision")
	}
	switch e.Op {
	case OpAdd, OpSupersede:
		if strings.TrimSpace(e.Fact) == "" {
			return fmt.Errorf("fact is required")
		}
	case OpRetract:
		if e.Fact != "" {
			return fmt.Errorf("fact must be empty for retract")
		}
	default:
		return fmt.Errorf("op must be add, supersede, or retract")
	}
	if utf8.RuneCountInString(e.Fact) > 2000 {
		return fmt.Errorf("fact must be at most 2000 characters")
	}
	if hasControlOrNewline(e.Fact) {
		return fmt.Errorf("fact must be a single line without control characters")
	}
	if len(e.Tags) > 10 {
		return fmt.Errorf("tags must contain at most 10 items")
	}
	if !slices.IsSorted(e.Tags) || len(e.Tags) != len(NormalizeTags(e.Tags)) {
		return fmt.Errorf("tags must be sorted and deduplicated")
	}
	for _, tag := range e.Tags {
		if !ValidTag(tag) {
			return fmt.Errorf("invalid tag %q", tag)
		}
	}
	if e.Subject != "" && !ValidTag(e.Subject) {
		return fmt.Errorf("invalid subject %q", e.Subject)
	}
	if e.Session == "" || len(e.Session) > 128 || !printableASCII(e.Session) {
		return fmt.Errorf("session must be 1-128 printable ASCII characters")
	}
	if len(e.Agent) > 64 || !printableASCII(e.Agent) {
		return fmt.Errorf("agent must be 0-64 printable ASCII characters")
	}
	if utf8.RuneCountInString(e.Source) > 500 || hasControlOrNewline(e.Source) {
		return fmt.Errorf("source must be at most 500 single-line characters")
	}
	if e.Op == OpAdd && e.Ref != nil {
		return fmt.Errorf("ref must be null for add")
	}
	if (e.Op == OpSupersede || e.Op == OpRetract) && (e.Ref == nil || *e.Ref == "") {
		return fmt.Errorf("ref is required for %s", e.Op)
	}
	if e.Ref != nil {
		if _, err := ulid.ParseStrict(*e.Ref); err != nil {
			return fmt.Errorf("ref must be a ULID")
		}
	}
	return nil
}

func hasControlOrNewline(s string) bool {
	for _, r := range s {
		if r == '\n' || r == '\r' || unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func printableASCII(s string) bool {
	for _, r := range s {
		if r < 32 || r > 126 {
			return false
		}
	}
	return true
}
