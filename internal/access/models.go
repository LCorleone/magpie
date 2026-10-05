package access

import (
	"fmt"
	"slices"
	"strings"

	"github.com/yetone/magpie/internal/settings"
)

// ValidModels is a key's model whitelist as it is kept (#882): the
// "provider/model" ids it may serve — or "provider/*" for all of a
// provider's — trimmed, empty ones dropped and repeats kept once, in the
// order given; nil for none, which is every model, as keys always were.
// What can't be a model key is an error, by the one rule every such key
// is held to (settings.CheckModelKey), so what a key is limited to and
// what the pickers accept come out of the same shape. A routing group
// ("group/<id>", as provider.GroupPrefix spells it) is refused rather
// than kept as a dead entry: a group is never a model key, and the
// whitelist judges a group by its members anyway.
func ValidModels(models []string) ([]string, error) {
	var out []string
	for _, m := range models {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		if err := settings.CheckModelKey("a key's model", m); err != nil {
			return nil, err
		}
		if strings.HasPrefix(m, "group/") {
			return nil, fmt.Errorf("a key's model names the model itself, not a routing group: use its members, such as openai/gpt-5-mini or openai/*, not %q", m)
		}
		if !slices.Contains(out, m) {
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}
