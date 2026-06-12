package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func validEntry() Entry {
	return Entry{
		ID:      "01J9XK7M3QJ8Z6W4V2T1R0PQNM",
		TS:      "2026-06-12T14:03:22Z",
		Op:      OpAdd,
		Fact:    "The staging database password rotates every 30 days.",
		Tags:    []string{"infra", "staging"},
		Subject: "staging-db",
		Session: "claude-code-2026-06-12-a",
		Agent:   "claude-code",
		Source:  "user told me directly",
		Ref:     nil,
	}
}

func TestValidate(t *testing.T) {
	ref := "01J9XK7M3QJ8Z6W4V2T1R0PQNM"
	cases := map[string]func(Entry) Entry{
		"bad id":            func(e Entry) Entry { e.ID = "x"; return e },
		"bad timestamp":     func(e Entry) Entry { e.TS = "2026-06-12T14:03:22+01:00"; return e },
		"bad op":            func(e Entry) Entry { e.Op = "edit"; return e },
		"missing fact":      func(e Entry) Entry { e.Fact = ""; return e },
		"retract with fact": func(e Entry) Entry { e.Op = OpRetract; e.Fact = "x"; e.Ref = &ref; return e },
		"fact too long":     func(e Entry) Entry { e.Fact = string(make([]byte, 2001)); return e },
		"fact newline":      func(e Entry) Entry { e.Fact = "one\ntwo"; return e },
		"too many tags": func(e Entry) Entry {
			e.Tags = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"}
			return e
		},
		"bad tag":            func(e Entry) Entry { e.Tags = []string{"Bad"}; return e },
		"unsorted tags":      func(e Entry) Entry { e.Tags = []string{"staging", "infra"}; return e },
		"duplicate tags":     func(e Entry) Entry { e.Tags = []string{"infra", "infra"}; return e },
		"bad subject":        func(e Entry) Entry { e.Subject = "bad_subject"; return e },
		"missing session":    func(e Entry) Entry { e.Session = ""; return e },
		"bad session":        func(e Entry) Entry { e.Session = "bad\nsession"; return e },
		"agent too long":     func(e Entry) Entry { e.Agent = string(make([]byte, 65)); return e },
		"source multiline":   func(e Entry) Entry { e.Source = "one\ntwo"; return e },
		"add with ref":       func(e Entry) Entry { e.Ref = &ref; return e },
		"supersede no ref":   func(e Entry) Entry { e.Op = OpSupersede; return e },
		"supersede bad ref":  func(e Entry) Entry { bad := "bad"; e.Op = OpSupersede; e.Ref = &bad; return e },
		"valid supersede":    func(e Entry) Entry { e.Op = OpSupersede; e.Ref = &ref; return e },
		"valid empty fields": func(e Entry) Entry { e.Tags = nil; e.Subject = ""; e.Agent = ""; e.Source = ""; return e },
	}
	require.NoError(t, Validate(validEntry()))
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			err := Validate(mutate(validEntry()))
			if name == "valid supersede" || name == "valid empty fields" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
