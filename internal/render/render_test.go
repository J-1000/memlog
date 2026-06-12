package render

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/J-1000/memlog/internal/model"
	"github.com/J-1000/memlog/internal/store"
	"github.com/stretchr/testify/require"
)

func TestGoldenRender(t *testing.T) {
	for _, name := range []string{"empty", "subjects", "supersede", "retract"} {
		t.Run(name, func(t *testing.T) {
			state := fixtureState(t, name)
			got := Memory(state)
			want, err := os.ReadFile(filepath.Join("..", "..", "testdata", name+".golden.md"))
			require.NoError(t, err)
			require.Equal(t, string(want), string(got))
		})
	}
}

func TestContextDigest(t *testing.T) {
	state := fixtureState(t, "subjects")
	out, dropped := Context(state, "", 0)
	require.Zero(t, dropped)
	require.Equal(t, "# Memory\n\n## alpha\n\n- Alpha fact. #infra\n\n## (no subject)\n\n- No subject fact. #misc\n", string(out))
	out, dropped = Context(state, "alpha", 0)
	require.Zero(t, dropped)
	require.Equal(t, "# Memory\n\n## alpha\n\n- Alpha fact. #infra\n", string(out))
	out, dropped = Context(state, "", 45)
	require.Equal(t, 1, dropped)
	require.Equal(t, "# Memory\n\n## alpha\n\n- Alpha fact. #infra\n", string(out))
	out, dropped = Context(state, "", 10)
	require.Equal(t, 2, dropped)
	require.Equal(t, "# Memory\n", string(out))
}

func fixtureState(t *testing.T, name string) store.State {
	t.Helper()
	st := store.State{
		ByID:       map[string]model.Entry{},
		ReplacedBy: map[string]string{},
		Retracted:  map[string]string{},
		Parent:     map[string]string{},
		Meta:       store.Meta{Version: 1, CreatedAt: "2026-06-12T10:00:00Z", StoreID: "01J9XK7M3QJ8Z6W4V2T1R0PQNM"},
	}
	add := func(e model.Entry) {
		require.NoError(t, st.Accept(e))
	}
	id1 := "01J9XK7M3QJ8Z6W4V2T1R0PQNA"
	id2 := "01J9XK7M3QJ8Z6W4V2T1R0PQNB"
	id3 := "01J9XK7M3QJ8Z6W4V2T1R0PQNC"
	switch name {
	case "empty":
	case "subjects":
		add(model.Entry{ID: id2, TS: "2026-06-12T11:00:00Z", Op: model.OpAdd, Fact: "No subject fact.", Tags: []string{"misc"}, Session: "s2", Agent: "agent", Source: "source"})
		add(model.Entry{ID: id1, TS: "2026-06-12T10:00:00Z", Op: model.OpAdd, Fact: "Alpha fact.", Tags: []string{"infra"}, Subject: "alpha", Session: "s1", Agent: "agent", Source: "source"})
	case "supersede":
		add(model.Entry{ID: id1, TS: "2026-06-12T10:00:00Z", Op: model.OpAdd, Fact: "Old fact.", Tags: []string{"infra"}, Subject: "alpha", Session: "s1"})
		add(model.Entry{ID: id2, TS: "2026-06-12T11:00:00Z", Op: model.OpSupersede, Fact: "New fact.", Tags: []string{"ops"}, Subject: "beta", Session: "s2", Ref: &id1})
	case "retract":
		add(model.Entry{ID: id1, TS: "2026-06-12T10:00:00Z", Op: model.OpAdd, Fact: "Gone fact.", Tags: []string{"infra"}, Subject: "alpha", Session: "s1"})
		add(model.Entry{ID: id3, TS: "2026-06-12T12:00:00Z", Op: model.OpRetract, Session: "s3", Ref: &id1})
	}
	return st
}
