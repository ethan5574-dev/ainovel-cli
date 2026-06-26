# AI-Tone Removal Criteria

This document is a shared "AI-Tone" criteria library used by both the writer and the editor: the writer avoids all these patterns during writing, and the editor checks them item-by-item under the `aesthetic` dimension during review, **quoting the original text** as evidence.

> Mechanically enumerable parts (em-dashes, fixed cliché phrases, high-frequency fatigue words) are strictly checked by `working_memory.user_rules.structured` during commit. This document specifically governs **semantic judgments that cannot be checked mechanically**. They complement each other: the mechanical layer catches the surface, while this document catches the texture.

## 1. Structural AI-Tone

- **Three-part structure / Triple parallelism**: Using three symmetrically structured short sentences or clauses to "build momentum" ("He no longer hesitated, no longer backed down, no longer looked back"). Modification: Keep the most powerful one, and break the others into actions or details.
- **Symmetric paragraph stacking**: Paragraph lengths and sentence patterns are highly identical, reading like a list. Modification: Alternate long and short sentences to give the pacing room to breathe.
- **Sub-headers / `##` dividers inside chapters**: Using markers like `1`, `2`, `3` or `##`/`###` to segment paragraphs within the chapter body. Modification: Keep only the main chapter title; use blank lines for natural transitions between scenes.

## 2. Diction AI-Tone

- **Stacking four-character idioms**: Crowding multiple idioms into a single paragraph as descriptions ("thrilling, dangerous, critical"). Modification: Replace a string of idioms with a concrete action or image.
- **Simile clichés**: Repeatedly using fixed patterns like "just like...", "as if...", "like a...". Modification: Replace with precise verbs, fresh metaphors, or direct description.
- **Quantifier/filler word obsession**: Using words like "a trace of", "a touch of", "a wisp of" for emotions, or overusing catchphrases like "cannot help but", "unexpectedly", "unconsciously", "as if". Modification: Delete buffering words and let the action happen directly ("He smiled", not "He could not help but trace a smile").
- **Abstract big words**: "to some extent", "it is worth noting", "for some reason", "indescribable"—where the narrator summarizes on behalf of the reader. Modification: Delete these and let concrete facts make the point.
- **Contrast definition pattern**: Repeatedly using "not X, but Y" or "this is not the end, but the beginning" to "summarize the theme" using negation and turning points. Modification: Show directly through a concrete action or choice, instead of relying on sentence patterns to fabricate "golden quotes."

## 3. Description AI-Tone

- **Abstract generalizations instead of concrete senses**: Using label-like summaries like "the environment was oppressive" or "the atmosphere was tense." Modification: Provide a concrete detail that can be perceived by touch, smell, or hearing (which are often superior to pure vision).
- **Labeling emotions**: Directly writing "he was nervous/angry/sad." Modification: Present through physical reactions and choices ("knuckles turning white", "throat tightening") without naming the emotion.

## 4. Dialogue AI-Tone

- **Character homogenization**: Inability to distinguish who is speaking if speaker tags are removed—everyone shares the same sentence lengths, word choices, and cultural level. Modification: Give each character a stable sentence length, catchphrases, and subtext ratio.
- **Over-explaining motivations**: Characters fully spelling out their psychology, or the narrator immediately explaining "he said this because..." right after. Modification: Hide motivations in choices and subtext; trust the reader.
- **Written-language/formal tone**: Everyone speaks in complete, formal sentences with logical connectives. Modification: Spoken language should have pauses, omissions, and indirect answers.

## 5. Pacing and Emotion AI-Tone

- **Explaining everything fully**: Spelling out every action and cause-effect, leaving no room for imagination. Modification: Conceal what should be concealed; use blank spaces/gaps to create tension.
- **Forced sublimation / Theme summarization at endings**: Elevating the end of a chapter to life insights or theme quotes. Modification: Stop on a concrete image, choice, or emotional echo, without summarizing the meaning for the reader.
