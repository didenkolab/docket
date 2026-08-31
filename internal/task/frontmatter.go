package task

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// Frontmatter is every property the file declares, which is what a saved view
// filters and tabulates.
//
// Two maps rather than one, because a list and a scalar are asked different
// questions: `note.labels.contains("server")` is about the items, and
// `note.status == "Done"` is about the value. A list appears in both — joined
// in values, so a one-item list compares as itself.
func (t *Task) Frontmatter() (values map[string]string, lists map[string][]string) {
	values, lists = map[string]string{}, map[string][]string{}
	if len(t.front.Content) == 0 {
		return values, lists
	}

	mapping := t.front.Content[0]
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		name, node := mapping.Content[i].Value, mapping.Content[i+1]
		if node.Kind == yaml.SequenceNode {
			var items []string
			for _, item := range node.Content {
				items = append(items, strings.TrimSpace(item.Value))
			}
			lists[name] = items
			values[name] = strings.Join(items, ", ")
			continue
		}
		values[name] = strings.TrimSpace(node.Value)
	}
	return values, lists
}
