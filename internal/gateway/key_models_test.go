package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/yetone/magpie/internal/access"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/settings"
)

// modelsUpstream is a chat upstream plan's and fast's models share,
// counting the calls it is sent and answering streamed when the request
// asks to stream — as the vendors behind the Anthropic and Gemini shapes
// do — beside the routing groups the whitelist has to be judged over:
// Allpass of models both in one, Partly with one out, Nested with
// Allpass and one out, Nestedall of Allpass alone, and Ateffort at an
// effort of its own.
func modelsUpstream(t *testing.T) *atomic.Int64 {
	t.Helper()
	var calls atomic.Int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		b, _ := io.ReadAll(r.Body)
		if bytes.Contains(b, []byte(`"stream":true`)) {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, sse(
				`data: {"id":"c","object":"chat.completion.chunk","model":"m1","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":null}]}`,
				`data: {"id":"c","object":"chat.completion.chunk","model":"m1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
				`data: [DONE]`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":5}}`)
	}))
	t.Cleanup(up.Close)
	for _, p := range []provider.Provider{
		{ID: "plan", Name: "Plan", Key: "k", Chat: up.URL + "/v1", Models: []string{"m1", "m2"}},
		{ID: "fast", Name: "Fast", Key: "k", Chat: up.URL + "/v1", Models: []string{"f1"}},
	} {
		if err := provider.Save(p); err != nil {
			t.Fatal(err)
		}
	}
	for _, g := range []provider.Group{
		{Name: "Allpass", Members: []string{"plan/m1", "fast/f1"}},
		{Name: "Partly", Members: []string{"plan/m1", "plan/m2"}},
		{Name: "Nested", Members: []string{"group/allpass", "plan/m2"}},
		{Name: "Nestedall", Members: []string{"group/allpass"}},
		{Name: "Ateffort", Members: []string{"plan/m1:low"}},
	} {
		if err := provider.SaveGroup(g); err != nil {
			t.Fatal(err)
		}
	}
	return &calls
}

// setModels holds a key to a subset of the catalog (#882).
func setModels(t *testing.T, id string, models ...string) {
	t.Helper()
	if _, err := access.Update("models-key", access.Change{Key: id, Models: models}); err != nil {
		t.Fatal(err)
	}
}

