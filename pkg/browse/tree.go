package browse

import (
	"sort"
	"strings"
)

// NodeKind identifies the level of a node in the navigation tree.
type NodeKind int

const (
	KindLayer NodeKind = iota
	KindSource
	KindTeam
	KindConversation
	KindYear
	KindMonth
)

// Node is a node of the navigation tree built from the object listing.
type Node struct {
	Kind     NodeKind
	Label    string // human-readable label (cosmetic mapping applied)
	Slug     string // raw path segment in the object key
	Prefix   string // S3 key prefix up to and including this node
	Children []*Node
	Object   *Object // set for KindMonth leaves
}

// Tree is the full navigation tree rooted at the archive bucket.
type Tree struct {
	Root *Node
}

// BuildTree builds the navigation tree from a list of object keys.
// Keys that do not match the expected layout are ignored.
func BuildTree(keys []string) (*Tree, error) {
	root := &Node{Kind: KindLayer, Label: "conversations", Prefix: ""}

	for _, key := range keys {
		obj, err := ParseObjectKey(key)
		if err != nil {
			continue
		}

		layer := findOrCreate(root, KindLayer, obj.Layer, obj.Layer+"/")
		source := findOrCreate(layer, KindSource, obj.Source, layer.Prefix+obj.Source+"/")
		team := findOrCreate(source, KindTeam, obj.Team, source.Prefix+obj.Team+"/")

		// AI export threads have no conversation segment: the year sits
		// directly under the team.
		var year *Node
		if obj.Conversation != "" {
			conv := findOrCreate(team, KindConversation, obj.Conversation, team.Prefix+obj.Conversation+"/")
			year = findOrCreate(conv, KindYear, obj.Year, conv.Prefix+obj.Year+"/")
		} else {
			year = findOrCreate(team, KindYear, obj.Year, team.Prefix+obj.Year+"/")
		}

		if findChild(year, obj.Month) == nil {
			year.Children = append(year.Children, &Node{
				Kind:   KindMonth,
				Label:  obj.Month,
				Slug:   obj.Month,
				Prefix: key,
				Object: obj,
			})
		}
	}

	sortTree(root)
	return &Tree{Root: root}, nil
}

func findOrCreate(parent *Node, kind NodeKind, slug, prefix string) *Node {
	if child := findChild(parent, slug); child != nil {
		return child
	}
	child := &Node{Kind: kind, Label: displayLabel(kind, slug), Slug: slug, Prefix: prefix}
	parent.Children = append(parent.Children, child)
	return child
}

func findChild(parent *Node, slug string) *Node {
	for _, c := range parent.Children {
		if c.Slug == slug {
			return c
		}
	}
	return nil
}

// displayLabel applies the cosmetic mapping of known pseudo-teams. Other
// segments are shown as their raw slug (de-slugifying is lossy).
func displayLabel(kind NodeKind, slug string) string {
	if kind == KindTeam {
		switch slug {
		case "-":
			return "No team"
		case "direct-messages":
			return "Direct messages"
		case "group-messages":
			return "Group messages"
		}
	}
	if kind == KindLayer {
		switch slug {
		case LayerJSONL:
			return "JSONL (raw)"
		case LayerMarkdown:
			return "Markdown (readable)"
		}
	}
	return slug
}

// sortTree sorts children in place: teams and conversations alphabetically,
// years descending, months ascending.
func sortTree(n *Node) {
	for _, c := range n.Children {
		sortTree(c)
	}
	sort.SliceStable(n.Children, func(i, j int) bool {
		a, b := n.Children[i], n.Children[j]
		if a.Kind == KindYear && b.Kind == KindYear {
			return a.Slug > b.Slug
		}
		return strings.ToLower(a.Label) < strings.ToLower(b.Label)
	})
}
