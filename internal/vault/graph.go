package vault

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/didenkolab/docket/internal/project"
)

// The graph view, configured.
//
// Obsidian's graph is the whole argument for keeping a tracker in a vault —
// purpose §3 ends "open the repository in Obsidian and see the graph — of
// what". Opened on a real vault it was forty-six identical grey dots. A task,
// an epic, a label, a sprint and a wiki page looked the same, so the only thing
// the picture said was how many notes there were.
//
// The settings that fix that live in a file inside the vault, which means they
// can be shipped the way the boards are. They were not, because the file also
// holds the zoom level and which panels are folded, and the whole thing was put
// in .gitignore as "per-machine state". That was half right: the zoom is
// per-machine, and the colour groups are as much vault content as a board is.
//
// What this cannot do is make a force layout of a whole project readable. That
// is a property of force layouts, not of the settings: labels either overlap or
// disappear, and node size only ever means link count. The global graph answers
// "what shape is this" — where the clusters are, what is an island, which note
// is a hub. "What is connected to what" is the local graph of a note, and
// `docket graph` is for the questions a picture cannot answer at all.

// GraphFile is where Obsidian keeps the graph view's settings.
const GraphFile = ".obsidian/graph.json"

// Colours, chosen to be legible on both of Obsidian's themes and to mean
// something rather than to be pretty: work in flight is the one colour that
// draws the eye, closed work recedes, and the two kinds of hub — a sprint in
// time, a page of knowledge — are told apart at a glance.
const (
	colourSprint = 0xfbbf24 // amber: a fortnight
	colourEpic   = 0xa78bfa // violet: a container
	colourDoing  = 0x60a5fa // blue: moving
	colourDone   = 0x34d399 // green: closed
	colourTodo   = 0x94a3b8 // grey: not started
	colourDocs   = 0x22d3ee // cyan: knowledge
)

type graphColour struct {
	A   int `json:"a"`
	RGB int `json:"rgb"`
}

type graphGroup struct {
	Query string      `json:"query"`
	Color graphColour `json:"color"`
}

// GraphSettings is Obsidian's graph.json, in the order it writes it.
type GraphSettings struct {
	CollapseFilter      bool         `json:"collapse-filter"`
	Search              string       `json:"search"`
	ShowTags            bool         `json:"showTags"`
	ShowAttachments     bool         `json:"showAttachments"`
	HideUnresolved      bool         `json:"hideUnresolved"`
	ShowOrphans         bool         `json:"showOrphans"`
	CollapseColorGroups bool         `json:"collapse-color-groups"`
	ColorGroups         []graphGroup `json:"colorGroups"`
	CollapseDisplay     bool         `json:"collapse-display"`
	ShowArrow           bool         `json:"showArrow"`
	TextFadeMultiplier  float64      `json:"textFadeMultiplier"`
	NodeSizeMultiplier  float64      `json:"nodeSizeMultiplier"`
	LineSizeMultiplier  float64      `json:"lineSizeMultiplier"`
	CollapseForces      bool         `json:"collapse-forces"`
	CenterStrength      float64      `json:"centerStrength"`
	RepelStrength       float64      `json:"repelStrength"`
	LinkStrength        float64      `json:"linkStrength"`
	LinkDistance        float64      `json:"linkDistance"`
	Scale               float64      `json:"scale"`
	Close               bool         `json:"close"`
}