// A gateway key held to a subset of the catalog (#882) is refused, with a
// 403 that names what it takes, before any provider is asked — however
// the model is named: bare, prefixed, at an effort of its own, with
// Claude Code's [1m] mark, in another case, or a routing group, nested or
// not, which passes only when every member is in the subset. A key held
// to nothing, a keyless request from this computer and the model among
// those the key takes all go on as they always did, and a model magpie
// knows none of is still a 404.
func TestKeyModelsRefusesBeforeUpstream(t *testing.T) {
	fresh(t)
	calls := modelsUpstream(t)
	keys, secrets := newCaller(t, "Intern", "Admin")
	setModels(t, keys[0].ID, "plan/m1", "fast/*")
	h := New().Handler()
	call := func(secret, model string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"`+model+`","messages":[{"role":"user","content":"hi"}]}`))
		if secret != "" {
			r.Header.Set("Authorization", "Bearer "+secret)
		} else {
			r.RemoteAddr = "127.0.0.1:5000"
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, m := range []string{"plan/m1", "m1", "Plan/M1", "plan/m1:high", "plan/m1[1m]",
		"group/allpass", "allpass", "group/nestedall", "group/ateffort"} {
		if w := call(secrets[0], m); w.Code != 200 {
			t.Errorf("%s: refused or failed: %d %s", m, w.Code, w.Body.String())
		}
	}
	letIn := calls.Load()
	for _, m := range []string{"plan/m2", "m2", "Plan/M2", "plan/m2:low", "plan/m2[1m]", "group/partly", "group/nested"} {
		w := call(secrets[0], m)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s: %d %s", m, w.Code, w.Body.String())
			continue
		}
		var e struct {
			Error struct{ Message, Type string }
		}
		if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
			t.Fatalf("%s: %v", m, err)
		}
		if e.Error.Type != "permission_error" || !strings.Contains(e.Error.Message, `"Intern"`) ||
			!strings.Contains(e.Error.Message, "takes only plan/m1, fast/*") || !strings.Contains(e.Error.Message, "is not one of them") {
			t.Errorf("%s: refusal says %s", m, w.Body.String())
		}
	}
	if calls.Load() != letIn {
		t.Fatal("a refused model reached the provider", calls.Load(), letIn)
	}
	// the refusal is in the log as the failure it was, named as the
	// gateway's own
	rec := lastUsage(t)
	if !rec.Rejected || rec.ErrType != "gateway_key_models" || rec.Status != 403 || rec.CallerKeyID != keys[0].ID || rec.Requested != "group/nested" {
		t.Fatalf("refusal recorded as %+v", rec)
	}
	// another key, a keyless request from this computer, and the whitelist
	// taken off all serve the model outside it, as before
	for _, secret := range []string{secrets[1], ""} {
		if w := call(secret, "plan/m2"); w.Code != 200 {
			t.Errorf("unrestricted %q: %d %s", secret, w.Code, w.Body.String())
		}
	}
	setModels(t, keys[0].ID)
	if w := call(secrets[0], "plan/m2"); w.Code != 200 {
		t.Fatalf("an emptied whitelist still refused: %d %s", w.Code, w.Body.String())
	}
	// a model magpie knows none of is a 404, whitelist or not
	setModels(t, keys[0].ID, "plan/m1")
	if w := call(secrets[0], "nope/none"); w.Code != 404 {
		t.Fatalf("unknown model under a whitelist: %d %s", w.Code, w.Body.String())
	}
}

// The refusal is said in each API's own error shape (#882): OpenAI's and
// Anthropic's permission_error, Gemini's PERMISSION_DENIED with the model
// in its URL — and each API's token counting is refused the same way,
// before anything is counted.
func TestKeyModelsAcrossProtocolsAndCounting(t *testing.T) {
	fresh(t)
	modelsUpstream(t)
	keys, secrets := newCaller(t, "Intern")
	setModels(t, keys[0].ID, "plan/m1")
	h := New().Handler()
	call := func(path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+secrets[0])
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	// Anthropic
	w := call("/v1/messages", `{"model":"plan/m2","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`)
	var a struct {
		Type  string
		Error struct{ Type, Message string }
	}
	if w.Code != 403 || json.Unmarshal(w.Body.Bytes(), &a) != nil || a.Type != "error" || a.Error.Type != "permission_error" || !strings.Contains(a.Error.Message, `"Intern"`) {
		t.Fatalf("anthropic refusal: %d %s", w.Code, w.Body.String())
	}
	if w = call("/v1/messages", `{"model":"plan/m1","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`); w.Code != 200 {
		t.Fatalf("anthropic allowed: %d %s", w.Code, w.Body.String())
	}
	// Gemini: the model is in the URL
	w = call("/v1beta/models/plan/m2:generateContent", `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)
	var g struct {
		Error struct {
			Code    int
			Message string
			Status  string
		}
	}
	if w.Code != 403 || json.Unmarshal(w.Body.Bytes(), &g) != nil || g.Error.Code != 403 || g.Error.Status != "PERMISSION_DENIED" || !strings.Contains(g.Error.Message, "plan/m1") {
		t.Fatalf("gemini refusal: %d %s", w.Code, w.Body.String())
	}
	if w = call("/v1beta/models/plan/m1:generateContent", `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`); w.Code != 200 {
		t.Fatalf("gemini allowed: %d %s", w.Code, w.Body.String())
	}
	// counting tokens, Anthropic's shape and Gemini's, a group's too: one
	// whose members aren't all in the subset is refused, one whose are is
	// counted
	for _, c := range []struct{ path, body string }{
		{"/v1/messages/count_tokens", `{"model":"plan/m2","messages":[{"role":"user","content":"hi"}]}`},
		{"/v1/messages/count_tokens", `{"model":"group/partly","messages":[{"role":"user","content":"hi"}]}`},
		{"/v1beta/models/plan/m2:countTokens", `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`},
	} {
		w = call(c.path, c.body)
		if w.Code != 403 || !strings.Contains(w.Body.String(), "permission") && !strings.Contains(w.Body.String(), "PERMISSION_DENIED") {
			t.Fatalf("counting refused: %s %d %s", c.path, w.Code, w.Body.String())
		}
	}
	for _, c := range []struct{ path, body, got string }{
		{"/v1/messages/count_tokens", `{"model":"plan/m1","messages":[{"role":"user","content":"hi"}]}`, "input_tokens"},
		{"/v1/messages/count_tokens", `{"model":"group/ateffort","messages":[{"role":"user","content":"hi"}]}`, "input_tokens"},
		{"/v1beta/models/plan/m1:countTokens", `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`, "totalTokens"},
	} {
		if w = call(c.path, c.body); w.Code != 200 || !strings.Contains(w.Body.String(), c.got) {
			t.Fatalf("counting allowed: %s %d %s", c.path, w.Code, w.Body.String())
		}
	}
}

// Every model list a key is handed is its own (#882): /v1/models,
// Gemini's and Muse's lists show only the models in the key's subset —
// a group only when every member is — the one model's page is a 404
// outside it, and Codex's list keeps the account's own models while
// magpie's added ones are the subset's. A keyless request and a key held
// to nothing see the whole catalog, as before.
func TestKeyModelsListsAndDetails(t *testing.T) {
	fresh(t)
	modelsUpstream(t)
	keys, secrets := newCaller(t, "Intern", "Admin")
	setModels(t, keys[0].ID, "plan/m1", "fast/*")
	h := New().Handler()
	get := func(path, secret string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		if secret != "" {
			r.Header.Set("Authorization", "Bearer "+secret)
		} else {
			r.RemoteAddr = "127.0.0.1:5000"
		}
		if strings.Contains(path, "/muse-code/") {
			r.Header.Set("User-Agent", "muse-build/1.4.2 (non-interactive; macos-aarch64; build 0)")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	check := func(w *httptest.ResponseRecorder, want, keepOut []string) {
		t.Helper()
		if w.Code != 200 {
			t.Fatalf("list: %d %s", w.Code, w.Body.String())
		}
		body := w.Body.String()
		for _, id := range want {
			if !strings.Contains(body, `"`+id+`"`) {
				t.Errorf("list without %s: %s", id, body)
			}
		}
		for _, id := range keepOut {
			if strings.Contains(body, `"`+id+`"`) {
				t.Errorf("list with %s: %s", id, body)
			}
		}
	}
	want := []string{"plan/m1", "fast/f1", "group/allpass", "group/nestedall", "group/ateffort"}
	keepOut := []string{"plan/m2", "group/partly", "group/nested"}
	check(get("/v1/models", secrets[0]), want, keepOut)
	gemini := func(ids []string) []string {
		out := make([]string, len(ids))
		for i, id := range ids {
			out[i] = "models/" + id // Gemini's list names them so
		}
		return out
	}
	check(get("/v1beta/models", secrets[0]), gemini(want), gemini(keepOut))
	check(get("/muse-code/models", secrets[0]), want, keepOut)
	// the model's own page: inside, and a 404 that says no more of the
	// one outside it than of a model magpie knows none of
	if w := get("/v1/models/plan/m1", secrets[0]); w.Code != 200 {
		t.Fatalf("detail inside: %d %s", w.Code, w.Body.String())
	}
	for _, id := range []string{"plan/m2", "group/partly"} {
		if w := get("/v1/models/"+id, secrets[0]); w.Code != 404 || !strings.Contains(w.Body.String(), "unknown model") {
			t.Fatalf("detail of %s: %d %s", id, w.Code, w.Body.String())
		}
	}
	// a group whose every member is in the subset has its page
	if w := get("/v1/models/group/allpass", secrets[0]); w.Code != 200 {
		t.Fatalf("group detail inside: %d %s", w.Code, w.Body.String())
	}
	// a keyless request and a key held to nothing see everything
	for _, secret := range []string{secrets[1], ""} {
		check(get("/v1/models", secret), append(want, "plan/m2", "group/partly"), nil)
	}
}

// Codex's model list keeps the ChatGPT account's own models, which the
// backend answers, and narrows only the ones magpie adds to the key's
// subset (#882).
func TestKeyModelsCodexList(t *testing.T) {
	fresh(t)
	codexSignedIn(t)
	if err := provider.Save(provider.Provider{ID: "codex", Models: []string{"gpt-5.5", "gpt-5-codex"}}); err != nil {
		t.Fatal(err)
	}
	if err := provider.Save(provider.Provider{ID: "relay", Name: "Relay", Key: "k", Anthropic: "http://127.0.0.1:1",
		Models: []string{"claude-opus-5.5", "claude-sonnet-4-5"}}); err != nil {
		t.Fatal(err)
	}
	// a routing group of two of relay's models, one entry of the appended
	// ones, judged by its members
	if err := provider.SaveGroup(provider.Group{Name: "Picks", Members: []string{"relay/claude-opus-5.5", "relay/claude-sonnet-4-5"}}); err != nil {
		t.Fatal(err)
	}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		io.WriteString(w, `{"models":[{"slug":"gpt-5.5","priority":1},{"slug":"gpt-5-codex","priority":2}]}`)
	}))
	t.Cleanup(up.Close)
	was := provider.CodexBase
	provider.CodexBase = up.URL + "/backend-api/codex"
	t.Cleanup(func() { provider.CodexBase = was })
	keys, secrets := newCaller(t, "Intern")
	slugs := func(secret string) []string {
		r := httptest.NewRequest("GET", CodexPath+"/models", nil)
		if secret != "" {
			r.Header.Set("Authorization", "Bearer "+secret)
		} else {
			r.RemoteAddr = "127.0.0.1:5000" // this computer asks without a key
		}
		w := httptest.NewRecorder()
		New().Handler().ServeHTTP(w, r)
		var got struct {
			Models []map[string]any `json:"models"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, m := range got.Models {
			out = append(out, m["slug"].(string))
		}
		return out
	}
	if got := slugs(""); !slices.Equal(got, []string{"gpt-5.5", "gpt-5-codex", "group/picks", "relay/claude-opus-5.5", "relay/claude-sonnet-4-5"}) {
		t.Fatalf("unrestricted list %v", got)
	}
	setModels(t, keys[0].ID, "relay/claude-opus-5.5")
	// the group is dropped — one of its members is outside the subset —
	// while the model in it stays, and the account's own models keep
	// their places
	if got := slugs(secrets[0]); !slices.Equal(got, []string{"gpt-5.5", "gpt-5-codex", "relay/claude-opus-5.5"}) {
		t.Fatalf("restricted list %v", got)
	}
	// the group comes back when both its members are in the subset
	setModels(t, keys[0].ID, "relay/*")
	if got := slugs(secrets[0]); !slices.Equal(got, []string{"gpt-5.5", "gpt-5-codex", "group/picks", "relay/claude-opus-5.5", "relay/claude-sonnet-4-5"}) {
		t.Fatalf("group back on the list %v", got)
	}
}

// The model a stand-in sends the request to is the one the whitelist
// judges (#882): Claude Code naming a model magpie serves none of is
// refused when its tier's model is outside the key's subset, and served
// when it is in it.
func TestKeyModelsStandInTarget(t *testing.T) {
	fresh(t)
	modelsUpstream(t)
	keys, secrets := newCaller(t, "Narrow", "Wide")
	setModels(t, keys[0].ID, "plan/m1")
	setModels(t, keys[1].ID, "plan/m2")
	StandIn = func(agent, model string) string {
		if model == "claude-haiku-4-5-20251001" {
			return "plan/m2"
		}
		return ""
	}
	t.Cleanup(func() { StandIn = nil })
	h := New().Handler()
	call := func(secret string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"claude-haiku-4-5-20251001","messages":[{"role":"user","content":"hi"}]}`))
		r.Header.Set("Authorization", "Bearer "+secret)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := call(secrets[0]); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "takes only plan/m1") {
		t.Fatalf("stand-in target outside: %d %s", w.Code, w.Body.String())
	}
	if w := call(secrets[1]); w.Code != 200 {
		t.Fatalf("stand-in target inside: %d %s", w.Code, w.Body.String())
	}
}

// The model endpoints beside chat (#882) — embeddings and rerank, images
// (the Settings drawer included) and videos — are refused for a model
// outside the key's subset before any provider is asked, and take the
// ones in it; System One's decider is refused the same way.
func TestKeyModelsModelEndpointsRefuseBeforeUpstream(t *testing.T) {
	fresh(t)
	calls := modelsUpstream(t)
	var drew atomic.Int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		drew.Add(1)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"created":1,"data":[{"b64_json":"eA=="}],"usage":{"input_tokens":7,"output_tokens":100}}`)
	}))
	t.Cleanup(up.Close)
	if err := provider.Save(provider.Provider{ID: "art", Name: "Art", Key: "k", Chat: up.URL + "/v1", Models: []string{"gpt-image-1"}}); err != nil {
		t.Fatal(err)
	}
	if err := provider.Save(provider.Provider{ID: "jev", Name: "Jev", Key: "k", Decide: up.URL + "/v1"}); err != nil {
		t.Fatal(err)
	}
	// the drawer a request that names no model draws with
	s := settings.Load()
	s.ImageGen = "plan/m2"
	if err := settings.Save(s); err != nil {
		t.Fatal(err)
	}
	keys, secrets := newCaller(t, "Intern")
	setModels(t, keys[0].ID, "plan/m1")
	h := New().Handler()
	call := func(path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+secrets[0])
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	refused := func(path, body string) {
		t.Helper()
		if w := call(path, body); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "permission_error") || !strings.Contains(w.Body.String(), "Intern") {
			t.Errorf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	chat, drawn := calls.Load(), drew.Load()
	// embeddings, rerank, images and videos, each naming a model outside
	refused("/v1/embeddings", `{"model":"plan/m2","input":"hi"}`)
	refused("/v1/rerank", `{"model":"plan/m2","query":"hi","documents":["a"]}`)
	refused("/v1/images/generations", `{"model":"art/gpt-image-1","prompt":"a bird"}`)
	refused("/v1/videos", `{"model":"plan/m2","prompt":"a bird"}`)
	// the default drawer is outside the subset too
	refused("/v1/images/generations", `{"prompt":"a bird"}`)
	// and so is System One's decider
	refused("/v1/systemone", `{"model":"jev/jev-latest","state":{"message":"hi"},"questions":{"intent":{"type":"choice"}}}`)
	if calls.Load() != chat || drew.Load() != drawn {
		t.Fatal("a refused endpoint reached a provider", calls.Load(), drew.Load())
	}
	// the embeddings of a model in the subset go through as they did
	if w := call("/v1/embeddings", `{"model":"plan/m1","input":"hi"}`); w.Code != 200 {
		t.Errorf("embeddings allowed: %d %s", w.Code, w.Body.String())
	}
	// so do the image named and the decider once they are in it
	setModels(t, keys[0].ID, "plan/m1", "art/gpt-image-1", "jev/jev-latest")
	if w := call("/v1/images/generations", `{"model":"art/gpt-image-1","prompt":"a bird"}`); w.Code != 200 {
		t.Errorf("images allowed: %d %s", w.Code, w.Body.String())
	}
	if w := call("/v1/systemone", `{"model":"jev/jev-latest","state":{"message":"hi"},"questions":{"intent":{"type":"choice"}}}`); w.Code != 200 {
		t.Errorf("system one allowed: %d %s", w.Code, w.Body.String())
	}
	if drew.Load() == drawn {
		t.Fatal("an allowed endpoint reached no provider")
	}
}

