package gateway

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/yetone/magpie/internal/access"
	"github.com/yetone/magpie/internal/provider"
)

// A gateway key held to a subset of the catalog (#882): one it is held to
// nothing of is every key there was before, taking every model as keys
// always did; one held to some takes a model when it is among them — by
// its catalog id "provider/model", or all of a provider's by
// "provider/*" — and a routing group when every member is, its members
// as deep as they nest. The check is made on the catalog id a request
// resolves to, so a bare name, a prefixed one, a stand-in or a group the
// gateway rewrote all meet it as the model they became.

// inModels is whether id, a catalog id "provider/model", is among the
// models of a key's whitelist: one named for it — case-insensitively, as
// agents spell a model either way — or all of a provider's by its
// "provider/*" wildcard.
func inModels(models []string, id string) bool {
	for _, m := range models {
		if strings.EqualFold(m, id) {
			return true
		}
		// a model id may have slashes of its own, so only the first ends
		// the provider's id (see settings.CheckModelKey)
		if w, ok := strings.CutSuffix(m, "/*"); ok && w != "" {
			if pid, _, found := strings.Cut(id, "/"); found && strings.EqualFold(pid, w) {
				return true
			}
		}
	}
	return false
}

// modelAllowed is whether who may serve the model a request resolved to
// (#882): the catalog id of p's model when ms is empty, else the group
// those members are of, which passes only when every one of them does.
func modelAllowed(who access.Identity, id string, ms []provider.Member) bool {
	if len(who.Models) == 0 {
		return true
	}
	if len(ms) == 0 {
		return inModels(who.Models, id)
	}
	for _, m := range ms {
		if !inModels(who.Models, m.Provider.ID+"/"+m.Model) {
			return false
		}
	}
	return true
}

// groupMembersOf is the members of id when it names a routing group —
// resolved and ready, as FindGroup has them — nil when it names none.
func groupMembersOf(id string) []provider.Member {
	if _, ms, ok := provider.FindGroup(id); ok {
		return ms
	}
	return nil
}

// refusedModels says why a key held to a subset of models (#882) was
// turned away from the one it asked for: the key, the models it takes,
// and the model that isn't among them, in the voice of the key-limit
// refusals beside it.
func refusedModels(who access.Identity, asked string) string {
	return fmt.Sprintf("magpie gateway key %q takes only %s; %q is not one of them. Use one of those models, or ask for the key's models to be widened",
		who.KeyName, strings.Join(who.Models, ", "), asked)
}

// modelRefused answers the 403 a key held to a subset of models (#882)
// gets for the model id asks for, resolved as serve resolves it — a bare
// name or a group's first, a group expanded to its members — and says
// whether it was: proto is the API the error is answered on. Nothing is
// said, and false returned, for a key held to no subset.
func modelRefused(w http.ResponseWriter, r *http.Request, proto provider.Protocol, id string) bool {
	who := access.Caller(r.Context())
	if len(who.Models) == 0 {
		return false
	}
	if gid, ok := provider.GroupFor(id); ok {
		id = gid
	}
	p, model, ok := provider.Resolve(id)
	if !ok {
		return false
	}
	return refusedAs(w, proto, who, id, p, model)
}

// resolvedRefused is modelRefused for an endpoint that resolved its model
// itself (#882) — the images and videos APIs, whose fallback (Settings'
// drawer or videomaker) Resolve alone may not name: p's model as its
// catalog id, id's group expanded to its members.
func resolvedRefused(w http.ResponseWriter, r *http.Request, proto provider.Protocol, id string, p provider.Provider, model string) bool {
	who := access.Caller(r.Context())
	if len(who.Models) == 0 {
		return false
	}
	return refusedAs(w, proto, who, id, p, model)
}

// refusedAs writes the 403 of a key held to a subset of models (#882) for
// the model p and model resolve to, and says whether it did.
func refusedAs(w http.ResponseWriter, proto provider.Protocol, who access.Identity, id string, p provider.Provider, model string) bool {
	if modelAllowed(who, p.ID+"/"+model, groupMembersOf(id)) {
		return false
	}
	writeError(w, proto, http.StatusForbidden, refusedModels(who, id))
	return true
}

// catalogShown is the catalog as the calling key may list it (#882): a
// key held to a subset of models is shown only those — a group when
// every member is in it — and a key held to none everything.
func catalogShown(who access.Identity, shown []provider.Entry) []provider.Entry {
	if len(who.Models) == 0 {
		return shown
	}
	find := provider.GroupFinder()
	out := make([]provider.Entry, 0, len(shown))
	for _, e := range shown {
		if _, ms, isGroup := find(e.ID); isGroup && len(ms) > 0 {
			if modelAllowed(who, e.ID, ms) {
				out = append(out, e)
			}
			continue
		}
		if inModels(who.Models, e.ID) {
			out = append(out, e)
		}
	}
	return out
}

// shownObjects is the appended drawer and videomaker objects as the
// calling key may list them (#882): a key held to a subset of models is
// shown only those in it.
func shownObjects(who access.Identity, objs []map[string]any) []map[string]any {
	if len(who.Models) == 0 {
		return objs
	}
	return slices.DeleteFunc(objs, func(m map[string]any) bool {
		id, _ := m["id"].(string)
		return !inModels(who.Models, id)
	})
}
