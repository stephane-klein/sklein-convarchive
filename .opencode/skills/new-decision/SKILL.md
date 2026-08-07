---
name: new-decision
description: Create a new Architecture Decision Record in docs/decisions/
---

# new-decision

Create a new Architecture Decision Record in `docs/decisions/`.

## Format

```
docs/decisions/YYYY-MM_NNN-description.md
```

Where:
- `YYYY-MM` — year and month of the decision
- `NNN` — zero-padded sequence number (increment highest existing)
- `description` — kebab-case slug

## Template

Copy the following and fill in:

```markdown
---
status: proposed | accepted | deprecated | superseded
date: YYYY-MM-DD
decision-makers: { list everyone involved in the decision }
ai-assistants: { list AI agents/LLMs used, e.g., "DeepSeek V4 Flash (OpenCode Go)" }
discussion: { link to issue / pull request / merge request where the decision was debated }
---

# ADR NNN — Short title

## Context and Problem Statement

Describe the context and problem statement. What is the issue we need to solve?

## Decision Drivers

- {driver 1, e.g., a constraint, quality attribute, or force}
- {driver 2}
- …

## Considered Options

- {option 1}
- {option 2}
- {option 3}

## Decision Outcome

Chosen option: "{option X}", because {justification}.

### Consequences

- Good, because {positive consequence}
- Bad, because {negative consequence}
- …

## Pros and Cons of the Options

### {option 1}

- Good, because {argument a}
- Bad, because {argument b}
- …

### {option 2}

- Good, because {argument a}
- Bad, because {argument b}
- …

## More Information

- {link to related issue / PR / MR where the decision was discussed}
- {link to other relevant resources}
```

## Workflow

1. Check the highest `NNN` in `docs/decisions/`
2. Create the new file with the next number
3. Update `docs/decisions/README.md` to add the entry
