package vault

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/didenkolab/docket/internal/task"
)

// What shape the vault is, as numbers.
//
// This exists because looking at the graph does not answer the questions the
// graph is for. A force layout of a whole project is a hairball whatever the
// settings, and every judgement made about one here — that index pages were
// costing the vault its structure, that sprint pages citing their own tasks
// were worse — was made by counting, not by looking. Twice the picture said
// "busy" and the count said which part was wrong and by how much.
//
// So the counting is a command rather than a script somebody wrote once. The
// four questions are the ones docs/design/how-things-connect.md opens with:
// what moves with what, what is this part of, where does work pile up, and what
// is nobody looking after.

// addressed are the files written for whoever opens the repository rather than
// for the vault. Nothing links to them and nothing should.
var addressed = map[string]bool{
	"README": true, "AGENTS": true, "CLAUDE": true, "LICENSE": true, "CONTRIBUTING": true,
	"TEMPLATE": true,
}

// Shape is a vault's graph, measured.
type Shape struct {
	Notes int
	Edges int
	// Clusters are the connected components, largest first. One cluster holding
	// nearly everything means no structure at all: every note a few hops from
	// every other.
	Clusters []Cluster
	// Hubs are the most connected notes, most first.
	Hubs []Hub
	// Islands are the notes with no edges — the fourth question, and usually an
	// owner who left.
	Islands []string
}

// Cluster is one connected component.
type Cluster struct {
	Size int
	// Named is the most connected note in it, which is what to call it.
	Named string
}

// Hub is one note and how many edges it carries.
type Hub struct {
	Note  string
	Edges int
	// Share is its edges as a percentage of every edge in the vault.
	Share float64
}

// Largest is the biggest cluster's size, or nought for an empty vault.
func (s Shape) Largest() int {
	if len(s.Clusters) == 0 {
		return 0
	}
	return s.Clusters[0].Size
}

// Concentration is the share of edges held by the most connected note.
//
// The number that caught both mistakes. Index pages held eighteen per cent
// between two of them and were deleted for it; sprint pages reached
// twenty-eight per cent before anybody looked at the picture and saw only that
// it was busy.
func (s Shape) Concentration() float64 {
	if len(s.Hubs) == 0 {
		return 0
	}
	return s.Hubs[0].Share
}

// Measure reads every note in a vault and works out the shape of its links.
//
// Links are counted the way Obsidian draws them: undirected, once per pair,
// resolving to a note that exists. A link written twice is one edge, because
// on the canvas it is one line.
func Measure(root string) (Shape, error) {
	notes := map[string]string{} // note name → path, for what a link can resolve to
	bodies := map[string]string{}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if Hidden(d.Name()) || d.Name() == TemplatesDir {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(d.Name(), ".md")
		// A note either way — a link to it resolves and draws an edge — but not
		// something to report as an island. README and AGENTS.md exist for
		// whoever opens the repository, not as knowledge, and nothing linking
		// to them is the arrangement rather than a fault.
		notes[name] = path
		bodies[name] = string(raw)
		return nil
	})
	if err != nil {
		return Shape{}, err
	}

	type pair struct{ a, b string }
	edges := map[pair]bool{}
	for name, body := range bodies {
		for _, target := range task.Links(body) {
			target = lastSegment(target)
			if target == name {
				continue // a note linking itself is not an edge
			}
			if _, real := notes[target]; !real {
				continue // an unresolved link draws nothing
			}
			if name < target {
				edges[pair{name, target}] = true
			} else {
				edges[pair{target, name}] = true
			}
		}
	}

	next := map[string]map[string]bool{}
	for e := range edges {
		if next[e.a] == nil {
			next[e.a] = map[string]bool{}
		}
		if next[e.b] == nil {
			next[e.b] = map[string]bool{}
		}
		next[e.a][e.b] = true
		next[e.b][e.a] = true
	}

	shape := Shape{Notes: len(notes), Edges: len(edges)}

	for name := range notes {
		if len(next[name]) == 0 {
			if !addressed[name] {
				shape.Islands = append(shape.Islands, name)
			}
			continue
		}
		share := 0.0
		if shape.Edges > 0 {
			share = 100 * float64(len(next[name])) / float64(shape.Edges)
		}
		shape.Hubs = append(shape.Hubs, Hub{Note: name, Edges: len(next[name]), Share: share})
	}
	sort.Slice(shape.Hubs, func(i, j int) bool {
		if shape.Hubs[i].Edges != shape.Hubs[j].Edges {
			return shape.Hubs[i].Edges > shape.Hubs[j].Edges
		}
		return shape.Hubs[i].Note < shape.Hubs[j].Note
	})
	sort.Strings(shape.Islands)

	shape.Clusters = clustersOf(notes, next)
	return shape, nil
}

// clustersOf walks the graph and reports each connected component, largest
// first, named after its most connected note.
func clustersOf(notes map[string]string, next map[string]map[string]bool) []Cluster {
	seen := map[string]bool{}
	var out []Cluster

	names := make([]string, 0, len(notes))
	for name := range notes {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, start := range names {
		if seen[start] || len(next[start]) == 0 {
			continue
		}
		found := map[string]bool{}
		stack := []string{start}
		for len(stack) > 0 {
			at := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if found[at] {
				continue
			}
			found[at] = true
			seen[at] = true
			for on := range next[at] {
				if !found[on] {
					stack = append(stack, on)
				}
			}
		}

		named, most := "", -1
		for name := range found {
			if len(next[name]) > most || (len(next[name]) == most && name < named) {
				named, most = name, len(next[name])
			}
		}
		out = append(out, Cluster{Size: len(found), Named: named})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Size != out[j].Size {
			return out[i].Size > out[j].Size
		}
		return out[i].Named < out[j].Named
	})
	return out
}

// lastSegment is a wikilink target without its folder, heading or display text.
func lastSegment(target string) string {
	if bar := strings.Index(target, "|"); bar >= 0 {
		target = target[:bar]
	}
	if hash := strings.Index(target, "#"); hash >= 0 {
		target = target[:hash]
	}
	if slash := strings.LastIndex(target, "/"); slash >= 0 {
		target = target[slash+1:]
	}
	return strings.TrimSpace(target)
}
