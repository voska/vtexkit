package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voska/vtexkit/cli/config"
)

// sandbox points HOME at a temp dir so config writes never touch the real
// ~/.config. This is the pattern frescatto's cmd tests already use.
func sandbox(t *testing.T, name string) config.Store {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	return config.Store{Name: name}
}

func TestPathsAreNamespacedPerStore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fr := config.Store{Name: "frescatto"}
	zs := config.Store{Name: "zonasul"}

	if fr.Dir() == zs.Dir() {
		t.Fatal("two stores must never share a config dir")
	}
	if !strings.HasSuffix(fr.Dir(), filepath.Join(".config", "frescatto")) {
		t.Errorf("frescatto dir = %s", fr.Dir())
	}
	// zonasul v0.5.0 ships ~/.config/zonasul — migrating must not move it.
	if !strings.HasSuffix(zs.Dir(), filepath.Join(".config", "zonasul")) {
		t.Errorf("zonasul dir = %s; v0.5.0 compatibility requires ~/.config/zonasul", zs.Dir())
	}
	for _, p := range []string{fr.Path(), fr.CredentialsPath(), fr.ListsPath(), fr.PendingAuthPath()} {
		if !strings.HasPrefix(p, fr.Dir()+string(filepath.Separator)) {
			t.Errorf("%s escapes the store config dir", p)
		}
	}
}

func TestFileNamesMatchPublishedLayout(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := config.Store{Name: "zonasul"}
	want := map[string]string{
		s.Path():            "config.json",
		s.CredentialsPath(): "credentials.json",
		s.ListsPath():       "lists.json",
		s.PendingAuthPath(): "pending_auth.json",
	}
	for path, base := range want {
		if filepath.Base(path) != base {
			t.Errorf("got %s, want basename %s", path, base)
		}
	}
}

func TestSaveCreatesDir0700AndFile0600(t *testing.T) {
	s := sandbox(t, "testkit")
	if err := s.Save(&config.Config{CEP: "01310100", Number: "50"}); err != nil {
		t.Fatal(err)
	}
	assertMode(t, s.Dir(), 0o700)
	assertMode(t, s.Path(), 0o600)
}

func TestConfigRoundTrip(t *testing.T) {
	s := sandbox(t, "testkit")
	want := &config.Config{
		CEP: "01310100", Street: "Rua X", Number: "50",
		Neighborhood: "Bela Vista", City: "São Paulo", State: "SP",
		OrderFormID: "abc123",
	}
	if err := s.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if *got != *want {
		t.Errorf("round trip lost data:\n got %+v\nwant %+v", *got, *want)
	}
}

func TestLoadMissingReturnsZeroValueNotError(t *testing.T) {
	s := sandbox(t, "testkit")
	got, err := s.Load()
	if err != nil {
		t.Fatalf("a missing config must not be an error: %v", err)
	}
	if got.CEP != "" || got.OrderFormID != "" {
		t.Errorf("want zero config, got %+v", *got)
	}
}

func TestCredentialsRoundTrip(t *testing.T) {
	s := sandbox(t, "testkit")
	if err := s.SaveCredentials(&config.Credentials{Email: "a@b.c"}); err != nil {
		t.Fatal(err)
	}
	assertMode(t, s.CredentialsPath(), 0o600)
	got, err := s.LoadCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if got.Email != "a@b.c" {
		t.Errorf("email = %q", got.Email)
	}
}

func TestListsRoundTrip(t *testing.T) {
	s := sandbox(t, "testkit")
	l := config.Lists{}
	if !l.Add("weekly", "134") {
		t.Error("first Add must report true")
	}
	if l.Add("weekly", "134") {
		t.Error("duplicate Add must report false")
	}
	l.Add("weekly", "62")
	if err := s.SaveLists(l); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadLists()
	if err != nil {
		t.Fatal(err)
	}
	if len(got["weekly"]) != 2 {
		t.Fatalf("round trip lost data: %v", got)
	}
	if !got.Remove("weekly", "134") {
		t.Error("Remove of a present SKU must report true")
	}
	if got.Remove("weekly", "999") {
		t.Error("Remove of an absent SKU must report false")
	}
	if len(got["weekly"]) != 1 || got["weekly"][0] != "62" {
		t.Errorf("after remove = %v, want [62]", got["weekly"])
	}
}

func TestLoadListsMissingReturnsEmptyMap(t *testing.T) {
	s := sandbox(t, "testkit")
	got, err := s.LoadLists()
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("must return an empty map, not nil — callers Add to it directly")
	}
	if !got.Add("new", "1") {
		t.Error("the returned map must be usable")
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s mode = %o, want %o", path, got, want)
	}
}
