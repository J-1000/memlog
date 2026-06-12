package render

import (
	"bytes"
	"fmt"
	"html"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/J-1000/memlog/internal/model"
	"github.com/J-1000/memlog/internal/store"
)

func Memory(st store.State) []byte {
	var b bytes.Buffer
	live := st.LiveHeads()
	newest := "never"
	if len(st.Entries) > 0 {
		newest = st.Entries[len(st.Entries)-1].TS
	}
	retracted := len(st.Retracted)
	fmt.Fprintf(&b, "# Memory\n\n_Last updated: %s · %d live facts · %d retracted · store %s_\n\n", newest, len(live), retracted, st.Meta.StoreID)
	sections := map[string][]model.Entry{}
	var subjects []string
	for _, e := range live {
		key := e.Subject
		sections[key] = append(sections[key], e)
	}
	for subject := range sections {
		if subject != "" {
			subjects = append(subjects, subject)
		}
	}
	slices.Sort(subjects)
	if _, ok := sections[""]; ok {
		subjects = append(subjects, "")
	}
	for _, subject := range subjects {
		title := subject
		if title == "" {
			title = "(no subject)"
		}
		fmt.Fprintf(&b, "## %s\n\n", title)
		items := sections[subject]
		sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
		for _, e := range items {
			fmt.Fprintf(&b, "- %s", e.Fact)
			if len(e.Tags) > 0 {
				b.WriteByte(' ')
				for i, tag := range e.Tags {
					if i > 0 {
						b.WriteByte(' ')
					}
					fmt.Fprintf(&b, "`#%s`", tag)
				}
			}
			fmt.Fprintf(&b, " ⟨%s · session %s⟩\n", e.ID[:8], e.Session)
		}
		b.WriteByte('\n')
	}
	b.WriteString("## Provenance\n\n")
	b.WriteString("| id | learned | session | agent | source | history |\n")
	b.WriteString("|---|---|---|---|---|---|\n")
	sort.Slice(live, func(i, j int) bool { return live[i].ID < live[j].ID })
	for _, e := range live {
		chain := st.Chain(e.ID)
		learned := dateOnly(chain[0].TS)
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | v%d |\n",
			e.ID[:8],
			learned,
			escapeCell(e.Session),
			escapeCell(e.Agent),
			escapeCell(e.Source),
			len(chain),
		)
	}
	return b.Bytes()
}

func dateOnly(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts[:10]
	}
	return t.Format(time.DateOnly)
}

func escapeCell(s string) string {
	s = html.EscapeString(s)
	return strings.ReplaceAll(s, "|", `\|`)
}