// The drawers appended to /v1/models for a magpie that asks for them
// (#882) are the calling key's own too: only those in its subset are
// appended. (The videomakers appended beside them go by the same
// shownObjects, and need a Grok subscription to list.)
func TestKeyModelsAppendedDrawers(t *testing.T) {
	fresh(t)
	modelsUpstream(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{}`)
	}))
	t.Cleanup(up.Close)
	if err := provider.Save(provider.Provider{ID: "art", Name: "Art", Key: "k", Chat: up.URL + "/v1", Models: []string{"gpt-image-1", "flux-pro"}}); err != nil {
		t.Fatal(err)
	}
	keys, secrets := newCaller(t, "Intern")
	setModels(t, keys[0].ID, "art/flux-pro")
	h := New().Handler()
	list := func(secret string) string {
		r := httptest.NewRequest("GET", "/v1/models", nil)
		if secret != "" {
			r.Header.Set("Authorization", "Bearer "+secret)
		} else {
			r.RemoteAddr = "127.0.0.1:5000" // this computer asks without a key
		}
		r.Header.Set(provider.DrawersHeader, "1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Body.String()
	}
	if got := list(secrets[0]); !strings.Contains(got, `"art/flux-pro"`) || strings.Contains(got, `"art/gpt-image-1"`) {
		t.Fatalf("the key's drawers: %s", got)
	}
	if got := list(""); !strings.Contains(got, `"kind":"image"`) || !strings.Contains(got, `"art/gpt-image-1"`) {
		t.Fatalf("a keyless request's drawers: %s", got)
	}
}

// A group magpie found, while such groups are off, stands a request in for
// the model of its first provider (#882): the whitelist judges that
// stand-in's target.
func TestKeyModelsAutoStandInTarget(t *testing.T) {
	fresh(t)
	modelsUpstream(t)
	if err := provider.SetAutoGroups(false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { provider.SetAutoGroups(true) })
	keys, secrets := newCaller(t, "Intern")
	h := New().Handler()
	call := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"group/auto-m1","messages":[{"role":"user","content":"hi"}]}`))
		r.Header.Set("Authorization", "Bearer "+secrets[0])
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	setModels(t, keys[0].ID, "plan/m1")
	if w := call(); w.Code != 200 {
		t.Fatalf("the stand-in's target in the subset: %d %s", w.Code, w.Body.String())
	}
	setModels(t, keys[0].ID, "fast/*")
	if w := call(); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "takes only fast/*") {
		t.Fatalf("the stand-in's target outside: %d %s", w.Code, w.Body.String())
	}
}
