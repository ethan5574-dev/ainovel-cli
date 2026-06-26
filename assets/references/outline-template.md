# Outline Planning Template

The purpose of this template is not to force all works into a fixed length, but to help determine the work level first, and then choose the appropriate outline granularity.

## Step 1: Determine the Work Length Level

### Short Form / Single-Volume Story

- **Applicability**: Single conflict, single goal, few characters, focused ending.
- **Reference Length**: 8-25 chapters.
- **Recommended Format**: Flat `outline`.

### Mid Form / Multi-stage Story

- **Applicability**: Stage upgrades, several subplots, evolving character relationships.
- **Reference Length**: 25-60 chapters.
- **Recommended Format**: Flat `outline` or lightweight layered outline.

### Long-form Serialization / Web-novel-style Story

- **Applicability**: Genres with natural room for continuous upgrades, long-term relationship tension, multiple stage goals, expandable world, long-term mysteries, or long-term growth threads.
- **Reference Length**: 80-200+ chapters.
- **Recommended Format**: Layered `layered_outline`.

## Step 2: Determine if a Layered Outline is Required

Prioritize using `layered_outline` if at least 2 of the following conditions are met:

- The worldview needs to unfold step-by-step rather than all at once.
- The protagonist's growth is a multi-stage upgrade rather than a single jump.
- Character relationships continue to evolve across multiple stages.
- Different types of primary contradictions exist in the mid and late stages.
- Multiple maps, factions, identities, or goals need to be switched.
- The genre is clearly closer to serialized commercial fiction rather than a single-volume story.

## Step 3: Do Not Create a "Book-wide Chapter Ledger" Directly for Long-form

The recommended long-form planning sequence is:

1. Selling points and differentiation of the work.
2. Long-term story engines.
3. Volume-level themes and upgrades.
4. Arc-level goals and stage turning points.
5. Chapter-level events and hooks.

**Incorrect Approaches**:

- Writing a 20-chapter synopsis first and then forcing it to stretch.
- Repeating "encountering enemies → getting stronger → changing maps" in every volume.
- Only upgrading the main plot, without upgrading relationships.
- Exhausting all major secrets early on, leaving only repetitive formula in the middle and late stages.

## Flat Outline Template (Short/Mid Form)

```json
[
  {
    "chapter": 1,
    "title": "Chapter Title",
    "core_event": "Core event of this chapter",
    "hook": "Hook at the end of the chapter",
    "scenes": ["Scene 1", "Scene 2", "Scene 3"]
  }
]
```

## Layered Outline Template (Long-form - Vol/Arc Dual-level Rolling Expansion)

Initial planning uses dual-level rolling: the first 2 volumes have arc skeletons, while the remaining volumes are skeleton volumes; the first arc has detailed chapters.

```json
[
  {
    "index": 1,
    "title": "Volume 1 Title",
    "theme": "The new core contradiction/theme introduced in this volume",
    "arcs": [
      {
        "index": 1,
        "title": "First Arc (Expanded)",
        "goal": "Local goals, resistance, and turning points",
        "chapters": [
          {"chapter": 1, "title": "Chapter Title", "core_event": "Core Event", "hook": "Ending Hook", "scenes": ["Scene 1", "Scene 2"]}
        ]
      },
      {
        "index": 2,
        "title": "Second Arc (Skeleton Arc)",
        "goal": "Overview of this arc's goal",
        "estimated_chapters": 12,
        "chapters": []
      }
    ]
  },
  {
    "index": 2,
    "title": "Volume 2 Title",
    "theme": "Volume 2 theme",
    "arcs": [
      {"index": 1, "title": "Arc Title", "goal": "Arc Goal", "estimated_chapters": 15, "chapters": []},
      {"index": 2, "title": "Arc Title", "goal": "Arc Goal", "estimated_chapters": 10, "chapters": []}
    ]
  },
  {
    "index": 3,
    "title": "Volume 3 Title (Skeleton Volume)",
    "theme": "Volume 3 theme direction",
    "estimated_chapters": 60,
    "arcs": []
  }
]
```

- **Arc Expansion**: When writing progresses to a skeleton arc, the Architect expands the detailed chapters of that arc.
- **Volume Expansion**: When writing progresses to a skeleton volume, the Architect expands the arc structure + the first arc's chapters for that volume.

## Long-form Volume-level Checklist

For each volume, answer:

- What new world information is introduced in this volume?
- What core contradiction is upgraded in this volume?
- What does this volume let the protagonist gain, and what do they lose?
- How does this volume change major character relationships?
- Why must the story enter the next volume after this one ends?

## Long-form Arc-level Checklist

For each arc, answer:

- What is the clear goal of this arc?
- Where does the resistance come from (who, what rule, what cost)?
- What is the turning point?
- What states have changed irreversibly after this arc ends?

## Chapter-level Checklist

- Each chapter must serve the goal of its arc.
- Each chapter must contain an indispensable event progression.
- Hooks must be diverse; do not rely solely on "discovering a secret."
- Early chapters cannot just "introduce the world"; they must simultaneously advance characters and conflicts.