// GraphConfig is what this vault's graph settings should hold.
//
// The colour groups are written in the vault's own words: a vault whose epic
// type is called Эпик gets a group querying that, because the type names belong
// to the vault and nothing central has a list of them.
func GraphConfig(c *project.Config) GraphSettings {
	var groups []graphGroup

	// A sprint first, because the query is exact and the others are broad: the
	// first group that matches a note is the one that colours it.
	groups = append(groups, graphGroup{
		Query: property("type", SprintType), Color: graphColour{A: 1, RGB: colourSprint},
	})
	// Containers, whatever this vault calls them. A vault that has not said what
	// its levels are has no containers to colour.
	for _, t := range c.Types {
		if c.LevelOf(t.Name) > 0 {
			groups = append(groups, graphGroup{
				Query: property("type", t.Name), Color: graphColour{A: 1, RGB: colourEpic},
			})
		}
	}
	// Then the work, by whether it is moving. Categories rather than statuses:
	// a vault with eight statuses would otherwise need eight colours, and the
	// question a graph answers is coarser than that.
	for _, byCategory := range []struct {
		category string
		colour   int
	}{
		{project.CategoryDoing, colourDoing},
		{project.CategoryDone, colourDone},
		{project.CategoryTodo, colourTodo},
	} {
		groups = append(groups, graphGroup{
			Query: property("status_category", byCategory.category),
			Color: graphColour{A: 1, RGB: byCategory.colour},
		})
	}
	// Everything else in the knowledge base: label pages and wiki pages, which
	// no property tells apart — a label page is a page that tasks happen to
	// link to, and Obsidian's search cannot ask that.
	groups = append(groups, graphGroup{
		Query: "path:" + DocsDir, Color: graphColour{A: 1, RGB: colourDocs},
	})

	return GraphSettings{
		ColorGroups: groups,

		// A tag is not a node — that is the whole reason tags are cheap and
		// links are spent deliberately, and showing them as nodes would undo
		// it. See docs/design/how-things-connect.md §6.
		ShowTags:        false,
		ShowAttachments: false,
		// An island is usually an owner who left, and hiding it is hiding the
		// fourth question the graph is for.
		ShowOrphans:    true,
		HideUnresolved: false,

		// Arrows on an undirected reading are decoration that costs legibility:
		// writing [[X]] in A puts them next to each other whichever end typed
		// it, so a direction on the line means less than it looks.
		ShowArrow: false,
		// Labels appear as the reader zooms in. There is no setting that shows
		// them all at once and keeps them readable — at forty-six notes they
		// overlap, and that is the smallest a real vault gets.
		TextFadeMultiplier: 1,
		NodeSizeMultiplier: 2,
		LineSizeMultiplier: 0.8,

		// Weak pull to the middle so clusters can sit apart, strong links so
		// what is connected stays together, and enough repulsion that the
		// unconnected do not pile up. Tuned by looking, not by theory.
		CenterStrength: 0.2,
		RepelStrength:  18,
		LinkStrength:   1,
		LinkDistance:   110,

		Scale: 1,
		Close: true,
	}
}

// WriteGraph writes the graph settings, keeping whatever the person at this
// machine had chosen about the view itself.
//
// The zoom and which panels are folded are theirs; the colours and the forces
// are the vault's. Overwriting the first to deliver the second is how a tool
// earns being switched off.
func WriteGraph(root string, c *project.Config) error {
	want := GraphConfig(c)

	full := filepath.Join(root, filepath.FromSlash(GraphFile))
	if raw, err := os.ReadFile(full); err == nil {
		var had GraphSettings
		if json.Unmarshal(raw, &had) == nil {
			want.Scale = had.Scale
			want.Search = had.Search
			want.CollapseFilter = had.CollapseFilter
			want.CollapseColorGroups = had.CollapseColorGroups
			want.CollapseDisplay = had.CollapseDisplay
			want.CollapseForces = had.CollapseForces
			want.Close = had.Close
		}
	}
	if want.Scale == 0 {
		want.Scale = 1
	}

	body, err := json.MarshalIndent(want, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, append(body, '\n'), 0o644)
}

// property is Obsidian's search for a frontmatter value. Both sides are quoted
// so a value with a space or a non-Latin letter is one term rather than two.
func property(name, value string) string {
	quoted, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	key, err := json.Marshal(name)
	if err != nil {
		return ""
	}
	return "[" + string(key) + ":" + string(quoted) + "]"
}
