# Chapter Revision Analysis

Compare the system's accepted chapter version with the user's edited version. The edited prose is authoritative. Reconstruct facts; do not evaluate or rewrite the user's text.

## Principles

- facts describes the entire revised chapter, not just differences.
- revised_content is the full new prose. changed_excerpt contains old/new fragments after removing identical prefixes and suffixes, to help identify editing intent.
- Extract only supported facts; do not invent events absent from the text.
- Reuse IDs from previous_facts for foreshadowing that remains valid. Do not retain deleted events.
- style_delta records reusable preferences expressed by deliberate edits. Typos, proper-name corrections, and plot-only changes are not style preferences.
- story_changed indicates changed narrative facts. Return outline_impact only if those changes affect unwritten plans; otherwise return null.
- downstream_issues lists specific conflicts with completed later chapters, or an empty array if none.
- Do not output prose or propose reverting the user's edits.
