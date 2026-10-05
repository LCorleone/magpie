package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/access"
	"github.com/yetone/magpie/internal/settings"
)

func TestGatewayKeyCLI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := settings.Save(settings.Settings{LAN: true}); err != nil {
		t.Fatal(err)
	}
	call := func(args ...string) string {
		t.Helper()
		var out bytes.Buffer
		if err := gatewayKeysTo(&out, append([]string{"gateway-key"}, args...)); err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(out.String())
	}
	if got := call("list"); !strings.Contains(got, "GATEWAY KEY") {
		t.Fatal(got)
	}
	secret := call("add", "Remote laptop")
	keys, _ := access.List()
	if len(keys) != 1 {
		t.Fatal(keys)
	}
	id := keys[0].ID
	if who, ok := access.Authenticate(secret); !ok || who.KeyID != id || who.KeyName != "Remote laptop" {
		t.Fatal(who, ok)
	}
	if got := call(); strings.Contains(got, secret) || !strings.Contains(got, id) || !strings.Contains(got, "Remote laptop") {
		t.Fatal("list must mask credentials", got)
	}
	next := call("rotate", id)
	if next == secret {
		t.Fatal("rotation reused the secret")
	}
	if _, ok := access.Authenticate(secret); ok {
		t.Fatal("old credential works after rotation")
	}
	if who, ok := access.Authenticate(next); !ok || who.KeyID != id {
		t.Fatal("rotation changed identity", who, ok)
	}
	call("remove", id)
	if _, ok := access.Authenticate(next); ok {
		t.Fatal("removed credential still works")
	}
	for _, args := range [][]string{{"add"}, {"add", ""}, {"list", "extra"}, {"rotate", "missing"}, {"remove", "missing"}, {"unknown", "x"}} {
		if err := gatewayKeysTo(&bytes.Buffer{}, append([]string{"gateway-key"}, args...)); err == nil {
			t.Error("accepted", args)
		}
	}
}

func TestGatewayKeyListWithReadOnlyMigration(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s := settings.Settings{LAN: true, LANKey: "sk-magpie-fixture-cli"}
	if err := settings.Save(s); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(settings.Path(), 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(settings.Path(), 0o600) })
	if err := settings.Save(s); err == nil {
		t.Skip("settings.json remains writable")
	}
	var out bytes.Buffer
	if err := gatewayKeysTo(&out, []string{"gateway-key", "list"}); err != nil || !strings.Contains(out.String(), "Magpie") {
		t.Fatal("read-only migration blocked CLI list", out.String(), err)
	}
}

// magpie gateway-key limit sets, shows and takes off a key's limit, and
// list says what each key has used of its own (#585).
func TestGatewayKeyLimitCLI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := settings.Save(settings.Settings{LAN: true}); err != nil {
		t.Fatal(err)
	}
	call := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := gatewayKeysTo(&out, append([]string{"gateway-key"}, args...))
		return out.String(), err
	}
	if _, err := call("add", "Phone"); err != nil {
		t.Fatal(err)
	}
	keys, _ := access.List()
	id := keys[0].ID
	if got, err := call("limit", id); err != nil || !strings.Contains(got, "no limit") {
		t.Fatal(got, err)
	}
	got, err := call("limit", id, "week", "--tokens", "2m", "--cost=5", "--cache-reads")
	if err != nil {
		t.Fatal(err)
	}
	keys, _ = access.List()
	if l := keys[0].Limit; l == nil || l.Period != "week" || l.Tokens != 2_000_000 || l.Cost != 5 || !l.CacheReads {
		t.Fatalf("limit kept as %+v", keys[0].Limit)
	}
	for _, want := range []string{"0 used of 2000000, 2000000 left", "cache reads", "$0.00 used of $5.00", "State", "open"} {
		if !strings.Contains(got, want) {
			t.Fatalf("limit shows %q, without %q", got, want)
		}
	}
	if got, _ := call("list"); !strings.Contains(got, "LIMIT") || !strings.Contains(got, "0/2000000 tokens $0.00/$5.00 per week") {
		t.Fatal(got)
	}
	for _, args := range [][]string{{"limit"}, {"limit", id, "hour", "--tokens", "5"}, {"limit", id, "day"}, {"limit", id, "day", "--tokens", "lots"},
		{"limit", id, "day", "--cost"}, {"limit", id, "day", "--what"}, {"limit", "missing", "day", "--tokens", "5"}, {"limit", id, "off", "extra"}} {
		if _, err := call(args...); err == nil {
			t.Error("accepted", args)
		}
	}
	if got, err := call("limit", id, "off"); err != nil || !strings.Contains(got, "no limit") {
		t.Fatal(got, err)
	}
	if keys, _ = access.List(); keys[0].Limit != nil {
		t.Fatal("off kept", keys[0].Limit)
	}
}

// magpie gateway-key models sets, shows and takes off a key's model
// whitelist, and refuses what can't be one (#882).
func TestGatewayKeyModelsCLI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := settings.Save(settings.Settings{LAN: true}); err != nil {
		t.Fatal(err)
	}
	call := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := gatewayKeysTo(&out, append([]string{"gateway-key"}, args...))
		return out.String(), err
	}
	if _, err := call("add", "Intern"); err != nil {
		t.Fatal(err)
	}
	keys, _ := access.List()
	id := keys[0].ID
	if got, err := call("models", id); err != nil || !strings.Contains(got, "every model") {
		t.Fatal(got, err)
	}
	got, err := call("models", id, "openai/gpt-5-mini", " deepseek/* ")
	if err != nil {
		t.Fatal(err)
	}
	keys, _ = access.List()
	if m := keys[0].Models; len(m) != 2 || m[0] != "openai/gpt-5-mini" || m[1] != "deepseek/*" {
		t.Fatalf("models kept as %v", m)
	}
	for _, want := range []string{"openai/gpt-5-mini", "deepseek/*"} {
		if !strings.Contains(got, want) {
			t.Fatalf("models show %q, without %q", got, want)
		}
	}
	// what can't be a model is refused, by the shape every model key has
	if _, err := call("models", id, "gpt-5-mini"); err == nil || !strings.Contains(err.Error(), "<provider>/<model>") {
		t.Fatal("accepted a bare model", err)
	}
	if _, err := call("models", id, "openai/"); err == nil {
		t.Fatal("accepted a wildcard without its provider's models")
	}
	for _, args := range [][]string{{"models"}, {"models", id, "off", "extra"}, {"models", "missing", "a/m"}} {
		if _, err := call(args...); err == nil {
			t.Error("accepted", args)
		}
	}
	if got, err := call("models", id, "off"); err != nil || !strings.Contains(got, "every model") {
		t.Fatal(got, err)
	}
	if keys, _ = access.List(); keys[0].Models != nil {
		t.Fatal("off kept models", keys[0].Models)
	}
}
