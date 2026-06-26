---
# Built-in Default Rules (Phase 1 Safe Version)
#
# Only put "mechanically checkable + low controversy" default constraints here.
# Non-mechanical aesthetic preferences (like style tendencies) are still carried by writer.md / editor.md for now,
# and we will decide whether to move them to this file after Phase 1.5 validation.
#
# Users can override normal fields in the ./.ainovel/rules/ or ~/.ainovel/rules/ directory (any .md file under them);
# fatigue_words are merged by word, and a closer source overrides the threshold for the same word.
# See rules.md.example at the project root for detailed field semantics.

# Chapter word count range: deviation <20% triggers warning; ≥20% triggers error.
chapter_words: 3000-6000

# Phrase blacklist: error if appearing ≥1 time. The checker does literal substring matching, no wildcards,
# so only put "fixed-length literal strings" of AI clichés here (low controversy); patterns with variables
# (like "not X but Y") cannot be caught by literal matching, and are handled in anti-ai-tone.md at the semantic layer.
# Em-dashes "——" are legal when dialogues are interrupted (controversial), so they are not in the built-in defaults
# and are left to ./.ainovel/rules/ for custom configuration.
forbidden_phrases:
  - "to some extent"
  - "it is worth noting"
  - "for some reason"
  - "mixed feelings"

# Fatigue word soft limits: commit_chapter checks occurrences in each chapter, warning if exceeding the threshold.
# These are commonly overused words in novels, and anti-ai-tone.md has semantic prompts in the same direction—consistent dual signals.
# The last few (like a / fell silent / did not speak / breaths) are derived from the 196-chapter long-run validation:
# traditional clichés were wiped out by the above rules, but the model turned to using these "pacing words" 5-7 times per chapter;
# thresholds are relaxed to tolerate normal usage.
fatigue_words:
  cannot help but: 1
  unexpectedly: 1
  as if: 2
  besides: 1
  however: 2
  a trace of: 2
  a touch of: 2
  a wisp of: 2
  just like: 1
  could not help: 1
  like a: 3
  fell silent: 2
  did not speak: 2
  a few breaths: 3
  one breath: 3
  several breaths: 2
---
