package access

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// setModels is what the CLI and the GUI send to hold a key to a subset of
// the catalog (#882).
func setModels(t *testing.T, id string, models ...string) {
	t.Helper()
	if _, err := Update("models-key", Change{Key: id, Models: models}); err != nil {
		t.Fatal(err)
	}
}

// A key held to a subset of the catalog (#882): what "models-key" writes
// is kept trimmed, once each and in order, checked by the one rule every
// model key is held to; what can't be a model is refused, and nothing
// (or "off" upstream of here) is every model, as keys always were.
func TestModelsKeyHoldsAndValidates(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	secret, err := Update("add-key", Change{Name: "Intern"})
	if err != nil {
		t.Fatal(err)
	}
	keys, _ := List()
	id := keys[0].ID
	for _, c := range []struct {
		name   string
		in     []string
		want   []string
		badErr bool
	}{
		{"an empty list is every model", nil, nil, false},
		{"so is one of blanks", []string{"  ", ""}, nil, false},
		{"a model as <provider>/<model>", []string{"openai/gpt-5-mini"}, []string{"openai/gpt-5-mini"}, false},
		{"all of a provider", []string{"deepseek/*"}, []string{"deepseek/*"}, false},
		{"a model id may have slashes of its own", []string{"relay/deepseek/v4.1"}, []string{"relay/deepseek/v4.1"}, false},
		{"trimmed, once each, in order", []string{"b/m", " a/m ", "a/m", "b/m"}, []string{"b/m", "a/m"}, false},
		{"a bare model is no provider", []string{"gpt-5-mini"}, nil, true},
		{"a provider of capitals is no id", []string{"OpenAI/gpt-5-mini"}, nil, true},
		{"a wildcard needs its model", []string{"openai/"}, nil, true},
		{"a bare wildcard names no provider", []string{"*"}, nil, true},
		{"a routing group is no model key", []string{"group/cheap"}, nil, true},
	} {
		_, err := Update("models-key", Change{Key: id, Models: c.in})
		if c.badErr {
			if err == nil {
				t.Errorf("%s: accepted %v", c.name, c.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		keys, err := List()
		if err != nil {
			t.Fatal(err)
		}
		if got := keys[0].Models; !slices.Equal(got, c.want) {
			t.Errorf("%s: kept %v, want %v", c.name, got, c.want)
		}
		if who, ok := Authenticate(secret); !ok || !slices.Equal(who.Models, c.want) {
			t.Errorf("%s: authenticated as %+v", c.name, who)
		}
	}
	// taken off again
	setModels(t, id, "openai/gpt-5-mini")
	setModels(t, id)
	keys, _ = List()
	if keys[0].Models != nil {
		t.Fatal("an empty list kept models", keys[0].Models)
	}
	// a key that isn't there isn't found
	if _, err := Update("models-key", Change{Key: "missing", Models: []string{"a/m"}}); err == nil {
		t.Fatal("set models on a missing key")
	}
	// and a group points at its members rather than kept as a dead entry
	if _, err := Update("models-key", Change{Key: id, Models: []string{"group/cheap"}}); err == nil ||
		!strings.Contains(err.Error(), "not a routing group") || !strings.Contains(err.Error(), "use its members") {
		t.Fatal("kept a routing group as a model key", err)
	}
}

// The store keeps the whitelist under "models" and drops it when empty,
// so a caller-keys.json written before the field (#882) reads as it
// always did, and one written since loads its models back.
func TestModelsJSONBackAndForth(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	secret, err := Update("add-key", Change{Name: "Laptop"})
	if err != nil {
		t.Fatal(err)
	}
	keys, _ := List()
	id := keys[0].ID
	setModels(t, id, "openai/gpt-5-mini", "deepseek/*")
	b, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	var raw []map[string]any
	if err := json.Unmarshal(b, &raw); err != nil || len(raw) != 1 {
		t.Fatal(err, len(raw))
	}
	if got, ok := raw[0]["models"].([]any); !ok || len(got) != 2 || got[0] != "openai/gpt-5-mini" || got[1] != "deepseek/*" {
		t.Fatalf("models kept as %v", raw[0]["models"])
	}
	// a store written before the field: nothing of it is there, and a key
	// from it takes every model, as before
	old := `[{"id":"old","name":"Old","secret":"` + secret + `"}]`
	if err := os.WriteFile(filepath.Join(dir, "magpie", "caller-keys.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	keys, err = List()
	if err != nil || len(keys) != 1 || keys[0].Models != nil {
		t.Fatal("an old store's key grew models", keys, err)
	}
	if who, ok := Authenticate(secret); !ok || who.Models != nil || who.KeyID != "old" {
		t.Fatalf("an old store's key authenticated as %+v", who)
	}
	// the field comes back off a save that kept none
	setModels(t, "old")
	b, _ = os.ReadFile(Path())
	raw = nil
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	if _, has := raw[0]["models"]; has {
		t.Fatal("an empty whitelist was written as a field", string(b))
	}
}
