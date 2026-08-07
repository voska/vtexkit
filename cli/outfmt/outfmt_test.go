package outfmt_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/voska/vtexkit/cli/outfmt"
)

type row struct {
	SKU   string `json:"sku"`
	Name  string `json:"name"`
	Price int    `json:"price"`
}

var sample = []row{
	{"134", "Kit Peixe Fresco", 15372},
	{"62", "Porção de Salmão 200g", 3990},
}

func TestJSONMode(t *testing.T) {
	var buf bytes.Buffer
	f := outfmt.New(outfmt.Options{JSON: true}, &buf)
	if err := f.Print(map[string]string{"name": "Banana"}); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "{\"name\":\"Banana\"}\n" {
		t.Errorf("json = %q", buf.String())
	}
}

func TestHumanMode(t *testing.T) {
	var buf bytes.Buffer
	f := outfmt.New(outfmt.Options{}, &buf)
	if err := f.Print("hello"); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "hello\n" {
		t.Errorf("human = %q", buf.String())
	}
}

func TestSelectProjectsFields(t *testing.T) {
	var buf bytes.Buffer
	f := outfmt.New(outfmt.Options{JSON: true, Select: []string{"sku", "price"}}, &buf)
	if err := f.Print(sample); err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v (%s)", err, buf.String())
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2", len(got))
	}
	if _, ok := got[0]["name"]; ok {
		t.Error("name must be projected away")
	}
	if got[0]["sku"] != "134" {
		t.Errorf("sku = %v, want 134", got[0]["sku"])
	}
	if got[0]["price"] != float64(15372) {
		t.Errorf("price = %v, want 15372", got[0]["price"])
	}
}

func TestSelectSupportsDotPaths(t *testing.T) {
	var buf bytes.Buffer
	f := outfmt.New(outfmt.Options{JSON: true, Select: []string{"seller.id"}}, &buf)
	err := f.Print([]map[string]any{
		{"sku": "134", "seller": map[string]any{"id": "1", "name": "Jahu"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	seller, ok := got[0]["seller"].(map[string]any)
	if !ok {
		t.Fatalf("seller missing or wrong type: %#v", got[0])
	}
	if seller["id"] != "1" {
		t.Errorf("seller.id = %v, want 1", seller["id"])
	}
	if _, ok := seller["name"]; ok {
		t.Error("seller.name must be projected away")
	}
}

func TestSelectIgnoresMissingFields(t *testing.T) {
	var buf bytes.Buffer
	f := outfmt.New(outfmt.Options{JSON: true, Select: []string{"sku", "nonexistent"}}, &buf)
	if err := f.Print(sample); err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	// An agent guessing a field name should get the fields that do exist,
	// not an error and not a null-filled row.
	if len(got[0]) != 1 || got[0]["sku"] != "134" {
		t.Errorf("row = %#v, want only sku", got[0])
	}
}

func TestQuietEmitsBareValues(t *testing.T) {
	var buf bytes.Buffer
	f := outfmt.New(outfmt.Options{Quiet: true, Select: []string{"sku"}}, &buf)
	if err := f.Print(sample); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(buf.String()); got != "134\n62" {
		t.Errorf("quiet = %q, want \"134\\n62\"", got)
	}
}

func TestResultsOnlyStripsEnvelope(t *testing.T) {
	var buf bytes.Buffer
	f := outfmt.New(outfmt.Options{JSON: true, ResultsOnly: true}, &buf)
	err := f.Print(map[string]any{
		"total": 2,
		"items": sample,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("results-only must emit the bare array, got %s", buf.String())
	}
	if len(got) != 2 {
		t.Errorf("got %d rows, want 2", len(got))
	}
}

func TestPlainEmitsTabSeparated(t *testing.T) {
	var buf bytes.Buffer
	f := outfmt.New(outfmt.Options{Plain: true, Select: []string{"sku", "name"}}, &buf)
	if err := f.Print(sample); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %q", len(lines), buf.String())
	}
	if lines[0] != "134\tKit Peixe Fresco" {
		t.Errorf("line 0 = %q", lines[0])
	}
}

func TestJSONDisablesColor(t *testing.T) {
	f := outfmt.New(outfmt.Options{JSON: true}, &bytes.Buffer{})
	if !f.IsJSON() {
		t.Error("IsJSON() must be true")
	}
	if f.ColorEnabled() {
		t.Error("color must be off in JSON mode")
	}
}

func TestNoColorEnvDisablesColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	f := outfmt.New(outfmt.Options{}, &bytes.Buffer{})
	if f.ColorEnabled() {
		t.Error("NO_COLOR must disable color")
	}
}

func TestPlainDisablesColor(t *testing.T) {
	f := outfmt.New(outfmt.Options{Plain: true}, &bytes.Buffer{})
	if f.ColorEnabled() {
		t.Error("plain mode must disable color")
	}
}
