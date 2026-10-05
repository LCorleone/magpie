package gui

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/settings"
)

// A routing group's editor reads each member off the served catalog entry:
// whether agents are told it takes images, whether anything said so either
// way, the levels it reasons at and the window it holds. The first is the
// same expression the gateway decides by when it describes an image for a
// model that can't see one (blindTo reads Entry.Images), so the editor cannot
// say a member sees images that the gateway would describe them for.
func TestGroupModelsCarryWhatTheyTake(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	yes, no := true, false
	if err := provider.Save(provider.Provider{ID: "p", Name: "P", Key: "k", Chat: "http://127.0.0.1:1/v1",
		Models: []string{"eye", "text", "mystery", "mine"}}); err != nil {
		t.Fatal(err)
	}
	// what each model's own list says: one sees, one takes none, one says
	// nothing at all
	if err := catalog.SaveLive("p", "http://127.0.0.1:1/v1", []catalog.Model{
		{ID: "eye", Context: 1048576, Efforts: []string{"low", "medium", "high"}, Images: true, ImageInput: &yes},
		{ID: "text", Context: 200000, ImageInput: &no},
		{ID: "mystery", Context: 128000},
		{ID: "mine", Context: 64000, ImageInput: &no},
	}); err != nil {
		t.Fatal(err)
	}
	// the user's own answer for one its list said took none
	st := settings.Load()
	st.ModelImages = map[string]bool{"p/mine": true}
	if err := settings.Save(st); err != nil {
		t.Fatal(err)
	}
	if err := provider.SaveGroup(provider.Group{ID: "g", Name: "G", Members: []string{"p/eye", "p/text", "p/mystery", "p/mine"}}); err != nil {
		t.Fatal(err)
	}

	byID := map[string]modelRef{}
	for _, m := range groupsState().Models {
		byID[m.ID] = m
	}
	for _, tc := range []struct {
		id      string
		images  bool
		unknown bool
		context int
		efforts int
	}{
		{"p/eye", true, false, 1048576, 3},    // its list says it sees
		{"p/text", false, false, 200000, 0},   // its list says it takes none
		{"p/mystery", false, true, 128000, 0}, // nothing was read of it either way
		{"p/mine", true, false, 64000, 0},     // the user's answer, over its list
	} {
		m, ok := byID[tc.id]
		if !ok {
			t.Fatalf("%s is not in the group's models", tc.id)
		}
		if m.Images != tc.images || m.ImagesUnknown != tc.unknown {
			t.Errorf("%s: images %v unknown %v, want %v %v", tc.id, m.Images, m.ImagesUnknown, tc.images, tc.unknown)
		}
		if m.Context != tc.context {
			t.Errorf("%s: context %d, want %d", tc.id, m.Context, tc.context)
		}
		if len(m.Efforts) != tc.efforts {
			t.Errorf("%s: efforts %v, want %d of them", tc.id, m.Efforts, tc.efforts)
		}
	}

	// and the group's own members say the same of them
	var g groupJSON
	for _, x := range groupsState().Groups {
		if x.ID == "g" {
			g = x
		}
	}
	if len(g.Info) != 4 {
		t.Fatalf("the group lists %d members", len(g.Info))
	}
	for i, want := range []struct {
		id      string
		images  bool
		unknown bool
	}{{"p/eye", true, false}, {"p/text", false, false}, {"p/mystery", false, true}, {"p/mine", true, false}} {
		if g.Info[i].ID != want.id || g.Info[i].Images != want.images || g.Info[i].ImagesUnknown != want.unknown {
			t.Errorf("member %d is %s images %v unknown %v, want %s %v %v", i, g.Info[i].ID, g.Info[i].Images, g.Info[i].ImagesUnknown, want.id, want.images, want.unknown)
		}
	}
}

// The page reads a member's images off the wire: a model magpie has an answer
// for carries no imagesUnknown key at all (and `images` is omitempty, so a
// model that takes none carries no images key either), while one nothing was
// read of carries imagesUnknown: true. What the browser test's fixture writes
// is this shape, not one of its own.
func TestGroupModelImagesOnTheWire(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	yes, no := true, false
	if err := provider.Save(provider.Provider{ID: "p", Name: "P", Key: "k", Chat: "http://127.0.0.1:1/v1",
		Models: []string{"eye", "text", "mystery"}}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.SaveLive("p", "http://127.0.0.1:1/v1", []catalog.Model{
		{ID: "eye", Images: true, ImageInput: &yes},
		{ID: "text", ImageInput: &no},
		{ID: "mystery"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := provider.SaveGroup(provider.Group{ID: "g", Name: "G", Members: []string{"p/eye", "p/text", "p/mystery"}}); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(groupsState())
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Models []map[string]any `json:"models"`
		Groups []struct {
			Info []map[string]any `json:"memberInfo"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	keys := func(m map[string]any) (images, unknown any) { return m["images"], m["imagesUnknown"] }
	byID := map[string]map[string]any{}
	for _, m := range doc.Models {
		byID[m["id"].(string)] = m
	}
	for _, tc := range []struct {
		id      string
		images  bool
		unknown bool
	}{
		{"p/eye", true, false},
		{"p/text", false, false},
		{"p/mystery", false, true},
	} {
		m, ok := byID[tc.id]
		if !ok {
			t.Fatalf("%s is not in the models", tc.id)
		}
		img, unk := keys(m)
		if img != nil && img != tc.images || (img == nil) == tc.images {
			t.Errorf("%s: images on the wire is %v, want %v (a false is left out)", tc.id, img, tc.images)
		}
		if unk != nil && unk != tc.unknown || (unk == nil) == tc.unknown {
			t.Errorf("%s: imagesUnknown on the wire is %v, want %v (only true is sent)", tc.id, unk, tc.unknown)
		}
	}
	// the same for a group's own members
	for _, m := range doc.Groups[0].Info {
		want := byID[m["id"].(string)]
		img, unk := keys(m)
		wantImg, wantUnk := keys(want)
		if (img == nil) != (wantImg == nil) || img != wantImg || (unk == nil) != (wantUnk == nil) || unk != wantUnk {
			t.Errorf("member %v carries images %v unknown %v, the model carries %v %v", m["id"], img, unk, wantImg, wantUnk)
		}
	}
}
