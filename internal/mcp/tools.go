package mcp

type toolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func toolDefs() []toolDef {
	str := map[string]any{"type": "string"}
	tags := map[string]any{"type": "array", "items": str}
	schema := func(required []string, props map[string]any) map[string]any {
		return map[string]any{
			"type":                 "object",
			"properties":           props,
			"required":             required,
			"additionalProperties": false,
		}
	}
	return []toolDef{
		{
			Name:        "memlog_add",
			Description: "Record a new immutable fact in the memlog store. Returns the new entry's ULID.",
			InputSchema: schema([]string{"fact", "session"}, map[string]any{
				"fact": str, "session": str, "tags": tags, "subject": str, "agent": str, "source": str,
			}),
		},
		{
			Name:        "memlog_search",
			Description: "Case-insensitive substring search over live facts. Returns a JSON array of raw entries; [] when nothing matches.",
			InputSchema: schema([]string{"query"}, map[string]any{
				"query": str, "tag": str, "subject": str,
				"all": map[string]any{"type": "boolean", "description": "include superseded and retracted versions"},
			}),
		},
		{
			Name:        "memlog_show",
			Description: "Show one logical fact's full version chain, oldest first, as a JSON array of raw entries.",
			InputSchema: schema([]string{"ref"}, map[string]any{
				"ref": map[string]any{"type": "string", "description": "full ULID or unambiguous prefix of at least 8 characters"},
			}),
		},
		{
			Name:        "memlog_supersede",
			Description: "Replace the live fact REF with a new version. Returns the new entry's ULID.",
			InputSchema: schema([]string{"ref", "fact", "session"}, map[string]any{
				"ref": str, "fact": str, "session": str, "tags": tags, "subject": str, "agent": str, "source": str,
				"inherit": map[string]any{"type": "boolean", "description": "copy tags and subject from REF when not given"},
			}),
		},
		{
			Name:        "memlog_retract",
			Description: "Mark the live fact REF as no longer true. Returns the new entry's ULID.",
			InputSchema: schema([]string{"ref", "session"}, map[string]any{
				"ref": str, "session": str, "source": str,
			}),
		},
	}
}
