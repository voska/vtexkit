package cli

import (
	"github.com/alecthomas/kong"
)

// SchemaCmd dumps the command tree so an agent can discover the surface
// without scraping --help. Generated from the live Kong model, so it can
// never drift from the actual commands the way a hand-written table does.
type SchemaCmd struct {
	Command string `arg:"" optional:"" help:"Limit output to one command."`
}

type schemaFlag struct {
	Name    string   `json:"name"`
	Help    string   `json:"help,omitempty"`
	Type    string   `json:"type,omitempty"`
	Default string   `json:"default,omitempty"`
	Env     []string `json:"env,omitempty"`
}

type schemaNode struct {
	Name        string       `json:"name"`
	Help        string       `json:"help,omitempty"`
	Args        []string     `json:"args,omitempty"`
	Flags       []schemaFlag `json:"flags,omitempty"`
	Subcommands []schemaNode `json:"subcommands,omitempty"`
}

func (c *SchemaCmd) Run(g *Globals, kctx *kong.Context) error {
	root := kctx.Model.Node
	out := map[string]any{
		"name":         g.Store.Name,
		"version":      g.Version,
		"store":        g.Store.Label(),
		"globalFlags":  flagsOf(root),
		"commands":     childrenOf(root, c.Command),
		"exitCodesCmd": g.Store.Name + " exit-codes --json",
	}
	return g.Formatter().Print(out)
}

func childrenOf(node *kong.Node, filter string) []schemaNode {
	var out []schemaNode
	for _, child := range node.Children {
		if child.Hidden {
			continue
		}
		if filter != "" && child.Name != filter {
			continue
		}
		out = append(out, nodeOf(child))
	}
	return out
}

func nodeOf(node *kong.Node) schemaNode {
	n := schemaNode{Name: node.Name, Help: node.Help, Flags: flagsOf(node)}
	for _, arg := range node.Positional {
		n.Args = append(n.Args, arg.Name)
	}
	n.Subcommands = childrenOf(node, "")
	return n
}

func flagsOf(node *kong.Node) []schemaFlag {
	var out []schemaFlag
	for _, f := range node.Flags {
		if f.Hidden || f.Name == "help" {
			continue
		}
		out = append(out, schemaFlag{
			Name:    "--" + f.Name,
			Help:    f.Help,
			Type:    f.Value.Target.Type().String(),
			Default: f.Default,
			Env:     f.Envs,
		})
	}
	return out
}
