# Editorial notes: no-multiple-primaries.md

- Wrap body text (paragraphs and list items) to 80 columns. Headings (`#`/`##`/
  `###`/`####` lines) are left unwrapped, since a Markdown heading must stay on
  a single line to render correctly. Fenced code blocks (```` ``` ````) are
  also left untouched, since wrapping would break their formatting.
- Use descriptive keys, not sequential numbers, for every Definition, Axiom,
  Observation, Lemma, and Case: `### Type (descriptive-key)`, or
  `### Type (descriptive-key): elaboration` when a short elaboration helps.
  Refer back to them in text as `Type (descriptive-key)` (e.g. `Axiom
  (write-consistency)`, `Case (voluntary-release)`). This avoids renumbering
  when new items are inserted, and keeps cross-references self-describing.