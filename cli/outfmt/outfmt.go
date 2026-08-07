// Package outfmt renders command output in the modes agents and humans need.
//
// The rule the whole package exists to enforce: data goes to stdout and is
// parseable; hints, progress, and warnings go to stderr and may be colored.
// Nothing ever mixes the two.
package outfmt

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/muesli/termenv"
)

var profile = termenv.ColorProfile()

// Options selects an output mode. The zero value is human-readable output
// with color, which is what an interactive terminal gets.
type Options struct {
	JSON        bool
	Plain       bool
	Quiet       bool     // bare values, one per line, no keys or headers
	ResultsOnly bool     // strip the metadata envelope, emit just the data
	Select      []string // dot-path field projection: ["sku", "seller.id"]
}

type Formatter struct {
	opts   Options
	writer io.Writer
}

func New(opts Options, w io.Writer) *Formatter {
	return &Formatter{opts: opts, writer: w}
}

func (f *Formatter) IsJSON() bool { return f.opts.JSON }

// ColorEnabled reports whether styled output is allowed. Machine-readable
// modes and NO_COLOR always win over any other preference.
func (f *Formatter) ColorEnabled() bool {
	if f.opts.JSON || f.opts.Plain {
		return false
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return profile != termenv.Ascii
}

// Print renders v according to the configured mode.
func (f *Formatter) Print(v any) error {
	// Projection, envelope stripping, and the tabular modes all need a
	// generic view of the value. Round-tripping through JSON gives that
	// uniformly for structs, maps, and slices alike, and costs nothing at
	// the sizes a CLI prints.
	needsShaping := len(f.opts.Select) > 0 || f.opts.ResultsOnly || f.opts.Quiet || f.opts.Plain
	if !needsShaping {
		if f.opts.JSON {
			return json.NewEncoder(f.writer).Encode(v)
		}
		_, err := fmt.Fprintln(f.writer, v)
		return err
	}

	shaped, err := toGeneric(v)
	if err != nil {
		return err
	}
	if f.opts.ResultsOnly {
		shaped = stripEnvelope(shaped)
	}
	if len(f.opts.Select) > 0 {
		shaped = project(shaped, f.opts.Select)
	}

	switch {
	case f.opts.Quiet:
		return f.printQuiet(shaped)
	case f.opts.Plain:
		return f.printPlain(shaped)
	case f.opts.JSON:
		return json.NewEncoder(f.writer).Encode(shaped)
	default:
		_, err := fmt.Fprintln(f.writer, shaped)
		return err
	}
}

func toGeneric(v any) (any, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// envelopeFields are the keys VTEX and this CLI use to wrap a result set.
// ResultsOnly unwraps the first one it finds.
var envelopeFields = []string{"items", "products", "results", "list", "data"}

func stripEnvelope(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	for _, key := range envelopeFields {
		if inner, ok := m[key]; ok {
			return inner
		}
	}
	return v
}

// project keeps only the requested dot-paths. Paths that do not exist are
// skipped rather than rendered as null, because an agent guessing a field
// name should still get back the fields that do exist.
func project(v any, paths []string) any {
	switch t := v.(type) {
	case []any:
		out := make([]any, 0, len(t))
		for _, elem := range t {
			out = append(out, project(elem, paths))
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for _, p := range paths {
			copyPath(t, out, strings.Split(p, "."))
		}
		return out
	default:
		return v
	}
}

func copyPath(src, dst map[string]any, parts []string) {
	if len(parts) == 0 {
		return
	}
	val, ok := src[parts[0]]
	if !ok {
		return
	}
	if len(parts) == 1 {
		dst[parts[0]] = val
		return
	}
	nestedSrc, ok := val.(map[string]any)
	if !ok {
		return
	}
	nestedDst, ok := dst[parts[0]].(map[string]any)
	if !ok {
		nestedDst = map[string]any{}
		dst[parts[0]] = nestedDst
	}
	copyPath(nestedSrc, nestedDst, parts[1:])
}

func (f *Formatter) printQuiet(v any) error {
	for _, row := range rows(v) {
		vals := f.orderedValues(row)
		if len(vals) == 0 {
			continue
		}
		if _, err := fmt.Fprintln(f.writer, vals[0]); err != nil {
			return err
		}
	}
	return nil
}

func (f *Formatter) printPlain(v any) error {
	for _, row := range rows(v) {
		if _, err := fmt.Fprintln(f.writer, strings.Join(f.orderedValues(row), "\t")); err != nil {
			return err
		}
	}
	return nil
}

func rows(v any) []any {
	if list, ok := v.([]any); ok {
		return list
	}
	return []any{v}
}

// orderedValues renders a row's fields. Select order is authoritative when
// given, because map iteration order is not stable and --plain output is a
// contract that scripts depend on.
func (f *Formatter) orderedValues(row any) []string {
	m, ok := row.(map[string]any)
	if !ok {
		return []string{fmt.Sprint(row)}
	}
	var out []string
	if len(f.opts.Select) > 0 {
		for _, p := range f.opts.Select {
			if val, ok := lookupPath(m, strings.Split(p, ".")); ok {
				out = append(out, scalar(val))
			}
		}
		return out
	}
	for _, val := range m {
		out = append(out, scalar(val))
	}
	return out
}

func lookupPath(m map[string]any, parts []string) (any, bool) {
	cur := any(m)
	for _, p := range parts {
		nested, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = nested[p]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func scalar(v any) string {
	if fl, ok := v.(float64); ok && fl == float64(int64(fl)) {
		return fmt.Sprintf("%d", int64(fl))
	}
	return fmt.Sprint(v)
}

func styled(msg string, color string) string {
	if os.Getenv("NO_COLOR") != "" || profile == termenv.Ascii {
		return msg
	}
	return termenv.String(msg).Foreground(profile.Color(color)).String()
}

// Hint, Success, Warn, and ErrorMsg write to stderr so they never pollute
// parseable stdout.
func Hint(format string, args ...any) {
	fmt.Fprintln(os.Stderr, styled(fmt.Sprintf(format, args...), "8"))
}

func Success(format string, args ...any) {
	fmt.Fprintln(os.Stderr, styled(fmt.Sprintf(format, args...), "2"))
}

func Warn(format string, args ...any) {
	fmt.Fprintln(os.Stderr, styled(fmt.Sprintf(format, args...), "3"))
}

func ErrorMsg(format string, args ...any) {
	fmt.Fprintln(os.Stderr, styled(fmt.Sprintf(format, args...), "1"))
}
