package importer

import "testing"

// A Confluence space and a Jira project refer to each other by writing the key
// down. In one vault, writing it down is the link — so the task shows the
// specification in its backlinks and the graph draws them together. Without
// this, sixteen imported specification pages named their issues and not one of
// the issues knew.
func TestLinkMentions(t *testing.T) {
	notes := map[string]string{
		"ACME-940":  "ACME-940 A single UUID for external identifiers",
		"ACME-1109": "ACME-1109 Close the third log write",
	}

	for _, c := range []struct{ what, given, want string }{
		{
			"a key in a sentence",
			"Обсуждение по ACME-940 идёт в комментариях.",
			"Обсуждение по [[ACME-940 A single UUID for external identifiers]] идёт в комментариях.",
		},
		{
			"Jira's own link keeps the label somebody wrote",
			"**Задача:** [ACME-940 — [SPEC] Единый UUID](https://x.atlassian.net/browse/ACME-940).",
			"**Задача:** [[ACME-940 A single UUID for external identifiers|ACME-940 — [SPEC] Единый UUID]].",
		},
		{
			"two keys in a row: the second must not be eaten by the first",
			"See ACME-940 ACME-1109 for both.",
			"See [[ACME-940 A single UUID for external identifiers]] " +
				"[[ACME-1109 Close the third log write]] for both.",
		},
		{
			"a key the import did not write stays text, not a dead link",
			"Blocked by ACME-77777 elsewhere.",
			"Blocked by ACME-77777 elsewhere.",
		},
		{
			"a longer identifier is not a key",
			"The case ACME-940-A and the tag ACME-INV-055 are not issues.",
			"The case ACME-940-A and the tag ACME-INV-055 are not issues.",
		},
		{
			"code is a value, not a reference",
			"Send `{\"issue\": \"ACME-940\"}` to the endpoint.",
			"Send `{\"issue\": \"ACME-940\"}` to the endpoint.",
		},
		{
			"a fenced block is left exactly as it was",
			"```\ncurl /browse/ACME-940\n```\nand ACME-940 in prose.",
			"```\ncurl /browse/ACME-940\n```\nand [[ACME-940 A single UUID for external identifiers]] in prose.",
		},
		{
			"a link already made is not made twice",
			"Already [[ACME-940 A single UUID for external identifiers]] here.",
			"Already [[ACME-940 A single UUID for external identifiers]] here.",
		},
	} {
		if got := linkMentions(c.given, notes); got != c.want {
			t.Errorf("%s:\n given %q\n  got  %q\n  want %q", c.what, c.given, got, c.want)
		}
	}
}

// Nothing to link to means nothing changes: an import of pages without issues
// must not rewrite a single character.
func TestLinkMentionsWithNothingImported(t *testing.T) {
	body := "ACME-940 and [ACME-1109](https://x/browse/ACME-1109)."
	if got := linkMentions(body, nil); got != body {
		t.Errorf("rewrote a page with no tasks to link to: %q", got)
	}
}
