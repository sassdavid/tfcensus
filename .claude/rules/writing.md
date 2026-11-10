# Writing

These rules cover everything written in this repo: Markdown, code comments, CLI help text,
commit messages, PR descriptions and issues. `mise run lint:prose` checks the word list, dashes,
quotes, emoji and bold list labels. The rest is judgment, and the checker can't do it for you.

Sources: Wikipedia's "Signs of AI writing" and "AI content detection" pages, go.dev/doc/comment,
the Google developer documentation style guide (voice, tense, word list), and the Mintlify and
Proxmox style guides. Detectors and word lists are weak evidence on their own. The aim is plain,
specific writing, not dodging a detector.

## Say the thing

- State facts directly. "The loader reads .tfcensus.yaml", not "the loader serves as a bridge to
  your settings".
- Use "is", "has" and "runs". Don't swap them for "serves as", "stands as", "functions as",
  "boasts" or "features".
- Give the number, command, file or flag. Don't claim importance ("a key part of",
  "plays a vital role", "plays a key role") without saying what it does.
- One idea per sentence. Keep most sentences under 25 words and paragraphs to two to four
  sentences.
- Use common words: use, start, show, help, need, about. Not utilize, commence, facilitate,
  approximately. Plain constructions are fine: "there is", "it has", "wrote", "used".
- Active voice, present tense, and "you" for the reader. Say who does what: "the server sends
  an acknowledgment", not "an acknowledgment is sent". Use "will" only for something that
  really happens later.
- Say how two things relate. Not "connected to", "associated with" or "in connection with" when
  you mean "calls", "reads" or "is part of".
- Use "such as" only when the list really is a sample. If it's the whole list, say so or drop
  "such as".
- Name your source or drop the claim. No "experts say", "industry reports" or "observers note".
- End when you're done. No closing paragraph that repeats the point, and no "In summary",
  "In conclusion" or "Overall". No "Despite X, it faces challenges" wrap-ups and no "Future
  outlook" sections.
- Don't argue with a claim nobody made: no "not only X but also Y", "it's not X, it's Y",
  or "Y rather than X" setups.
- Don't group things in threes out of habit. List what actually exists.
- Don't hang an -ing clause off a sentence to add weight ("..., highlighting its flexibility",
  "..., ensuring reliability"). Write a second sentence if the point matters.
- Call one thing by one name. Don't rotate synonyms to avoid repeating a word.
- No openers, closers or chat phrases: "It's important to note", "It is important to note",
  "It's worth noting", "Great question", "I hope this helps", "Let me know", "As an AI".
- Don't hedge about sources ("based on available information"). Say what you checked.
- No service-desk tone: "carefully reviewed", "rest assured", apologies, "please" in
  instructions.
- No leftovers: placeholders like `[Year]`, `TODO: link` or example.com URLs, and no tracking
  parameters (`utm_source=`) in links.
- If you're unsure, say so once and say what would settle it.

## Words to avoid

The checker rejects these anywhere, in any case. When one really is the right word (for
example "landscape" as a page orientation), put `prose:ignore` on that line.

additionally, boasts, bolstered, comprehensive, crucial, delve, delves, delving, effortless,
effortlessly, elevate, embark, empower, endeavor, enhance, enhances, enhancing, foster, fostering,
garner, groundbreaking, holistic, interplay, intricate, intricacies, landscape, leverage, leverages,
leveraging, meticulous, meticulously, multifaceted, myriad, noteworthy, paradigm, pivotal,
plethora, realm, robust, seamless, seamlessly, showcase, showcases, showcasing, streamline,
streamlined, synergy, tapestry, testament, transformative, unleash, utilize, utilizes, utilizing,
vibrant.

Phrases: a testament to, as an ai, deep dive, great question, i hope this helps, in conclusion,
in summary, it is important to note, it's important to note, it's worth noting, let me know,
not only, plays a key role, plays a vital role, serves as, stands as.

Also watch for these, which the checker doesn't catch: highlight, underscore and emphasize used
as verbs for significance, "align with", "evolving", "key" as a filler adjective, "valuable",
"insights", "ensure" at the end of a sentence, "prioritize". Also drop words that judge the
reader's task: simple, simply, easy, easily, just, quick, quickly, carefully, properly.

## Formatting

- Sentence case for headings: "Release flow", not "Release Flow". One `#` heading per file.
- No em dashes or en dashes. Use a comma, colon, parentheses, a hyphen or two sentences.
  Write ranges as "1 to 3" or "1-3".
- Straight quotes and apostrophes only.
- No emoji.
- Use bold rarely, for a word or two a reader must not miss. Never start list items with a bold
  label (`- **Speed:** fast`). Write a sentence, or use a table if it's really key and value.
- Lists for steps and real enumerations. Prose for explanations.
- Don't wrap ordinary words in backticks. Use them for commands, paths, flags, keys and code.

## Commits and PRs

- Conventional Commits subject in the imperative, under 72 characters, no trailing period.
- The body says what changed and why, then the verification command and its result.
